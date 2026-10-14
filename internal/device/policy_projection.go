package device

import (
	"fmt"
	"strings"

	"github.com/yibaiba/hideck/internal/backend"
	"github.com/yibaiba/hideck/internal/cardpolicy"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/pkg/logger"
)

// cellularDataAllowed reports whether the software IMS path may bring up data.
// 驻网和流量分开：飞行关射频；网络只控制数据。
var suppressHostInternetPath = func(worker *Worker) {
	if worker == nil || worker.QMICore == nil {
		return
	}
	worker.QMICore.SuppressHostDataInterface()
}

func cellularDataAllowed(phoneMode, dataStrategy string, networkEnabled bool) bool {
	if strings.TrimSpace(phoneMode) != "cellular" {
		return false
	}
	return dataStrategy == "always" || networkEnabled
}

func shouldSuppressCellularRadio(cfg config.DeviceConfig) bool {
	if cfg.AirplaneEnabled {
		return true
	}
	// WiFi calling 关射频。蜂窝软件电话和原生 VoLTE 都要驻网。
	return PhoneServiceEnabled(cfg) && !PhoneModeCampsOnCell(cfg.PhoneMode)
}

// shouldEnterAirplaneOnDeviceBind 绑定设备时就要关射频：默认 VoWiFi / 飞行都不能先驻网。
func shouldEnterAirplaneOnDeviceBind(cfg config.DeviceConfig) bool {
	return shouldSuppressCellularRadio(cfg)
}

// applyPolicyToWorker 把卡策略投影进 worker.Config 的运行时有效字段。
// 不在此触发 re-apply，仅做纯投影，便于单测。
func applyPolicyToWorker(w *Worker, p cardpolicy.Policy) error {
	if w == nil {
		return nil
	}
	class, err := ClassifyWorkerLebaraUK(w)
	if err != nil {
		return fmt.Errorf("识别 Lebara UK 射频策略失败: %w", err)
	}
	w.Config.NetworkEnabled = p.NetworkEnabled
	w.Config.VoWiFiEnabled = p.VoWiFiEnabled
	w.Config.AirplaneEnabled = p.AirplaneEnabled
	w.Config.PhoneMode = p.PhoneMode
	w.Config.DataStrategy = p.DataStrategy
	if PhoneServiceEnabled(w.Config) {
		if PhoneModeCampsOnCell(p.PhoneMode) {
			if p.AirplaneEnabled {
				w.Config.NetworkEnabled = false
			} else if cellularAlwaysData(w.Config) {
				w.Config.NetworkEnabled = true
			}
		} else {
			w.Config.AirplaneEnabled = true
			w.Config.NetworkEnabled = false
		}
	}
	forceSoftwareIMSBlockedToVoLTE(w, p)
	w.Config.IPVersion = strings.TrimSpace(p.IPVersion)
	if w.Config.IPVersion == "" {
		w.Config.IPVersion = "v4"
	}
	w.Config.APN = strings.TrimSpace(p.APN)
	w.Config.SMSEnabled = true // SMS 恒开
	// 用投影后的有效网络开关。WiFi calling 已强制关流量，启动失败不得借此把射频拉回来。
	w.restoreNetworkAfterVoWiFi = w.Config.NetworkEnabled
	if class.IsLebara {
		applyLebaraUKRFLock(w)
	}
	w.setCellularRadioSuppressed(shouldSuppressCellularRadio(w.Config))
	return nil
}

func withConnectHoldRF(cfg config.DeviceConfig) config.DeviceConfig {
	cfg.ConnectHoldRF = true
	return cfg
}

func clearConnectHoldRF(w *Worker) {
	if w != nil {
		w.Config.ConnectHoldRF = false
	}
}

// holdRadioOffOnConnect 连接期暂扣射频：控制口刚恢复时先 RFOff，不改卡策略里的 AirplaneEnabled。
// 成功（已飞或刚切到 RFOff）后清掉 ConnectHoldRF，避免 QMI 后台重试再打一轮飞。
func (p *Pool) holdRadioOffOnConnect(w *Worker, reason string) {
	if w == nil {
		return
	}
	if reason == "" {
		reason = "connect_hold_rf"
	}
	w.setCellularRadioSuppressed(true)
	if p != nil && p.ctx != nil {
		_ = w.cancelRadioRegistrationReconcile(p.ctx, reason)
	}
	if nc := w.NetworkController(); nc != nil && nc.IsConnected() {
		_ = w.StopNetwork()
	}
	w.clearCachedIP()
	if w.Backend == nil {
		return
	}
	ctrl, ok := w.Backend.(backend.OperatingModeController)
	if !ok {
		logger.Warn("设备不支持射频控制，无法在连接期暂扣射频", "device", w.ID, "reason", reason)
		clearConnectHoldRF(w)
		return
	}
	if cur, err := ctrl.GetOperatingMode(p.ctx); err == nil && isPersistFlightOperatingMode(cur) {
		logger.Info("连接期射频已处于飞行，保持暂扣", "device", w.ID, "reason", reason)
		clearConnectHoldRF(w)
		return
	}
	if err := ctrl.SetOperatingMode(p.ctx, backend.ModeRFOff); err != nil {
		logger.Warn("连接期暂扣射频失败", "device", w.ID, "reason", reason, "err", err)
		return
	}
	clearConnectHoldRF(w)
	logger.Info("连接期已暂扣射频", "device", w.ID, "reason", reason)
}

// applyAfterQMIControlReady QMI 后台起来后收口：身份已在则按卡策略投影，
// 不再无条件 RFOff。身份未到且仍要先飞时才 hold 一次。
func (p *Pool) applyAfterQMIControlReady(worker *Worker, reason string) {
	if p == nil || worker == nil {
		return
	}
	if worker.CurrentICCID() != "" {
		clearConnectHoldRF(worker)
		p.resolveAndApplyPolicy(worker, reason)
		return
	}
	if worker.Config.ConnectHoldRF {
		p.holdRadioOffOnConnect(worker, "connect_hold_rf")
	}
	if err := p.applyNetworkPreference(worker); err != nil {
		logger.Warn("QMI 控制面就绪后应用网络偏好失败", "device", worker.ID, "reason", reason, "err", err)
	}
}

// enterAirplaneModeFromPolicy 按策略进入纯飞行：先断数据网，再把射频切到 RFOff。
// 已处于飞行则跳过。设备不支持射频控制时仅告警。
func (p *Pool) enterAirplaneModeFromPolicy(w *Worker, reason string) {
	if w == nil {
		return
	}
	w.setCellularRadioSuppressed(true)
	if p.ctx != nil {
		_ = w.cancelRadioRegistrationReconcile(p.ctx, reason)
	}
	if nc := w.NetworkController(); nc != nil && nc.IsConnected() {
		_ = w.StopNetwork()
	}
	w.clearCachedIP()
	ctrl, ok := w.Backend.(backend.OperatingModeController)
	if !ok {
		logger.Warn("设备不支持射频控制，无法投影飞行模式", "device", w.ID, "reason", reason)
		return
	}
	if cur, err := ctrl.GetOperatingMode(p.ctx); err == nil && isPersistFlightOperatingMode(cur) {
		return
	}
	if err := ctrl.SetOperatingMode(p.ctx, backend.ModeRFOff); err != nil {
		logger.Warn("投影飞行模式失败", "device", w.ID, "reason", reason, "err", err)
		return
	}
	logger.Info("已按策略进入飞行模式", "device", w.ID, "reason", reason)
}

// exitAirplaneModeIfNeeded 当设备当前处于飞行(RFOff/LowPower)且策略不要求飞行时，切回 Online。
func (p *Pool) exitAirplaneModeIfNeeded(w *Worker, reason string) {
	if w == nil {
		return
	}
	ctrl, ok := w.Backend.(backend.OperatingModeController)
	if !ok {
		return
	}
	cur, err := ctrl.GetOperatingMode(p.ctx)
	if err != nil || !isFlightOperatingMode(cur) {
		return
	}
	if err := ctrl.SetOperatingMode(p.ctx, backend.ModeOnline); err != nil {
		logger.Warn("退出飞行模式失败", "device", w.ID, "reason", reason, "err", err)
		return
	}
	logger.Info("已按策略退出飞行模式", "device", w.ID, "reason", reason)
}

// CurrentICCIDForDevice 返回指定设备当前 worker 的 ICCID（无 worker 或未就绪返回空串）。
func (p *Pool) CurrentICCIDForDevice(deviceID string) string {
	if p == nil {
		return ""
	}
	w := p.GetWorker(deviceID)
	if w == nil {
		return ""
	}
	return w.CurrentICCID()
}

// SetPolicyResolver 注入卡策略解析器（cmd/hideck 启动时调用）。
func (p *Pool) SetPolicyResolver(r cardpolicy.Resolver) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.policyResolver = r
	p.mu.Unlock()
}
