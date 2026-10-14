package qdc507

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yibaiba/hideck/internal/modemvoice"
)

const adbEnableTimeout = 45 * time.Second
const adbPollInterval = 250 * time.Millisecond
const adbATTimeout = 5 * time.Second
const adbUSBSettleTimeout = 10 * time.Second

var ErrADBRestarting = errors.New("已请求模组重启，等待设备重新发现后继续准备 ADB")

type ADBEnableOptions struct {
	USB, Firmware string
	Disabled      bool
	AT            func(context.Context, string, time.Duration) (string, error)
	Probe         func(context.Context, string) error
	Check         func(context.Context) error
	// Backup must durably save the original, non-secret USB command before any write.
	Backup func(string) error
	// Reboot must consume a durable one-shot reservation before issuing a reset.
	Reboot func(context.Context) error
}

// EnsureADB only changes the ADB bit of the verified firmware's USB layout.
// The caller owns the device AT gate throughout the transaction.
func EnsureADB(ctx context.Context, options ADBEnableOptions) error {
	return ensureADB(ctx, options, adbUSBSettleTimeout)
}

func ensureADB(ctx context.Context, options ADBEnableOptions, settle time.Duration) error {
	if err := options.validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, adbEnableTimeout)
	defer cancel()
	if err := options.Check(ctx); err != nil {
		return err
	}
	err := options.Probe(ctx, options.USB)
	if !errors.Is(err, ErrADBNotFound) {
		return err
	}
	if options.Disabled {
		return fmt.Errorf("qdc507: 自动开启 ADB 已禁用: %w", err)
	}
	config, err := options.readUSB(ctx)
	if err != nil {
		return err
	}
	written := config.flags[adbUSBFlag] == 0
	if written {
		if err := options.enable(ctx, config); err != nil {
			return err
		}
	}
	err = options.waitForADB(ctx, settle)
	if !written || !errors.Is(err, ErrADBNotFound) || ctx.Err() != nil || options.Reboot == nil {
		return err
	}
	config.flags[adbUSBFlag] = 1
	return options.rebootForADB(ctx, config)
}

func (options ADBEnableOptions) validate() error {
	if options.AT == nil || options.Probe == nil || options.Check == nil || options.Backup == nil {
		return fmt.Errorf("qdc507: ADB 准备依赖不完整")
	}
	if options.Firmware != "QDC507GLEFM21" || !usbLocation.MatchString(options.USB) {
		return fmt.Errorf("qdc507: 未适配的 ADB 固件或 USB 路径")
	}
	return nil
}

func (options ADBEnableOptions) readUSB(ctx context.Context) (usbConfig, error) {
	response, err := options.command(ctx, `AT+QCFG="usbcfg"?`)
	if err != nil {
		return usbConfig{}, fmt.Errorf("qdc507: 查询 USB 配置失败: %w", err)
	}
	return parseUSBConfig(response)
}

func (options ADBEnableOptions) idle(ctx context.Context) error {
	if err := options.Check(ctx); err != nil {
		return err
	}
	response, err := options.command(ctx, "AT+CLCC")
	if err != nil {
		return fmt.Errorf("qdc507: 检查通话状态失败: %w", err)
	}
	calls, err := modemvoice.ParseCLCC(response)
	if err != nil {
		return err
	}
	for _, call := range calls {
		// This firmware reports its standing packet-data bearers as mode 1
		// CLCC entries. They are not voice calls; keep fax/unknown modes guarded.
		if call.Mode != 1 {
			return fmt.Errorf("qdc507: 当前有通话，不能修改 USB 配置")
		}
	}
	return nil
}

func (options ADBEnableOptions) enable(ctx context.Context, original usbConfig) error {
	if err := options.idle(ctx); err != nil {
		return err
	}
	if err := options.Backup(original.command()); err != nil {
		return fmt.Errorf("qdc507: 备份 USB 配置失败，未修改模组: %w", err)
	}
	if err := options.authorize(ctx); err != nil {
		return err
	}
	current, err := options.readUSB(ctx)
	if err != nil {
		return err
	}
	if current != original {
		return fmt.Errorf("qdc507: USB 配置已变化，取消写入，请重新准备")
	}
	if err := options.idle(ctx); err != nil {
		return err
	}
	updated := original
	updated.flags[adbUSBFlag] = 1
	if _, err := options.command(ctx, updated.command()); err != nil {
		return fmt.Errorf("qdc507: 开启 ADB 的 USB 写入失败（可能已生效，请检查 USB 重枚举；未自动重启）: %w", err)
	}
	actual, err := options.readUSB(ctx)
	if err != nil {
		return fmt.Errorf("qdc507: USB 写入后复核失败（未自动重启）: %w", err)
	}
	if actual != updated {
		return fmt.Errorf("qdc507: 模组未应用预期的 USB 配置，ADB 准备失败")
	}
	return nil
}

func (options ADBEnableOptions) authorize(ctx context.Context) error {
	response, err := options.command(ctx, "AT+QADBKEY?")
	if err != nil {
		return adbAuthorizationError(ctx, "读取挑战失败")
	}
	challenge, err := parseADBChallenge(response)
	if err != nil {
		return err
	}
	key, err := deriveADBKey(challenge)
	if err != nil {
		return err
	}
	if err := options.Check(ctx); err != nil {
		return err
	}
	if _, err := options.command(ctx, `AT+QADBKEY="`+key+`"`); err != nil {
		return adbAuthorizationError(ctx, "提交失败，模组拒绝或 AT 传输异常")
	}
	return nil
}

func (options ADBEnableOptions) command(ctx context.Context, command string) (string, error) {
	if err := options.Check(ctx); err != nil {
		return "", err
	}
	response, err := options.AT(ctx, command, adbATTimeout)
	if err != nil {
		return "", err
	}
	// The transient QMI AT adapter returns terminal ERROR as response text;
	// the managed AT adapter instead returns an error and strips terminal OK.
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if line == "ERROR" || strings.HasPrefix(line, "+CME ERROR:") || strings.HasPrefix(line, "+CMS ERROR:") {
			return "", fmt.Errorf("qdc507: 模组拒绝 AT 指令（响应内容已隐藏）")
		}
	}
	return response, options.Check(ctx)
}

func adbAuthorizationError(ctx context.Context, stage string) error {
	// Raw AT errors may contain the echoed challenge/key.
	return errors.Join(fmt.Errorf("qdc507: ADB 授权%s（授权数据已隐藏），请检查固件及 AT 连接", stage), ctx.Err())
}

func (options ADBEnableOptions) waitForADB(parent context.Context, settle time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, settle)
	defer cancel()
	ticker := time.NewTicker(adbPollInterval)
	defer ticker.Stop()
	for {
		if err := options.Check(ctx); err != nil {
			return err
		}
		err := options.Probe(ctx, options.USB)
		if !errors.Is(err, ErrADBNotFound) {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.Join(fmt.Errorf("qdc507: USB 已开启 ADB，但连接未出现: %w", ErrADBNotFound), ctx.Err())
		case <-ticker.C:
		}
	}
}

func (options ADBEnableOptions) rebootForADB(ctx context.Context, expected usbConfig) error {
	current, err := options.readUSB(ctx)
	if err != nil {
		return err
	}
	if current != expected {
		return fmt.Errorf("qdc507: 重启前 USB 配置已变化，取消自动重启")
	}
	if err := options.idle(ctx); err != nil {
		return err
	}
	// ADB may have appeared while AT was queried; do not reset a working interface.
	if err := options.Probe(ctx, options.USB); !errors.Is(err, ErrADBNotFound) {
		return err
	}
	if err := options.Check(ctx); err != nil {
		return err
	}
	if err := options.Reboot(ctx); err != nil {
		return err
	}
	return ErrADBRestarting
}
