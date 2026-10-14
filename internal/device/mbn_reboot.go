package device

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yibaiba/hideck/internal/db"
	"github.com/yibaiba/hideck/internal/mbn"
	"github.com/yibaiba/hideck/pkg/logger"
)

func (s *mbnDeviceSession) Reboot(ctx context.Context, timeout time.Duration) error {
	t := s.pool.policyTransitionFor(s.owner.DeviceID)
	t.Lock()
	if err := s.validateLocked(); err != nil {
		t.Unlock()
		return err
	}
	w := s.worker
	if !s.writable {
		t.Unlock()
		return errors.New("只读 MBN 会话")
	}
	class, classErr := ClassifyWorkerLebaraUK(w)
	if classErr != nil {
		t.Unlock()
		return classErr
	}
	if shouldSuppressCellularRadio(w.Config) || class.IsLebara || class.BlocksVoWiFi() {
		t.Unlock()
		return errors.New("当前射频锁不允许 CFUN=1,1；未重启模组")
	}
	_, err := s.executeLocked(ctx, "AT+CFUN=1,1")
	t.Unlock()
	if err != nil {
		return fmt.Errorf("重启命令未确认: %w", err)
	}
	opts := defaultModemRebootRecoveryOptions(s.owner.DeviceID, "mbn_apply")
	opts.initialWorker = w
	opts.delays = manualRebootRecoveryDelays()
	opts.restoreVoWiFi = false
	go s.pool.runModemRebootRecovery(opts)
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("等待原模组重启恢复: %w", waitCtx.Err())
		case <-ticker.C:
		}
		next := s.pool.GetWorker(s.owner.DeviceID)
		if next == nil || next == w {
			continue
		}
		t.Lock()
		next.cacheMu.RLock()
		identity := next.state.Identity
		next.cacheMu.RUnlock()
		if !identity.Ready {
			t.Unlock()
			continue
		}
		if identity.IMEI != s.owner.IMEI || db.CanonicalICCID(identity.ICCID) != s.owner.ICCID || t.switchEpoch != s.epoch {
			t.Unlock()
			return mbn.ErrOwnerChanged
		}
		s.worker = next
		s.identityGeneration = identity.Generation
		s.owner.Generation = next.generation
		s.owner.IdentityGeneration = identity.Generation
		model, modelErr := s.executeLocked(waitCtx, "AT+CGMM")
		firmware, firmwareErr := s.executeLocked(waitCtx, "AT+CGMR")
		t.Unlock()
		if err := errors.Join(modelErr, firmwareErr); err != nil {
			return fmt.Errorf("重启后重新读取型号和固件: %w", err)
		}
		if mbnIdentityLine(model) != s.owner.Model || mbnIdentityLine(firmware) != s.owner.Firmware {
			return mbn.ErrOwnerChanged
		}
		logger.Info("MBN 模组重启已恢复，开始读回校验", "device", s.owner.DeviceID)
		return nil
	}
}
