package notify

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/device"
	"github.com/yibaiba/hideck/pkg/logger"
)

// Manager 统一通知管理器
// 持有多个 Channel 实例，向所有已启用渠道广播通知和命令
type Manager struct {
	pool                    *device.Pool
	updateMu                sync.Mutex
	channelsMu              sync.Mutex
	incomingMu              sync.Mutex
	lastIncomingKey         string
	lastIncomingAt          time.Time
	channels                []Channel // 所有已启用的通知渠道
	channelActivity         []*channelActivity
	deviceFilters           map[Channel]map[string]bool // guarded by channelsMu
	stateStore              RuntimeStateStore
	qqChannelFactory        func(config.QQConfig) (Channel, error)
	commandMu               sync.Mutex
	commandService          *CommandService
	commandExecutorMu       sync.RWMutex
	commandExecutor         ChannelCommandExecutor
	commandReceiversStarted bool
	confirmRegistry         *confirmRegistry
	notificationLocation    *time.Location
}

type ManagerOptions struct {
	StateStore                RuntimeStateStore
	QQChannelFactory          func(config.QQConfig) (Channel, error)
	DeferCommandReceiverStart bool
	NotificationLocation      *time.Location
}

type NotificationContext struct {
	Event       string
	Title       string
	Text        string
	DeviceID    string
	DeviceName  string
	Number      string
	LocalNumber string // the receiving SIM: its number, else an ICCID tail
	ContactName string
	Attribution string
	Content     string
	Timestamp   time.Time
}

func (c NotificationContext) DeviceLabel() string {
	id := strings.TrimSpace(c.DeviceID)
	name := strings.TrimSpace(c.DeviceName)
	if name != "" && id != "" {
		return fmt.Sprintf("%s (%s)", name, id)
	}
	if name != "" {
		return name
	}
	if id != "" {
		return id
	}
	return "未知设备"
}

type contextualChannel interface {
	SendWithContext(ctx NotificationContext) error
}

// NewManager 根据配置创建通知管理器，初始化所有已启用的通知渠道
func NewManager(cfg *config.Config, pool *device.Pool) (*Manager, error) {
	return NewManagerWithOptions(cfg, pool, ManagerOptions{})
}

func NewManagerWithOptions(cfg *config.Config, pool *device.Pool, options ManagerOptions) (*Manager, error) {
	m := &Manager{
		pool:                    pool,
		stateStore:              options.StateStore,
		qqChannelFactory:        options.QQChannelFactory,
		commandReceiversStarted: !options.DeferCommandReceiverStart,
		notificationLocation:    options.NotificationLocation,
	}
	m.commandService = m.newCommandService()

	// 初始化所有通知渠道
	if err := m.initChannels(cfg); err != nil {
		return nil, err
	}

	return m, nil
}

func (m *Manager) LoadRuntimeState() (RuntimeState, error) {
	if m == nil || m.stateStore == nil {
		return RuntimeState{}, ErrRuntimeStateStoreUnavailable
	}
	return m.stateStore.Load()
}

func (m *Manager) SaveRuntimeState(state RuntimeState) error {
	if m == nil || m.stateStore == nil {
		return ErrRuntimeStateStoreUnavailable
	}
	return m.stateStore.Save(state)
}

func (m *Manager) UpdateRuntimeState(update func(*RuntimeState) error) error {
	if m == nil || m.stateStore == nil {
		return ErrRuntimeStateStoreUnavailable
	}
	return m.stateStore.Update(update)
}

func (m *Manager) baseCommandHandlers() map[string]CommandHandler {
	m.confirmRegistry = newConfirmRegistry()
	return map[string]CommandHandler{
		"send":     m.handleCmdSendSMS,
		"status":   m.handleCmdStatus,
		"rotate":   m.handleCmdRotate,
		"list":     m.handleCmdList,
		"sms":      m.handleCmdSMSInbox,
		"esim":     m.handleCmdEsim,
		"switch":   m.handleCmdSwitch,
		"vocall":   m.handleCmdCall,
		"cellcall": m.handleCmdCellCall,
		"y":        m.handleCmdConfirmYes,
		"n":        m.handleCmdConfirmNo,
	}
}

func (m *Manager) CommandService() *CommandService {
	m.commandMu.Lock()
	defer m.commandMu.Unlock()
	if m.commandService == nil {
		m.commandService = m.newCommandService()
	}
	return m.commandService
}

func (m *Manager) newCommandService() *CommandService {
	service := NewCommandService(m.baseCommandHandlers())
	service.SetHelpDevicesProvider(m.helpDevices)
	return service
}

func (m *Manager) helpDevices() []HelpDevice {
	if m.pool == nil {
		return nil
	}
	labels := m.pool.WorkerLabels()
	devices := make([]HelpDevice, 0, len(labels))
	for _, label := range labels {
		if strings.TrimSpace(label.ID) == "" {
			continue
		}
		devices = append(devices, HelpDevice{ID: label.ID, Name: label.Name})
	}
	return devices
}

func (m *Manager) SetBalanceCommandHandler(handler CommandHandler) error {
	return m.CommandService().SetHandler("balance", handler)
}

// NotifySMS 实现 device.Notifier 接口 — 收到短信通知
func (m *Manager) NotifySMS(deviceID, sender, content string, timestamp time.Time) {
	m.NotifySMSWithSource(deviceID, sender, content, "蜂窝", timestamp)
}

func (m *Manager) NotifySMSWithSource(deviceID, sender, content, source string, timestamp time.Time) {
	m.notifySMS(device.InboundSMS{DeviceID: deviceID, Sender: sender, Content: content, Time: timestamp}, source)
}

// NotifySMSReceipt implements device.SMSReceiptNotifier.
func (m *Manager) NotifySMSReceipt(sms device.InboundSMS, source string) {
	m.notifySMS(sms, source)
}

func (m *Manager) notifySMS(sms device.InboundSMS, source string) {
	deviceID, sender, content, timestamp := sms.DeviceID, sms.Sender, sms.Content, sms.Time
	source = strings.TrimSpace(source)
	if source == "" {
		source = "蜂窝"
	}
	title := notificationTitle("sms_received")
	region := m.phoneNumberRegion(deviceID)
	local := localSIMLabel(sms.IMSI, sms.ICCID, region)
	fields := []string{"设备", m.deviceLabel(deviceID), "本机", local, "通道", source}
	fields = append(fields, phoneIdentityFieldsWithRegion("号码", sender, region)...)
	fields = append(fields, "时间", m.formatNotificationTime(timestamp), "内容", content)
	msg := notificationLines(title, fields...)

	logger.Info("开始发送短信通知",
		"event", "sms_received",
		"sms_device", deviceID,
		"source", source,
		"channel_count", m.channelCount())

	ctx := NotificationContext{
		Event:       "sms_received",
		Title:       title,
		Text:        msg,
		DeviceID:    deviceID,
		DeviceName:  m.resolveDeviceName(deviceID),
		Number:      sender,
		LocalNumber: local,
		Content:     content,
		Timestamp:   timestamp,
	}
	fillPhoneIdentityWithRegion(&ctx, sender, region)
	m.broadcastWithContext(ctx)
}

func (m *Manager) formatNotificationTime(timestamp time.Time) string {
	location := time.Local
	if m != nil && m.notificationLocation != nil {
		location = m.notificationLocation
	}
	return timestamp.In(location).Format("2006-01-02 15:04:05")
}

// NotifyRaw 发送原始文本通知到所有渠道
func (m *Manager) NotifyRaw(msg string) {
	m.broadcastWithContext(NotificationContext{
		Event:     "raw",
		Text:      msg,
		Timestamp: time.Now(),
	})
}

// NotifyIPRotated 实现 device.Notifier 接口 — IP 切换通知
func (m *Manager) NotifyIPRotated(deviceID, oldIP, newIP string, duration time.Duration) {
	title := notificationTitle("ip_rotated")
	msg := notificationLines(title,
		"设备", m.deviceLabel(deviceID),
		"旧 IP", oldIP,
		"新 IP", newIP,
		"耗时", duration.String(),
	)
	m.broadcastWithContext(NotificationContext{
		Event:      "ip_rotated",
		Title:      title,
		Text:       msg,
		DeviceID:   deviceID,
		DeviceName: m.resolveDeviceName(deviceID),
		Timestamp:  time.Now(),
	})
}

// NotifyIncomingCall 实现 voice.CallNotifier 接口 — 来电通知
func (m *Manager) NotifyIncomingCall(deviceID, caller, callee string) {
	channelCount := m.channelCount()
	if channelCount == 0 {
		return
	}
	key := strings.TrimSpace(deviceID) + "\x00" + strings.TrimSpace(caller) + "\x00" + strings.TrimSpace(callee)
	now := time.Now()
	m.incomingMu.Lock()
	if key != "" && key == m.lastIncomingKey && now.Sub(m.lastIncomingAt) < 2*time.Second {
		m.incomingMu.Unlock()
		return
	}
	m.lastIncomingKey = key
	m.lastIncomingAt = now
	m.incomingMu.Unlock()

	title := notificationTitle("incoming_call")
	region := m.phoneNumberRegion(deviceID)
	fields := []string{"设备", m.deviceLabel(deviceID)}
	fields = append(fields, phoneIdentityFieldsWithRegion("主叫", caller, region)...)
	fields = append(fields, "被叫", callee)
	msg := notificationLines(title, fields...)

	logger.Info("开始发送来电通知", "device", deviceID, "caller", caller, "channel_count", channelCount)

	ctx := NotificationContext{
		Event:      "incoming_call",
		Title:      title,
		Text:       msg,
		DeviceID:   deviceID,
		DeviceName: m.resolveDeviceName(deviceID),
		Number:     caller,
		Timestamp:  time.Now(),
	}
	fillPhoneIdentityWithRegion(&ctx, caller, region)
	m.broadcastWithContext(ctx)
}

// NotifyCallResult publishes the terminal result after the call state machine
// has classified it. It is intentionally separate from the immediate ring.
func (m *Manager) NotifyCallResult(deviceID, peer, direction, status, reason string, at time.Time) {
	if m.channelCount() == 0 {
		return
	}
	title := callResultTitle(status, reason)
	region := m.phoneNumberRegion(deviceID)
	message := callResultMessage{
		Device: m.deviceLabel(deviceID), Peer: peer, Direction: direction, Status: status, Reason: reason,
	}
	ctx := NotificationContext{
		Event:      "call_" + status,
		Title:      title,
		Text:       formatCallResultMessage(message, region),
		DeviceID:   deviceID,
		DeviceName: m.resolveDeviceName(deviceID),
		Number:     peer,
		Timestamp:  at,
	}
	fillPhoneIdentityWithRegion(&ctx, peer, region)
	m.broadcastWithContext(ctx)
}

type callResultMessage struct {
	Device    string // device label: name (id), or the bare id
	Peer      string
	Direction string
	Status    string
	Reason    string
}

func formatCallResultMessage(message callResultMessage, region string) string {
	fields := []string{"设备", message.Device}
	fields = append(fields, phoneIdentityFieldsWithRegion(
		callResultPeerField(message.Direction), message.Peer, region,
	)...)
	return notificationLines(callResultTitle(message.Status, message.Reason), fields...)
}

func callResultPeerField(direction string) string {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "inbound", "incoming":
		return "主叫"
	case "outbound", "outgoing":
		return "被叫"
	default:
		return "号码"
	}
}

func (m *Manager) resolveDeviceName(deviceID string) string {
	if strings.TrimSpace(deviceID) == "" || m.pool == nil {
		return ""
	}
	return strings.TrimSpace(m.pool.WorkerName(deviceID))
}

func (m *Manager) phoneNumberRegion(deviceID string) string {
	if m == nil || m.pool == nil {
		return ""
	}
	return m.pool.PhoneNumberRegion(deviceID)
}

func (m *Manager) deviceLabel(deviceID string) string {
	return (NotificationContext{DeviceID: deviceID, DeviceName: m.resolveDeviceName(deviceID)}).DeviceLabel()
}

func (m *Manager) broadcastWithContext(ctx NotificationContext) {
	ctx.Text = strings.TrimSpace(ctx.Text)
	if ctx.Text == "" {
		return
	}
	if ctx.Timestamp.IsZero() {
		ctx.Timestamp = time.Now()
	}
	if strings.TrimSpace(ctx.Event) == "" {
		ctx.Event = "notification"
	}

	deliveries := m.beginChannelSends()
	for _, delivery := range deliveries {
		delivery := delivery
		if !delivery.accepts(ctx) {
			logger.Debug("通知渠道按设备过滤，跳过", "channel", delivery.channel.Name(),
				"event", ctx.Event, "device", ctx.DeviceID)
			delivery.activity.sends.Done()
			continue
		}
		go func() {
			defer delivery.activity.sends.Done()
			ch := delivery.channel
			if withCtx, ok := ch.(contextualChannel); ok {
				if err := withCtx.SendWithContext(ctx); err != nil {
					logger.Warn("通知渠道发送失败", "channel", ch.Name(), "event", ctx.Event, "err", err)
				}
				return
			}
			if err := ch.Send(ctx.Text); err != nil {
				logger.Warn("通知渠道发送失败", "channel", ch.Name(), "event", ctx.Event, "err", err)
			}
		}()
	}
}
