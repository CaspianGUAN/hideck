package device

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yibaiba/hideck/internal/esim"
	"github.com/yibaiba/hideck/pkg/logger"
)

const (
	LebaraUKIdentityRecovering = "recovering"
	LebaraUKIdentityWaiting    = "waiting_identity"
	LebaraUKIdentityFailed     = "failed"

	lebaraUKIdentityRecoverMaxRounds = 2
	lebaraUKRecoverBusyRetries       = 3
)

var (
	ErrLebaraUKIdentityRecoverNoParking = errors.New("Lebara Profile 不允许停用，且没有可用的停车 Profile")
	ErrLebaraUKIdentityRecoverStopped   = errors.New("两轮清污后仍是 20404，已停止自动恢复")
	ErrLebaraUKIdentityNotLebara        = errors.New("当前不是 Lebara UK 分享卡")
	ErrLebaraUKIdentityICCIDMismatch    = errors.New("ICCID 不是当前 Lebara Profile")

	lebaraUKIdentityPollInterval = 2 * time.Second
	lebaraUKIdentityStableCount  = 3
	lebaraUKIdentityWaitTimeout  = 90 * time.Second
	lebaraUKProfileCycleSettle   = 10 * time.Second
	lebaraUKSwitchIdleTimeout    = 90 * time.Second
	lebaraUKRecoverBusyRetryWait = 800 * time.Millisecond

	lebaraUKDisableProfileHook func(context.Context, *Worker, string, string) error
	lebaraUKEnableProfileHook  func(context.Context, *Worker, string, string) error
)

type LebaraUKIdentityRecoverSnapshot struct {
	Status   string `json:"status,omitempty"`
	Message  string `json:"message,omitempty"`
	ICCID    string `json:"iccid,omitempty"`
	InFlight bool   `json:"-"`
	Attempts int    `json:"-"`
}

type lebaraUKIdentityRecoverState struct {
	ICCID     string
	Status    string
	Message   string
	Attempts  int
	InFlight  bool
	Manual    bool
	UpdatedAt time.Time
}

type lebaraUKParkingProfile struct {
	ICCID  string
	AIDHex string
	Name   string
	Class  string
}

type lebaraUKRecoverPlan struct {
	Mode          string
	TargetICCID   string
	TargetAIDHex  string
	ParkingICCID  string
	ParkingAIDHex string
}

func (p *Pool) LebaraUKIdentityRecoverSnapshot(deviceID string) LebaraUKIdentityRecoverSnapshot {
	if p == nil {
		return LebaraUKIdentityRecoverSnapshot{}
	}
	deviceID = strings.TrimSpace(deviceID)
	p.lebaraRecoverMu.Lock()
	defer p.lebaraRecoverMu.Unlock()
	state := p.lebaraRecover[deviceID]
	if state == nil {
		return LebaraUKIdentityRecoverSnapshot{}
	}
	return LebaraUKIdentityRecoverSnapshot{
		Status:   state.Status,
		Message:  state.Message,
		ICCID:    state.ICCID,
		InFlight: state.InFlight,
		Attempts: state.Attempts,
	}
}

func (p *Pool) lebaraUKRecoverBlocksVoWiFi(deviceID string) bool {
	snap := p.LebaraUKIdentityRecoverSnapshot(deviceID)
	return snap.InFlight || snap.Status == LebaraUKIdentityRecovering || snap.Status == LebaraUKIdentityWaiting
}

func (p *Pool) ScheduleLebaraUKIdentityRecover(w *Worker, iccid string, manual bool) error {
	return p.launchLebaraUKIdentityRecover(w, iccid, manual, false)
}

func (p *Pool) scheduleLebaraUKIdentityRecover(w *Worker, manual bool) {
	_ = p.launchLebaraUKIdentityRecover(w, "", manual, false)
}

func (p *Pool) launchLebaraUKIdentityRecover(w *Worker, iccid string, manual, fromSwitchFinalize bool) error {
	if p == nil || w == nil {
		return fmt.Errorf("设备不可用")
	}
	if w.EsimMgr == nil {
		return fmt.Errorf("设备没有 eSIM 管理器")
	}
	if !manual && !fromSwitchFinalize && p.IsESIMSwitching(w.ID) {
		logger.Info("Lebara 清污推迟：设备正在切卡", "device", w.ID)
		return nil
	}
	target := normalizeSIMIdentity(iccid)
	if target == "" {
		target = normalizeSIMIdentity(w.CurrentICCID())
	}
	if !p.lebaraUKRecoverICCIDAccepted(w, target) {
		return ErrLebaraUKIdentityICCIDMismatch
	}
	class, err := ClassifyWorkerLebaraUK(w)
	if err != nil {
		return fmt.Errorf("识别 Lebara UK 状态失败: %w", err)
	}
	if class.LiveHome23487 && normalizeSIMIdentityForCompare(w.CurrentICCID()) == normalizeSIMIdentityForCompare(target) {
		p.clearLebaraUKIdentityRecover(w.ID)
		return nil
	}
	if !class.IsLebara && p.pinnedLebaraUKRecoverICCID(w.ID) == "" {
		return ErrLebaraUKIdentityNotLebara
	}
	if !manual && !class.LiveFlipped && p.pinnedLebaraUKRecoverICCID(w.ID) == "" {
		return nil
	}
	if !p.beginLebaraUKIdentityRecover(w, target, manual) {
		snap := p.LebaraUKIdentityRecoverSnapshot(w.ID)
		if snap.InFlight {
			return nil
		}
		if !manual && snap.Attempts >= lebaraUKIdentityRecoverMaxRounds {
			return ErrLebaraUKIdentityRecoverStopped
		}
		if manual {
			return fmt.Errorf("无法开始恢复英国身份")
		}
		return nil
	}
	go p.runLebaraUKIdentityRecover(w, manual)
	return nil
}

func (p *Pool) lebaraUKRecoverICCIDAccepted(w *Worker, iccid string) bool {
	target := normalizeSIMIdentityForCompare(iccid)
	if target == "" {
		return false
	}
	if normalizeSIMIdentityForCompare(w.CurrentICCID()) == target {
		return true
	}
	return normalizeSIMIdentityForCompare(p.pinnedLebaraUKRecoverICCID(w.ID)) == target
}

func (p *Pool) pinnedLebaraUKRecoverICCID(deviceID string) string {
	if p == nil {
		return ""
	}
	p.lebaraRecoverMu.Lock()
	defer p.lebaraRecoverMu.Unlock()
	state := p.lebaraRecover[strings.TrimSpace(deviceID)]
	if state == nil {
		return ""
	}
	return state.ICCID
}

func (p *Pool) beginLebaraUKIdentityRecover(w *Worker, iccid string, manual bool) bool {
	if p == nil || w == nil {
		return false
	}
	deviceID := strings.TrimSpace(w.ID)
	pinned := normalizeSIMIdentity(iccid)
	if pinned == "" {
		pinned = normalizeSIMIdentity(w.CurrentICCID())
	}
	p.lebaraRecoverMu.Lock()
	if pinned == "" {
		if state := p.lebaraRecover[deviceID]; state != nil {
			pinned = normalizeSIMIdentity(state.ICCID)
		}
	}
	if deviceID == "" || pinned == "" {
		p.lebaraRecoverMu.Unlock()
		return false
	}
	if p.lebaraRecover == nil {
		p.lebaraRecover = make(map[string]*lebaraUKIdentityRecoverState)
	}
	state := p.lebaraRecover[deviceID]
	if state != nil && state.InFlight {
		p.lebaraRecoverMu.Unlock()
		return false
	}
	if state != nil && !manual && normalizeSIMIdentityForCompare(state.ICCID) == normalizeSIMIdentityForCompare(pinned) &&
		state.Attempts >= lebaraUKIdentityRecoverMaxRounds {
		state.Status = LebaraUKIdentityFailed
		state.Message = ErrLebaraUKIdentityRecoverStopped.Error()
		state.UpdatedAt = time.Now()
		p.lebaraRecoverMu.Unlock()
		return false
	}
	if state == nil || normalizeSIMIdentityForCompare(state.ICCID) != normalizeSIMIdentityForCompare(pinned) {
		state = &lebaraUKIdentityRecoverState{ICCID: pinned}
		p.lebaraRecover[deviceID] = state
	}
	if manual {
		state.Attempts = 0
	}
	state.ICCID = pinned
	state.InFlight = true
	state.Manual = manual
	state.Status = LebaraUKIdentityRecovering
	state.Message = "正在恢复英国身份"
	state.UpdatedAt = time.Now()
	p.lebaraRecoverMu.Unlock()
	return true
}

func (p *Pool) runLebaraUKIdentityRecover(w *Worker, manual bool) {
	if p == nil || w == nil {
		return
	}
	deviceID := strings.TrimSpace(w.ID)
	defer func() {
		if rec := recover(); rec != nil {
			logger.Error("Lebara 清污过程异常",
				"event", "LEBARA_UK_IDENTITY_RECOVER_PANIC",
				"device", deviceID,
				"panic", rec)
			p.finishLebaraUKIdentityRecoverFailed(deviceID, fmt.Errorf("eSIM 操作异常"))
		}
		p.lebaraRecoverMu.Lock()
		if state := p.lebaraRecover[deviceID]; state != nil {
			state.InFlight = false
			state.UpdatedAt = time.Now()
		}
		p.lebaraRecoverMu.Unlock()
		p.broadcastVoWiFiStateChange(deviceID)
	}()

	class, _ := ClassifyWorkerLebaraUK(w)
	p.stopLebaraUKFlippedVoWiFi(w, class)
	p.enforceLebaraUKRadioOff(w, "lebara_uk_identity_recover")
	p.broadcastVoWiFiStateChange(deviceID)
	if err := p.waitLebaraUKSwitchIdle(deviceID); err != nil {
		p.finishLebaraUKIdentityRecoverFailed(deviceID, err)
		return
	}

	rounds := 1
	if !manual {
		rounds = lebaraUKIdentityRecoverMaxRounds
	}
	var lastErr error
	for round := 1; round <= rounds; round++ {
		p.addLebaraUKIdentityRecoverAttempt(deviceID)
		lastErr = p.recoverLebaraUKIdentityOnce(w)
		if lastErr == nil {
			p.finishLebaraUKIdentityRecoverSuccess(w)
			return
		}
		logger.Warn("Lebara 英国身份清污一轮未成功",
			"event", "LEBARA_UK_IDENTITY_RECOVER_ROUND_FAILED",
			"device", deviceID,
			"round", round,
			"iccid", maskICCIDForLog(p.pinnedLebaraUKRecoverICCID(deviceID)),
			"err", lastErr)
		p.setLebaraUKIdentityRecoverMessage(deviceID, lastErr.Error())
	}
	p.finishLebaraUKIdentityRecoverFailed(deviceID, lastErr)
}

func (p *Pool) recoverLebaraUKIdentityOnce(w *Worker) error {
	if w == nil || w.EsimMgr == nil {
		return fmt.Errorf("设备没有 eSIM 管理器")
	}
	ctx := p.recoverContext()
	targetICCID := normalizeSIMIdentity(p.pinnedLebaraUKRecoverICCID(w.ID))
	if targetICCID == "" {
		targetICCID = normalizeSIMIdentity(w.CurrentICCID())
	}
	if targetICCID == "" {
		return fmt.Errorf("当前 ICCID 不可用")
	}

	groups := w.EsimMgr.CachedProfileGroups()
	disablingNotAllowed, pprKnown := w.EsimMgr.LookupProfilePPR(targetICCID)
	if !pprKnown {
		loaded, err := w.EsimMgr.GetProfiles()
		if err != nil {
			logger.Warn("Lebara 清污读取 Profile 列表失败", "device", w.ID, "err", err)
		} else {
			groups = loaded
			disablingNotAllowed, pprKnown = w.EsimMgr.LookupProfilePPR(targetICCID)
		}
	}
	plan, err := planLebaraUKIdentityRecover(groups, targetICCID, disablingNotAllowed, pprKnown)
	if err != nil {
		return err
	}

	p.setLebaraUKIdentityRecoverStatus(w.ID, LebaraUKIdentityRecovering, "正在重置 eSIM Profile")
	switch plan.Mode {
	case "self_cycle":
		if err := p.lebaraUKDisableProfile(ctx, w, plan.TargetICCID, plan.TargetAIDHex); err != nil {
			if plan.ParkingICCID == "" {
				return err
			}
			logger.Warn("Lebara 同卡停用失败，改走停车 Profile",
				"device", w.ID, "err", err)
			plan.Mode = "parking"
		} else {
			if err := p.waitLebaraUKSwitchIdle(w.ID); err != nil {
				logger.Warn("Lebara 停用后等待切卡结束超时，仍尝试重新启用",
					"device", w.ID, "err", err)
			}
			p.sleepLebaraUKSettle(ctx)
			if err := p.lebaraUKEnableProfile(ctx, w, plan.TargetICCID, plan.TargetAIDHex); err != nil {
				retryErr := p.lebaraUKEnableProfile(ctx, w, plan.TargetICCID, plan.TargetAIDHex)
				if retryErr != nil && plan.ParkingICCID != "" {
					logger.Warn("Lebara 重新启用失败，尝试经停车 Profile 拉回",
						"device", w.ID, "err", retryErr)
					if parkErr := p.lebaraUKSwitchThroughParking(ctx, w, plan); parkErr != nil {
						return parkErr
					}
				} else if retryErr != nil {
					return retryErr
				}
			}
		}
	}
	if plan.Mode == "parking" {
		if err := p.lebaraUKSwitchThroughParking(ctx, w, plan); err != nil {
			return err
		}
	}

	if err := p.waitLebaraUKSwitchIdle(w.ID); err != nil {
		return err
	}
	p.setLebaraUKIdentityRecoverStatus(w.ID, LebaraUKIdentityWaiting, "等待英国 IMSI 稳定")
	if err := p.waitLebaraUKHomeIdentity(ctx, w, plan.TargetICCID); err != nil {
		return err
	}
	if err := w.RefreshIdentityLive(ctx, "lebara_uk_identity_recover"); err != nil {
		logger.Warn("Lebara 清污后刷新身份缓存失败", "device", w.ID, "err", err)
	}
	class, err := ClassifyWorkerLebaraUK(w)
	if err != nil {
		return err
	}
	if !class.LiveHome23487 {
		if class.LiveFlipped {
			return NewLebaraUKFlippedIMSIError(class.LiveIMSI)
		}
		return fmt.Errorf("Lebara 身份尚未回到 23487")
	}
	return nil
}

func (p *Pool) lebaraUKSwitchThroughParking(ctx context.Context, w *Worker, plan lebaraUKRecoverPlan) error {
	if plan.ParkingICCID == "" {
		return ErrLebaraUKIdentityRecoverNoParking
	}
	if err := p.lebaraUKEnableProfile(ctx, w, plan.ParkingICCID, plan.ParkingAIDHex); err != nil {
		return err
	}
	if err := p.waitLebaraUKSwitchIdle(w.ID); err != nil {
		return err
	}
	p.sleepLebaraUKSettle(ctx)
	if err := p.lebaraUKEnableProfile(ctx, w, plan.TargetICCID, plan.TargetAIDHex); err != nil {
		if retryErr := p.lebaraUKEnableProfile(ctx, w, plan.TargetICCID, plan.TargetAIDHex); retryErr != nil {
			return retryErr
		}
	}
	return nil
}

func (p *Pool) finishLebaraUKIdentityRecoverSuccess(w *Worker) {
	if p == nil || w == nil {
		return
	}
	p.clearLebaraUKIdentityRecover(w.ID)
	p.enforceLebaraUKRadioOff(w, "lebara_uk_identity_recovered")
	p.broadcastVoWiFiStateChange(w.ID)
	if !w.Config.VoWiFiEnabled || UsesModemPhoneControl(w.Config.PhoneMode) {
		return
	}
	if err := p.waitLebaraUKSwitchIdle(w.ID); err != nil {
		logger.Warn("Lebara 身份恢复后等待切卡结束超时，暂不启动 VoWiFi", "device", w.ID, "err", err)
		return
	}
	class, err := ClassifyWorkerLebaraUK(w)
	if err != nil || class.BlocksVoWiFi() {
		return
	}
	p.clearDesiredVoWiFiRecoverState(w.ID)
	if err := p.voWiFiHost().Enable(p.recoverContext(), w.ID); err != nil {
		logger.Warn("Lebara 身份恢复后启动 VoWiFi 失败", "device", w.ID, "err", err)
	}
}

func (p *Pool) finishLebaraUKIdentityRecoverFailed(deviceID string, err error) {
	message := "恢复英国身份失败"
	if err != nil {
		message = err.Error()
	}
	p.lebaraRecoverMu.Lock()
	if state := p.lebaraRecover[deviceID]; state != nil {
		state.InFlight = false
		state.Status = LebaraUKIdentityFailed
		state.Message = message
		state.UpdatedAt = time.Now()
	}
	p.lebaraRecoverMu.Unlock()
	logger.Warn("Lebara 英国身份清污失败，保留 Profile 且不开 VoWiFi",
		"event", "LEBARA_UK_IDENTITY_RECOVER_FAILED",
		"device", deviceID,
		"err", err)
	p.broadcastVoWiFiStateChange(deviceID)
}

func (p *Pool) waitLebaraUKHomeIdentity(ctx context.Context, worker *Worker, targetICCID string) error {
	if worker == nil {
		return fmt.Errorf("worker is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := lebaraUKIdentityWaitTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	interval := lebaraUKIdentityPollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	need := lebaraUKIdentityStableCount
	if need <= 0 {
		need = 3
	}
	target := normalizeSIMIdentityForCompare(targetICCID)
	if target == "" {
		target = normalizeSIMIdentityForCompare(worker.CurrentICCID())
	}
	deadline := time.Now().Add(timeout)
	consecutive := 0
	lastIMSI := ""
	var lastErr error
	for {
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		iccid, imsi, err := readLebaraUKLiveIdentity(readCtx, worker)
		cancel()
		lastErr = err
		imsi = strings.TrimSpace(imsi)
		iccidKey := normalizeSIMIdentityForCompare(iccid)
		iccidOK := target != "" && iccidKey == target
		if err == nil && iccidOK && IsLebaraUKHomeIMSI(imsi) {
			if lastIMSI != "" && lastIMSI != imsi {
				consecutive = 1
			} else {
				consecutive++
			}
			lastIMSI = imsi
			if consecutive >= need {
				logger.Info("Lebara 英国身份已稳定",
					"event", "LEBARA_UK_IDENTITY_STABLE",
					"device", worker.ID,
					"iccid", maskICCIDForLog(iccid),
					"imsi_prefix", LebaraUKHomeIMSIPrefix)
				return nil
			}
		} else {
			consecutive = 0
			lastIMSI = imsi
			if IsLebaraUKFlippedIMSI(imsi) {
				logger.Warn("Lebara 身份仍为 20404，继续等待",
					"device", worker.ID,
					"iccid", maskICCIDForLog(iccid))
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			if IsLebaraUKFlippedIMSI(imsi) {
				return NewLebaraUKFlippedIMSIError(imsi)
			}
			if lastErr != nil {
				return fmt.Errorf("等待 Lebara 英国身份超时: %w", lastErr)
			}
			return fmt.Errorf("等待 Lebara 英国身份超时")
		}
		sleep := interval
		if sleep > remaining {
			sleep = remaining
		}
		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-p.doneCh():
			timer.Stop()
			return fmt.Errorf("设备池已停止")
		case <-timer.C:
		}
	}
}

func (p *Pool) doneCh() <-chan struct{} {
	if p == nil || p.ctx == nil {
		return nil
	}
	return p.ctx.Done()
}

func (p *Pool) recoverContext() context.Context {
	if p != nil && p.ctx != nil {
		return p.ctx
	}
	return context.Background()
}

func readLebaraUKLiveIdentity(ctx context.Context, w *Worker) (iccid, imsi string, err error) {
	if w == nil {
		return "", "", fmt.Errorf("worker is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if reader, ok := w.Backend.(liveSIMIdentityReader); ok {
		liveICCID, iccidErr := reader.GetICCIDLive(ctx)
		liveIMSI, imsiErr := reader.GetIMSILive(ctx)
		if imsiErr != nil {
			return strings.TrimSpace(liveICCID), strings.TrimSpace(liveIMSI), imsiErr
		}
		return strings.TrimSpace(liveICCID), strings.TrimSpace(liveIMSI), iccidErr
	}
	if w.Backend != nil {
		liveIMSI, imsiErr := w.Backend.GetIMSI(ctx)
		return w.CurrentICCID(), strings.TrimSpace(liveIMSI), imsiErr
	}
	return w.CurrentICCID(), w.GetCachedIMSI(), nil
}

func planLebaraUKIdentityRecover(groups []esim.EUICCProfiles, lebaraICCID string, disablingNotAllowed, pprKnown bool) (lebaraUKRecoverPlan, error) {
	target := normalizeSIMIdentityForCompare(lebaraICCID)
	plan := lebaraUKRecoverPlan{TargetICCID: strings.TrimSpace(lebaraICCID)}
	if item, aid, ok := findCachedProfile(groups, target); ok {
		plan.TargetICCID = item.ICCID
		plan.TargetAIDHex = aid
	}
	if parking, ok := pickLebaraUKParkingProfile(groups, target); ok {
		plan.ParkingICCID = parking.ICCID
		plan.ParkingAIDHex = parking.AIDHex
	}
	if pprKnown && !disablingNotAllowed {
		plan.Mode = "self_cycle"
		return plan, nil
	}
	if plan.ParkingICCID != "" {
		plan.Mode = "parking"
		return plan, nil
	}
	if !pprKnown {
		return plan, fmt.Errorf("无法确认 Profile Policy Rules，且没有停车 Profile")
	}
	return plan, ErrLebaraUKIdentityRecoverNoParking
}

func pickLebaraUKParkingProfile(groups []esim.EUICCProfiles, lebaraICCID string) (lebaraUKParkingProfile, bool) {
	target := normalizeSIMIdentityForCompare(lebaraICCID)
	var (
		best      lebaraUKParkingProfile
		bestScore int
		found     bool
	)
	homeAID := ""
	for _, group := range groups {
		for _, profile := range group.Profiles {
			if normalizeSIMIdentityForCompare(profile.ICCID) == target {
				homeAID = strings.ToUpper(strings.TrimSpace(group.AIDHex))
				break
			}
		}
	}
	for _, group := range groups {
		aid := strings.TrimSpace(group.AIDHex)
		sameAID := homeAID != "" && strings.EqualFold(aid, homeAID)
		for _, profile := range group.Profiles {
			if profile.State == 1 {
				continue
			}
			if normalizeSIMIdentityForCompare(profile.ICCID) == target {
				continue
			}
			iccid := strings.TrimSpace(profile.ICCID)
			if iccid == "" {
				continue
			}
			if profileNameLooksLikeLebaraUK(profile.Name) || profileNameLooksLikeLebaraUK(profile.ServiceProviderName) {
				continue
			}
			score := 1
			if sameAID {
				score += 100
			}
			class := strings.ToLower(strings.TrimSpace(profile.ClassText))
			switch class {
			case "provisioning":
				score += 40
			case "test":
				score += 30
			}
			if !found || score > bestScore {
				best = lebaraUKParkingProfile{
					ICCID:  iccid,
					AIDHex: aid,
					Name:   profile.Name,
					Class:  profile.ClassText,
				}
				bestScore = score
				found = true
			}
		}
	}
	return best, found
}

func findCachedProfile(groups []esim.EUICCProfiles, iccid string) (esim.ProfileItem, string, bool) {
	target := normalizeSIMIdentityForCompare(iccid)
	if target == "" {
		return esim.ProfileItem{}, "", false
	}
	for _, group := range groups {
		for _, profile := range group.Profiles {
			if normalizeSIMIdentityForCompare(profile.ICCID) == target {
				return profile, group.AIDHex, true
			}
		}
	}
	return esim.ProfileItem{}, "", false
}

func (p *Pool) lebaraUKDisableProfile(ctx context.Context, w *Worker, iccid, aidHex string) error {
	return p.lebaraUKProfileOp(ctx, w, iccid, aidHex, true)
}

func (p *Pool) lebaraUKEnableProfile(ctx context.Context, w *Worker, iccid, aidHex string) error {
	return p.lebaraUKProfileOp(ctx, w, iccid, aidHex, false)
}

func (p *Pool) lebaraUKProfileOp(ctx context.Context, w *Worker, iccid, aidHex string, disable bool) error {
	var lastErr error
	for attempt := 0; attempt <= lebaraUKRecoverBusyRetries; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(lebaraUKRecoverBusyRetryWait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		var err error
		if disable {
			if lebaraUKDisableProfileHook != nil {
				err = lebaraUKDisableProfileHook(ctx, w, iccid, aidHex)
			} else if w != nil && w.EsimMgr != nil {
				err = w.EsimMgr.DisableProfile(ctx, iccid, aidHex)
			} else {
				err = fmt.Errorf("设备没有 eSIM 管理器")
			}
		} else {
			if lebaraUKEnableProfileHook != nil {
				err = lebaraUKEnableProfileHook(ctx, w, iccid, aidHex)
			} else if w != nil && w.EsimMgr != nil {
				err = w.EsimMgr.SwitchProfile(ctx, iccid, aidHex)
			} else {
				err = fmt.Errorf("设备没有 eSIM 管理器")
			}
		}
		if err == nil || !errors.Is(err, esim.ErrOperationInProgress) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

func (p *Pool) waitLebaraUKSwitchIdle(deviceID string) error {
	timeout := lebaraUKSwitchIdleTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		if p == nil || !p.IsESIMSwitching(deviceID) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待 eSIM 切卡结束超时")
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-p.doneCh():
			timer.Stop()
			return fmt.Errorf("设备池已停止")
		case <-timer.C:
		}
	}
}

func (p *Pool) sleepLebaraUKSettle(ctx context.Context) {
	delay := lebaraUKProfileCycleSettle
	if delay <= 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-p.doneCh():
	case <-timer.C:
	}
}

func (p *Pool) clearLebaraUKIdentityRecover(deviceID string) {
	if p == nil {
		return
	}
	deviceID = strings.TrimSpace(deviceID)
	p.lebaraRecoverMu.Lock()
	delete(p.lebaraRecover, deviceID)
	p.lebaraRecoverMu.Unlock()
}

func (p *Pool) addLebaraUKIdentityRecoverAttempt(deviceID string) {
	p.lebaraRecoverMu.Lock()
	if state := p.lebaraRecover[deviceID]; state != nil {
		state.Attempts++
		state.UpdatedAt = time.Now()
	}
	p.lebaraRecoverMu.Unlock()
}

func (p *Pool) setLebaraUKIdentityRecoverStatus(deviceID, status, message string) {
	p.lebaraRecoverMu.Lock()
	if state := p.lebaraRecover[deviceID]; state != nil {
		state.Status = status
		state.Message = message
		state.UpdatedAt = time.Now()
	}
	p.lebaraRecoverMu.Unlock()
	p.broadcastVoWiFiStateChange(deviceID)
}

func (p *Pool) setLebaraUKIdentityRecoverMessage(deviceID, message string) {
	p.setLebaraUKIdentityRecoverStatus(deviceID, LebaraUKIdentityRecovering, message)
}

func maskICCIDForLog(iccid string) string {
	iccid = strings.TrimSpace(iccid)
	if iccid == "" {
		return ""
	}
	if len(iccid) <= 4 {
		return "****"
	}
	return "…" + iccid[len(iccid)-4:]
}
