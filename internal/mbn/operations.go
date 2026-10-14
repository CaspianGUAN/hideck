package mbn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yibaiba/hideck/pkg/logger"
)

func (s *Service) run(op Operation) {
	defer s.wg.Done()
	defer func() { s.mu.Lock(); delete(s.active, op.DeviceID); s.mu.Unlock() }()
	op.Status = "running"
	err := s.checkpoint(&op, "reading_original")
	var session Session
	if err == nil {
		acquireCtx, cancel := context.WithTimeout(s.ctx, duration(op.Request.RecoverySeconds, DefaultRecovery))
		session, err = s.gateway.Open(acquireCtx, op.DeviceID, true)
		cancel()
	}
	if err == nil {
		op.Original, err = inspect(s.ctx, session)
		if err == nil && !sameGeneration(op.ExpectedOwner, op.Original.Owner) {
			err = ErrOwnerChanged
		}
		if err == nil && op.Request.ExpectedOwner != nil && !sameOwner(*op.Request.ExpectedOwner, op.Original.Owner) {
			err = ErrOwnerChanged
		}
		if err == nil {
			err = s.checkpoint(&op, "original_saved")
		}
		if err == nil {
			err = s.perform(session, &op)
		}
	}
	if session != nil {
		resume := err == nil || op.Stage == "restored"
		closeErr := session.Close()
		if closeErr == nil && resume && s.ctx.Err() == nil {
			closeErr = session.Resume(s.ctx)
		}
		if closeErr != nil {
			op.Stage = "resuming"
			err = errors.Join(err, closeErr)
		}
	}
	op.Status = "succeeded"
	if err != nil {
		op.Status = "failed"
		op.Error = err.Error()
		if errors.Is(err, context.Canceled) {
			op.Status = "cancelled"
		}
		if errors.Is(err, ErrOwnerChanged) || op.Stage == "restoring" || op.Stage == "resuming" || op.Stage == "waiting_reboot" || op.Stage == "applying" || op.Stage == "preparing" || op.Stage == "verifying" || op.Stage == "deleting" || op.Stage == "importing" {
			op.Status = "needs_attention"
		}
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 5*time.Second)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	if persistErr := s.store.SaveOperation(persistCtx, &op); persistErr != nil {
		logger.Error("MBN 任务终态持久化失败", "operation", op.ID, "err", persistErr)
	}
}

func (s *Service) perform(session Session, op *Operation) error {
	if err := s.cancelled(op); err != nil {
		return err
	}
	if err := s.preflight(session, op); err != nil {
		return err
	}
	if err := s.checkpoint(op, "preparing"); err != nil {
		return err
	}
	if err := session.Prepare(s.ctx, op.Request); err != nil {
		return err
	}
	switch op.Request.Kind {
	case "apply":
		if !op.Request.AutoSel && !hasProfile(op.Original, op.Request.Profile) && op.Request.AssetID != "" {
			if err := s.importFile(session, op); err != nil {
				return err
			}
		}
		if err := s.apply(session, op, op.Request.Profile, op.Request.AutoSel); err != nil {
			return err
		}
		s.keepSessionChoice(op.Current)
		op.Warning = "已在模组生效；当前会话保留选择。要跨启动和切卡托管，请保存模组默认或 SIM 策略。"
		return nil
	case "import":
		return s.importFile(session, op)
	case "delete":
		return s.deleteProfile(session, op)
	case "trial":
		return s.trial(session, op)
	case "restore":
		previous, err := s.store.Operation(s.ctx, op.Request.RestoreOperation)
		if err != nil {
			return err
		}
		if !sameOwner(previous.Original.Owner, session.Owner()) {
			return ErrOwnerChanged
		}
		return s.restore(session, op, previous.Original)
	}
	return errors.New("未知操作")
}

func sameOwner(a, b Owner) bool {
	return a.IMEI != "" && a.ICCID != "" && a.IMEI == b.IMEI && a.ICCID == b.ICCID && a.Model == b.Model && a.Firmware == b.Firmware
}

func sameGeneration(a, b Owner) bool {
	return a.DeviceID == b.DeviceID && a.IMEI != "" && a.ICCID != "" && a.IMEI == b.IMEI && a.ICCID == b.ICCID && a.Generation == b.Generation && a.IdentityGeneration == b.IdentityGeneration
}

func (s *Service) apply(session Session, op *Operation, name string, auto bool) error {
	current, err := inspect(s.ctx, session)
	if err != nil {
		return err
	}
	version := op.Request.Version
	if op.Stage == "restoring" {
		version = ""
	}
	if err := validateApply(current, name, auto, version); err != nil {
		return err
	}
	if current.AutoSel == auto && (auto || activeProfile(current, name)) {
		op.Current = current
		return nil
	}
	if err := s.checkpoint(op, "applying"); err != nil {
		return err
	}
	value := 0
	if auto {
		value = 1
	}
	if _, err := execute(s.ctx, session, fmt.Sprintf(`AT+QMBNCFG="AutoSel",%d`, value)); err != nil {
		return err
	}
	if !auto {
		command, err := NamedCommand("Select", name)
		if err != nil {
			return err
		}
		if _, err := execute(s.ctx, session, command); err != nil {
			return err
		}
	}
	if err := s.checkpoint(op, "waiting_reboot"); err != nil {
		return err
	}
	if err := session.Reboot(s.ctx, duration(op.Request.RecoverySeconds, DefaultRecovery)); err != nil {
		return err
	}
	if err := s.checkpoint(op, "verifying"); err != nil {
		return err
	}
	op.Current, err = inspect(s.ctx, session)
	if err != nil {
		return err
	}
	if op.Current.AutoSel != auto || (!auto && !activeProfile(op.Current, name)) {
		return errors.New("重启读回与目标 MBN 不一致")
	}
	return nil
}

func hasProfile(s Snapshot, name string) bool {
	for _, p := range s.Profiles {
		if p.Name == name {
			return true
		}
	}
	return false
}
func activeProfile(s Snapshot, name string) bool {
	for _, p := range s.Profiles {
		if p.Name == name && p.Selected && p.Activated {
			return true
		}
	}
	return false
}

func (s *Service) restore(session Session, op *Operation, original Snapshot) error {
	if !sameOwner(original.Owner, session.Owner()) {
		return ErrOwnerChanged
	}
	if err := s.checkpoint(op, "restoring"); err != nil {
		return err
	}
	err := s.apply(session, op, original.Active(), original.AutoSel)
	if err != nil {
		op.Stage = "restoring"
		return fmt.Errorf("恢复原配置失败: %w", err)
	}
	s.keepSessionChoice(op.Current)
	op.Stage = "restored"
	return nil
}

func (s *Service) deleteProfile(session Session, op *Operation) error {
	if err := s.validateDelete(op); err != nil {
		return err
	}
	name := op.Request.Profile
	for _, p := range op.Original.Profiles {
		if p.Name != name || (!p.Selected && !p.Activated) {
			continue
		}
		if op.Request.Replacement == "" || op.Request.Replacement == name {
			return errors.New("使用中的 MBN 必须指定不同的替代配置")
		}
		if err := s.apply(session, op, op.Request.Replacement, false); err != nil {
			return err
		}
	}
	if err := s.cancelled(op); err != nil {
		return err
	}
	if err := s.checkpoint(op, "deleting"); err != nil {
		return err
	}
	command, err := NamedCommand("Delete", name)
	if err != nil {
		return err
	}
	if _, err := execute(s.ctx, session, command); err != nil {
		return err
	}
	if err := session.Reboot(s.ctx, duration(op.Request.RecoverySeconds, DefaultRecovery)); err != nil {
		return err
	}
	op.Current, err = inspect(s.ctx, session)
	if err != nil {
		return err
	}
	if hasProfile(op.Current, name) {
		return errors.New("重启后仍存在待删除 MBN")
	}
	s.keepSessionChoice(op.Current)
	return nil
}
