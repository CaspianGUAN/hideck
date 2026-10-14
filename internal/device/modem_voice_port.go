package device

import (
	"context"
	"errors"
	"time"
)

// AT/MBIM reuse their existing owner. Pure QMI uses the same transient AT gate
// as the terminal and polls CLCC; it never adds a competing serial reader.
type modemVoicePort struct {
	pool               *Pool
	worker             *Worker
	iccid              string
	identityGeneration uint64
}

func (port *modemVoicePort) check(ctx context.Context) error {
	if err := port.pool.CheckMBNIdle(port.worker.ID); err != nil {
		return err
	}
	if err := port.checkIdentity(ctx); err != nil {
		return err
	}
	if !port.pool.IsModemVoice(port.worker.ID) || port.worker.Config.AirplaneEnabled {
		return errors.New("模组直拨已关闭或设备处于飞行模式")
	}
	return nil
}

func (port *modemVoicePort) checkIdentity(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w := port.worker
	if port.pool.GetWorker(w.ID) != w || currentSIMOwnership(w) != (simOwnership{port.iccid, port.identityGeneration}) || port.pool.IsESIMSwitching(w.ID) {
		return errors.New("模组或 SIM 已更换，模组直拨会话已失效")
	}
	class, err := ClassifyWorkerLebaraUK(w)
	if err != nil {
		return err
	}
	if class.IsLebara || class.BlocksVoWiFi() {
		return ErrLebaraUKRFLocked
	}
	select {
	case <-w.stop:
		return errors.New("设备正在停止")
	default:
		return nil
	}
}

func (port *modemVoicePort) ExecuteATContext(ctx context.Context, command string, timeout time.Duration) (string, error) {
	release, err := port.pool.acquireDeviceAT(ctx, port.worker.ID)
	if err != nil {
		return "", err
	}
	defer release()
	check := port.check
	// Reading or ending the same SIM's call cannot enable RF or originate a
	// new call. Keep these available after a mode change so teardown can finish.
	if command == "AT+CLCC" || command == "AT+CHUP" {
		check = port.checkIdentity
	}
	if err := check(ctx); err != nil {
		return "", err
	}
	return port.pool.executeWorkerAT(ctx, port.worker, ATRequest{DeviceID: port.worker.ID, Command: command, Timeout: timeout})
}

func (port *modemVoicePort) SubscribeVoiceChanges() (<-chan struct{}, func()) {
	if port.worker.Modem.OwnsATRuntime() {
		return port.worker.Modem.SubscribeVoiceChanges()
	}
	// No URC owner in QMI mode: Session's real CLCC polling supplies changes.
	return make(chan struct{}), func() {}
}

func (port *modemVoicePort) WaitATIdle(ctx context.Context) error {
	release, err := port.pool.acquireDeviceAT(ctx, port.worker.ID)
	if err != nil {
		return err
	}
	defer release()
	if port.worker.Modem.OwnsATRuntime() {
		return port.worker.Modem.WaitATIdle(ctx)
	}
	return nil
}
