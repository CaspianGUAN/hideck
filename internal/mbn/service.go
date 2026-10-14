package mbn

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"
)

type Service struct {
	store           Store
	files           Files
	gateway         Gateway
	ctx             context.Context
	mu              sync.Mutex
	active          map[string]string
	overrides       map[string]Policy
	policySessionID string // Generations are process-local; do not reuse a previous process's requests.
	wg              sync.WaitGroup
}

type Options struct {
	Context context.Context
	Store   Store
	Files   Files
	Gateway Gateway
}

func New(opts Options) *Service {
	return &Service{store: opts.Store, files: opts.Files, gateway: opts.Gateway, ctx: opts.Context, active: map[string]string{}, overrides: map[string]Policy{}, policySessionID: newID()}
}

// Recover never replays a mutation whose terminal response was lost.
func (s *Service) Recover(ctx context.Context) error {
	operations, err := s.store.Operations(ctx, "")
	if err != nil {
		return err
	}
	for _, operation := range operations {
		if !operation.Running() {
			continue
		}
		operation.Status = "needs_attention"
		operation.Error = "服务在操作期间退出；请读取当前模组状态后明确选择恢复，不自动重发写命令"
		if err := s.store.SaveOperation(ctx, &operation); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Busy(deviceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[deviceID] != ""
}
func (s *Service) Wait() { s.wg.Wait() }
func (s *Service) Operations(ctx context.Context, id string) ([]Operation, error) {
	return s.store.Operations(ctx, id)
}
func (s *Service) Operation(ctx context.Context, id string) (Operation, error) {
	return s.store.Operation(ctx, id)
}

func (s *Service) Inspect(ctx context.Context, id string) (Snapshot, error) {
	if s.Busy(id) {
		return Snapshot{}, ErrConflict
	}
	session, err := s.gateway.Open(ctx, id, false)
	if err != nil {
		return Snapshot{}, err
	}
	defer session.Close()
	return inspect(ctx, session)
}

func inspect(ctx context.Context, session Session) (Snapshot, error) {
	snap := Snapshot{Owner: session.Owner()}
	raw, err := execute(ctx, session, "AT+QMBNCFG=?")
	if err != nil {
		return snap, err
	}
	snap.Capabilities, err = ParseCapabilities(raw)
	if err != nil {
		return snap, err
	}
	raw, err = execute(ctx, session, `AT+QMBNCFG="List"`)
	if err != nil {
		return snap, err
	}
	snap.Profiles, err = ParseList(raw)
	if err != nil {
		return snap, err
	}
	raw, err = execute(ctx, session, `AT+QMBNCFG="AutoSel"`)
	if err != nil {
		return snap, err
	}
	snap.AutoSel, err = ParseAutoSel(raw)
	return snap, err
}

func execute(ctx context.Context, session Session, command string) (string, error) {
	response, err := session.Execute(ctx, command)
	return response, CheckResponse(response, err)
}

func (s *Service) Cancel(ctx context.Context, id string) (Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, err := s.store.Operation(ctx, id)
	if err != nil {
		return op, err
	}
	if !op.Running() {
		return op, fmt.Errorf("%w: 任务已结束", ErrConflict)
	}
	op.CancelRequested = true
	op.Status = "cancelling"
	return op, s.store.SaveOperation(ctx, &op)
}

func (s *Service) checkpoint(op *Operation, stage string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.store.Operation(s.ctx, op.ID)
	if err != nil {
		return err
	}
	op.CancelRequested = current.CancelRequested
	op.Stage = stage
	if op.CancelRequested {
		op.Status = "cancelling"
	}
	return s.store.SaveOperation(s.ctx, op)
}

func (s *Service) cancelled(op *Operation) error {
	if err := s.checkpoint(op, op.Stage); err != nil {
		return err
	}
	if op.CancelRequested {
		return context.Canceled
	}
	return s.ctx.Err()
}

func newID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes[:])
}

func validateRequest(r Request) error {
	if r.RequestID == "" {
		return errors.New("request_id 必填")
	}
	for _, seconds := range []int{r.RecoverySeconds, r.RegistrationSeconds, r.StabilitySeconds} {
		if seconds < 0 || int64(seconds) > int64((1<<63-1)/time.Second)/2 {
			return errors.New("超时超出有效时长范围")
		}
	}
	switch r.Kind {
	case "apply":
		if !r.AutoSel {
			if err := ValidateName(r.Profile); err != nil {
				return err
			}
		}
	case "import":
		if r.AssetID == "" {
			return errors.New("请选择 MBN 文件")
		}
	case "delete":
		if err := ValidateName(r.Profile); err != nil {
			return err
		}
		if r.ConfirmName != r.Profile || !r.AcceptNoBackup {
			return errors.New("删除需输入精确名称并确认无法恢复的风险")
		}
	case "restore":
		if r.RestoreOperation == "" {
			return errors.New("请选择恢复记录")
		}
	case "trial":
		if len(r.Candidates) == 0 {
			return errors.New("试配候选为空")
		}
		if r.TargetPLMN != "" {
			if len(r.TargetPLMN) != 5 && len(r.TargetPLMN) != 6 {
				return errors.New("PLMN 必须是 5 或 6 位数字")
			}
			for _, digit := range r.TargetPLMN {
				if digit < '0' || digit > '9' {
					return errors.New("PLMN 必须是 5 或 6 位数字")
				}
			}
		}
		if r.Goal != "" && r.Goal != "registration" && r.Goal != "ims" && r.Goal != "data" {
			return errors.New("不支持的验证目标")
		}
		if r.Goal == "ims" && !r.AllowNativeIMS {
			return errors.New("原生 IMS 目标需要明确授权")
		}
		if r.Goal == "data" {
			u, err := url.Parse(r.ProbeURL)
			if !r.AllowData || err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
				return errors.New("数据验证需要授权及有效的 HTTP(S) 探测 URL（不能包含密码）")
			}
		}
		seen := map[string]bool{}
		for _, name := range r.Candidates {
			if err := ValidateName(name); err != nil {
				return err
			}
			if seen[name] {
				return errors.New("试配候选重复")
			}
			seen[name] = true
		}
	default:
		return errors.New("未知 MBN 任务类型")
	}
	return nil
}

func duration(seconds int, defaultValue time.Duration) time.Duration {
	if seconds == 0 {
		return defaultValue
	}
	return time.Duration(seconds) * time.Second
}
