package device

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/pkg/logger"
)

const modemADBRebootReason = "modem_voice_adb_reboot"

var adbModemIMEI = regexp.MustCompile(`^[0-9]{15}$`)

func (port *modemVoicePort) rebootADB(ctx context.Context, state string, schedule func(bool)) error {
	response, err := port.adbAT(ctx, "AT+CGSN", 5*time.Second)
	if err != nil {
		return fmt.Errorf("重启前读取模组身份失败: %w", err)
	}
	imei, err := parseADBModemIMEI(response)
	if err != nil {
		return err
	}
	if expected := port.worker.Config.ModemIMEI; expected != "" && !config.IMEIMatches(imei, expected) {
		return errors.New("重启前模组身份不匹配，取消 ADB 自动重启")
	}
	if err := port.check(ctx); err != nil {
		return err
	}
	if err := reserveADBReboot(state, imei); err != nil {
		return err
	}
	// The reservation remains consumed even if cancellation or loss of the AT
	// reply prevents us from knowing whether the reset reached the modem.
	response, err = port.adbAT(ctx, "AT+CFUN=1,1", 20*time.Second)
	if err == nil && strings.Contains(response, "ERROR") {
		return errors.New("模组拒绝 ADB 自动重启；已使用本次自动重启机会，请检查设备")
	}
	if err != nil && ctx.Err() != nil {
		return fmt.Errorf("ADB 重启任务已取消，不再调度旧任务: %w", err)
	}
	schedule(err == nil)
	if err != nil {
		return fmt.Errorf("ADB 自动重启响应未确认，已安排设备检测，不重复重启: %w", err)
	}
	logger.Info("ADB USB 配置已写入，目标模组执行一次自动重启", "device", port.worker.ID)
	return nil
}

func parseADBModemIMEI(response string) (string, error) {
	var imei string
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "OK" || line == "AT+CGSN" {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "+CGSN:"))
		if !adbModemIMEI.MatchString(line) || imei != "" {
			return "", errors.New("重启前未获得唯一有效的模组 IMEI")
		}
		imei = line
	}
	if imei == "" {
		return "", errors.New("重启前模组 IMEI 为空")
	}
	return imei, nil
}

func reserveADBReboot(state, imei string) error {
	if !adbModemIMEI.MatchString(imei) {
		return errors.New("无法记录 ADB 重启次数：无效的模组身份")
	}
	directory := filepath.Join(state, "usb-reboot-attempts")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	identity := sha256.Sum256([]byte("QDC507GLEFM21:" + imei))
	path := filepath.Join(directory, fmt.Sprintf("%x.once", identity))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("此模组已尝试自动重启，不再重复；请检查 ADB/USB。重试记录：%s", path)
	}
	if err != nil {
		return fmt.Errorf("保存 ADB 重启记录失败，未重启: %w", err)
	}
	_, writeErr := file.WriteString("ADB USB activation reboot reserved\n")
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func (port *modemVoicePort) scheduleADBRebootRecovery(confirmed bool) {
	opts := defaultModemRebootRecoveryOptions(port.worker.ID, modemADBRebootReason)
	opts.delays = manualRebootRecoveryDelays()
	opts.restoreVoWiFi = false // Recovery re-reads current card policy, never a pre-reboot mode.
	opts.removeBeforeScan = confirmed
	opts.initialWorker = port.worker
	go port.pool.runModemRebootRecovery(opts)
}

func (p *Pool) adbRecoveryReplaced(opts modemRebootRecoveryOptions) bool {
	current := p.GetWorker(opts.deviceID)
	return opts.initialWorker != nil && current != nil && current != opts.initialWorker
}

// An asynchronous reset must not evict a worker installed by hotplug meanwhile.
func (p *Pool) removeInitialRecoveryWorker(opts modemRebootRecoveryOptions) error {
	if opts.initialWorker == nil {
		return p.RemoveWorker(opts.deviceID)
	}
	transition := p.policyTransitionFor(opts.deviceID)
	transition.Lock()
	defer transition.Unlock()
	current := p.GetWorker(opts.deviceID)
	if current == nil {
		return nil
	}
	if current != opts.initialWorker {
		return errors.New("ADB 重启后已有新 worker，交由新会话接管")
	}
	return p.removeWorkerForPolicyTransition(opts.deviceID)
}
