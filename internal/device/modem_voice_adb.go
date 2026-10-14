package device

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yibaiba/hideck/internal/modemvoice/host"
	"github.com/yibaiba/hideck/internal/modemvoice/qdc507"
	"github.com/yibaiba/hideck/pkg/logger"
)

func (port *modemVoicePort) prepareADB(ctx context.Context, client *qdc507.ADB, state string) error {
	if err := port.check(ctx); err != nil {
		return err
	}
	usb := filepath.Base(port.worker.Config.USBPath)
	if err := client.Probe(ctx, usb); !errors.Is(err, qdc507.ErrADBNotFound) {
		return err
	}
	// Keep manual AT writes from racing the USB read/authorize/write sequence.
	release, err := port.pool.acquireDeviceAT(ctx, port.worker.ID)
	if err != nil {
		return err
	}
	defer release()
	disabled := port.pool.cfg != nil && !port.pool.cfg.ModemVoice.ADBEnableAllowed()
	err = qdc507.EnsureADB(ctx, qdc507.ADBEnableOptions{
		USB: usb, Firmware: "QDC507GLEFM21", Disabled: disabled,
		Probe: client.Probe, Check: port.check,
		AT: port.adbAT,
		Reboot: func(ctx context.Context) error {
			return port.rebootADB(ctx, state, port.scheduleADBRebootRecovery)
		},
		Backup: func(original string) error {
			path, err := saveModemUSBBackup(filepath.Join(state, "usb-backups"), usb, original)
			if err == nil {
				logger.Info("模组直拨准备自动开启 ADB，原 USB 配置已备份", "device", port.worker.ID, "usb", usb, "backup", path)
			}
			return err
		},
	})
	if errors.Is(err, qdc507.ErrADBRestarting) {
		return fmt.Errorf("%w: %v", host.ErrPrepareRestarting, err)
	}
	return err
}

// The caller must hold acquireDeviceAT; identity is checked before every write.
func (port *modemVoicePort) adbAT(ctx context.Context, command string, timeout time.Duration) (string, error) {
	if err := port.check(ctx); err != nil {
		return "", err
	}
	return port.pool.executeWorkerAT(ctx, port.worker, ATRequest{
		DeviceID: port.worker.ID, Command: command, Timeout: timeout,
	})
}

// This infrastructure adapter stores no SIM identity, challenge or authorization key.
func saveModemUSBBackup(directory, usb, original string) (string, error) {
	if filepath.Base(usb) != usb || usb == "." || usb == ".." {
		return "", fmt.Errorf("无效的 USB 备份路径")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(directory, usb+"-*.json")
	if err != nil {
		return "", err
	}
	record := struct {
		USB            string    `json:"usb"`
		Firmware       string    `json:"firmware"`
		RestoreCommand string    `json:"restore_command"`
		CreatedAt      time.Time `json:"created_at"`
	}{usb, "QDC507GLEFM21", original, time.Now()}
	writeErr := json.NewEncoder(file).Encode(record)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return "", err
	}
	return file.Name(), nil
}
