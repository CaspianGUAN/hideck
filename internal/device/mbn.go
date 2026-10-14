package device

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/yibaiba/hideck/internal/db"
	"github.com/yibaiba/hideck/internal/mbn"
)

type MBNGateway struct {
	Pool  *Pool
	Probe func(context.Context, string, string) error
}
type mbnDeviceSession struct {
	pool               *Pool
	worker             *Worker
	owner              mbn.Owner
	epoch              uint64
	identityGeneration uint64
	writable           bool
	goal               string
	imsWorker          *Worker
	probe              func(context.Context, string, string) error
}

func (p *Pool) SetMBNService(s *mbn.Service) { p.mbnService = s }
func (p *Pool) MBNService() *mbn.Service     { return p.mbnService }

func (g MBNGateway) Identity(ctx context.Context, id string) (mbn.Owner, error) {
	if err := ctx.Err(); err != nil {
		return mbn.Owner{}, err
	}
	w := g.Pool.GetWorker(id)
	if w == nil {
		return mbn.Owner{}, mbn.ErrOwnerChanged
	}
	owner := mbnWorkerOwner(w)
	w.cacheMu.RLock()
	ready := w.state.Identity.Ready
	w.cacheMu.RUnlock()
	if !ready || owner.IMEI == "" || owner.ICCID == "" || g.Pool.IsESIMSwitching(id) {
		return owner, errors.New("模组或 SIM 身份未就绪")
	}
	return owner, nil
}

func (p *Pool) CheckMBNIdle(id string) error {
	if p == nil {
		return errors.New("设备管理器未初始化")
	}
	transition := p.policyTransitionFor(id)
	if transition.mbnSession.Load() != nil {
		return mbn.ErrConflict
	}
	return nil
}

func (p *Pool) AcquireDeviceOperation(id string) (func(), error) {
	if p == nil {
		return nil, errors.New("设备管理器未初始化")
	}
	t := p.policyTransitionFor(id)
	if !t.operations.TryRLock() {
		return nil, mbn.ErrConflict
	}
	if t.mbnSession.Load() != nil {
		t.operations.RUnlock()
		return nil, mbn.ErrConflict
	}
	return t.operations.RUnlock, nil
}

func (g MBNGateway) Open(ctx context.Context, id string, writable bool) (mbn.Session, error) {
	p := g.Pool
	transition := p.policyTransitionFor(id)
	if writable {
		if err := waitMBNExclusive(ctx, transition); err != nil {
			return nil, err
		}
	} else if !transition.operations.TryRLock() {
		return nil, mbn.ErrConflict
	}
	opened := false
	defer func() {
		if !opened {
			if writable {
				transition.operations.Unlock()
			} else {
				transition.operations.RUnlock()
			}
		}
	}()
	transition.Lock()
	defer transition.Unlock()
	if transition.mbnSession.Load() != nil {
		return nil, mbn.ErrConflict
	}
	w := p.GetWorker(id)
	if w == nil || w.Backend == nil || w.Modem == nil {
		return nil, errors.New("模组控制接口未就绪")
	}
	if p.IsESIMSwitching(id) || w.IsOperatorScanActive() {
		return nil, mbn.ErrConflict
	}
	w.cacheMu.RLock()
	identity, firmware := w.state.Identity, w.state.Runtime.Firmware
	w.cacheMu.RUnlock()
	if identity.IMEI == "" || identity.ICCID == "" || !identity.Ready {
		return nil, errors.New("等待模组和 SIM 身份就绪")
	}
	s := &mbnDeviceSession{pool: p, worker: w, epoch: transition.switchEpoch, identityGeneration: identity.Generation, writable: writable, probe: g.Probe,
		owner: mbn.Owner{DeviceID: id, IMEI: identity.IMEI, ICCID: db.CanonicalICCID(identity.ICCID), Firmware: firmware, Generation: w.generation, IdentityGeneration: identity.Generation}}
	if writable {
		if p.voiceGateway != nil && p.voiceGateway.ActiveCall(id) != nil {
			return nil, errors.New("通话中不能修改 MBN")
		}
		if p.volteCtl != nil && p.volteCtl.ActiveCall(id) != nil {
			return nil, errors.New("通话中不能修改 MBN")
		}
		if p.modemVoiceCtl != nil && p.modemVoiceCtl.ActiveCall(id) != nil {
			return nil, errors.New("通话中不能修改 MBN")
		}
	}
	if writable {
		transition.mbnSession.Store(s)
	}
	response, err := s.executeLocked(ctx, "AT+CGMM")
	if err != nil {
		transition.mbnSession.Store(nil)
		return nil, err
	}
	s.owner.Model = mbnIdentityLine(response)
	if s.owner.Model == "" {
		transition.mbnSession.Store(nil)
		return nil, errors.New("模组型号未读取成功")
	}
	response, err = s.executeLocked(ctx, "AT+CGMR")
	if err != nil {
		transition.mbnSession.Store(nil)
		return nil, err
	}
	s.owner.Firmware = mbnIdentityLine(response)
	if s.owner.Firmware == "" {
		transition.mbnSession.Store(nil)
		return nil, errors.New("未读取到模组固件")
	}
	opened = true
	return s, nil
}

// Policy reconciliation may enqueue a job from the tail of an existing mode or
// SIM change. Wait for that owner to leave; do not turn scheduling order into a
// permanently failed policy. The caller supplies the operation's deadline.
func waitMBNExclusive(ctx context.Context, transition *policyTransition) error {
	const leasePollInterval = 50 * time.Millisecond
	ticker := time.NewTicker(leasePollInterval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if transition.operations.TryLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func mbnIdentityLine(response string) string {
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != "OK" && !strings.HasPrefix(line, "AT") {
			return line
		}
	}
	return ""
}

func (s *mbnDeviceSession) Owner() mbn.Owner { return s.owner }
func (s *mbnDeviceSession) validateLocked() error {
	t := s.pool.policyTransitionFor(s.owner.DeviceID)
	if (s.writable && t.mbnSession.Load() != s) || t.switchEpoch != s.epoch || s.pool.GetWorker(s.owner.DeviceID) != s.worker {
		return mbn.ErrOwnerChanged
	}
	w := s.worker
	w.cacheMu.RLock()
	identity := w.state.Identity
	w.cacheMu.RUnlock()
	if !identity.Ready || db.CanonicalICCID(identity.ICCID) != s.owner.ICCID || identity.IMEI != s.owner.IMEI || identity.Generation != s.identityGeneration {
		return mbn.ErrOwnerChanged
	}
	return nil
}
func (s *mbnDeviceSession) Execute(ctx context.Context, command string) (string, error) {
	t := s.pool.policyTransitionFor(s.owner.DeviceID)
	t.Lock()
	defer t.Unlock()
	if err := s.validateLocked(); err != nil {
		return "", err
	}
	return s.executeLocked(ctx, command)
}
func (s *mbnDeviceSession) executeLocked(ctx context.Context, command string) (string, error) {
	release, err := s.pool.acquireDeviceAT(ctx, s.owner.DeviceID)
	if err != nil {
		return "", err
	}
	defer release()
	response, err := s.pool.executeWorkerAT(ctx, s.worker, ATRequest{DeviceID: s.owner.DeviceID, Command: command, Timeout: 10 * time.Second})
	return response, mbn.CheckResponse(response, err)
}

func (s *mbnDeviceSession) Close() error {
	t := s.pool.policyTransitionFor(s.owner.DeviceID)
	if !s.writable {
		t.operations.RUnlock()
		return nil
	}
	t.Lock()
	released := t.mbnSession.CompareAndSwap(s, nil)
	t.Unlock()
	if !released {
		return nil
	}
	t.operations.Unlock()
	return nil
}

func (s *mbnDeviceSession) Resume(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.worker != nil && s.writable && s.pool.GetWorker(s.owner.DeviceID) == s.worker && db.CanonicalICCID(s.worker.CurrentICCID()) == s.owner.ICCID {
		result := s.pool.resolveAndApplyPolicy(s.worker, "mbn_operation_finished")
		return result.Err
	}
	return mbn.ErrOwnerChanged
}

func (s *mbnDeviceSession) Upload(ctx context.Context, name string, data []byte) error {
	t := s.pool.policyTransitionFor(s.owner.DeviceID)
	t.Lock()
	defer t.Unlock()
	if err := s.validateLocked(); err != nil {
		return err
	}
	release, err := s.pool.acquireDeviceAT(ctx, s.owner.DeviceID)
	if err != nil {
		return err
	}
	defer release()
	if s.worker.Modem.OwnsATRuntime() {
		return s.worker.Modem.UploadFile(ctx, name, data)
	}
	port, err := s.pool.openATSession(s.worker.ResolvedATPort())
	if err != nil {
		return err
	}
	uploader, ok := port.(interface {
		UploadFile(context.Context, string, []byte) error
	})
	if !ok {
		return errors.Join(errors.New("当前 AT 适配器不支持二进制上传"), port.Close())
	}
	return errors.Join(uploader.UploadFile(ctx, name, data), port.Close())
}
