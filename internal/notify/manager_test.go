package notify

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/pkg/logger"
)

type captureChannel struct {
	mu    sync.Mutex
	msgs  []string
	calls []NotificationContext
}

type blockingChannel struct {
	started   chan struct{}
	release   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

type namedLifecycleChannel struct {
	name      string
	stopOnce  sync.Once
	closeOnce sync.Once
	stopped   chan struct{}
	closed    chan struct{}
}

func (c *namedLifecycleChannel) Name() string                           { return c.name }
func (c *namedLifecycleChannel) Send(string) error                      { return nil }
func (c *namedLifecycleChannel) RegisterCommand(string, CommandHandler) {}
func (c *namedLifecycleChannel) Start() error                           { return nil }
func (c *namedLifecycleChannel) StopReceivingCommands() {
	if c.stopped != nil {
		c.stopOnce.Do(func() { close(c.stopped) })
	}
}
func (c *namedLifecycleChannel) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

type revocableBlockingChannel struct {
	started   chan struct{}
	release   chan struct{}
	stopped   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
	closeOnce sync.Once
}

func (c *revocableBlockingChannel) Name() string { return "telegram" }
func (c *revocableBlockingChannel) Send(string) error {
	c.startOnce.Do(func() { close(c.started) })
	<-c.release
	return nil
}
func (c *revocableBlockingChannel) RegisterCommand(string, CommandHandler) {}
func (c *revocableBlockingChannel) Start() error                           { return nil }
func (c *revocableBlockingChannel) StopReceivingCommands() {
	c.stopOnce.Do(func() { close(c.stopped) })
}
func (c *revocableBlockingChannel) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *blockingChannel) Name() string { return "blocking" }
func (c *blockingChannel) Send(string) error {
	c.startOnce.Do(func() { close(c.started) })
	<-c.release
	return nil
}
func (c *blockingChannel) RegisterCommand(string, CommandHandler) {}
func (c *blockingChannel) Start() error                           { return nil }
func (c *blockingChannel) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *captureChannel) Name() string { return "capture" }

func (c *captureChannel) Send(text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, text)
	return nil
}

func (c *captureChannel) SendWithContext(ctx NotificationContext) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, ctx)
	c.msgs = append(c.msgs, ctx.Text)
	return nil
}

func (c *captureChannel) RegisterCommand(_ string, _ CommandHandler) {}
func (c *captureChannel) Start() error                               { return nil }
func (c *captureChannel) Close() error                               { return nil }

func (c *captureChannel) Last() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.msgs) == 0 {
		return ""
	}
	return c.msgs[len(c.msgs)-1]
}

func (c *captureChannel) LastContext() NotificationContext {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) == 0 {
		return NotificationContext{}
	}
	return c.calls[len(c.calls)-1]
}

func readLogFields(t *testing.T, entry logger.LogEntry) map[string]any {
	t.Helper()
	if entry.Fields == "" {
		return map[string]any{}
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(entry.Fields), &fields); err != nil {
		t.Fatalf("failed to parse log fields: %v", err)
	}
	return fields
}

func waitLogEntry(t *testing.T, ch <-chan logger.LogEntry, match func(entry logger.LogEntry) bool) logger.LogEntry {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case entry := <-ch:
			if match(entry) {
				return entry
			}
		case <-deadline:
			t.Fatal("matched log entry not found")
		}
	}
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func TestManagerNotifyEventsToWebhookWithTemplate(t *testing.T) {
	var mu sync.Mutex
	var payloads []webhookPayload

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload webhookPayload
		_ = json.Unmarshal(body, &payload)
		mu.Lock()
		payloads = append(payloads, payload)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh, err := NewWebhookChannel(webhookConfigForTest(srv.URL, "[{{device_label}}] {{text}}"))
	if err != nil {
		t.Fatalf("NewWebhookChannel() error = %v", err)
	}

	m := &Manager{channels: []Channel{wh}, notificationLocation: time.UTC}

	ts := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	m.NotifySMS("wwan0", "+8613800000000", "hello", ts)
	m.NotifyIPRotated("wwan0", "1.1.1.1", "2.2.2.2", 2*time.Second)
	m.NotifyRaw("raw message")

	waitUntil(t, time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(payloads) == 3
	})
	mu.Lock()
	defer mu.Unlock()
	if len(payloads) != 3 {
		t.Fatalf("payload count=%d, want=3", len(payloads))
	}
	byEvent := make(map[string]webhookPayload, len(payloads))
	for _, payload := range payloads {
		byEvent[payload.Event] = payload
	}
	if got := byEvent["sms_received"].Text; got != "[wwan0] "+wantSMSText("wwan0", "蜂窝", "+8613800000000", "2026-04-13 12:00:00", "hello") {
		t.Fatalf("sms text=%q", got)
	}
	if got := byEvent["ip_rotated"].Meta.DeviceID; got != "wwan0" {
		t.Fatalf("ip_rotated meta.device_id=%q", got)
	}
	if _, ok := byEvent["raw"]; !ok {
		t.Fatal("raw event missing")
	}
}

func TestManagerNotifyRawKeepsPlainChannelText(t *testing.T) {
	capture := &captureChannel{}
	m := &Manager{channels: []Channel{capture}}

	m.NotifyRaw("plain channel text")
	waitUntil(t, time.Second, func() bool { return capture.Last() != "" })
	if got := capture.Last(); got != "plain channel text" {
		t.Fatalf("plain channel text=%q", got)
	}
}

func TestManagerCloseWaitsForInFlightChannelSend(t *testing.T) {
	channel := &blockingChannel{
		started: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{}),
	}
	manager := &Manager{channels: []Channel{channel}}
	manager.NotifyRaw("wait for delivery")
	select {
	case <-channel.started:
	case <-time.After(time.Second):
		t.Fatal("channel send did not start")
	}

	closed := make(chan struct{})
	go func() {
		manager.Close()
		close(closed)
	}()
	select {
	case <-channel.closed:
		t.Fatal("channel closed before in-flight send completed")
	case <-time.After(25 * time.Millisecond):
	}
	close(channel.release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("manager close did not finish")
	}
}

func TestManagerChannelLifecycleConcurrentAccess(t *testing.T) {
	manager := &Manager{channels: []Channel{&captureChannel{}}}
	cfg := &config.Config{}
	var wait sync.WaitGroup
	wait.Add(3)
	go func() {
		defer wait.Done()
		for i := 0; i < 100; i++ {
			if err := manager.UpdateConfig(cfg); err != nil {
				t.Errorf("UpdateConfig() error = %v", err)
				return
			}
		}
	}()
	go func() {
		defer wait.Done()
		for i := 0; i < 100; i++ {
			manager.NotifyRaw("concurrent")
		}
	}()
	go func() {
		defer wait.Done()
		for i := 0; i < 100; i++ {
			_ = manager.GetChannelNames()
		}
	}()
	wait.Wait()
	manager.Close()
}

func TestManagerUpdateConfigFailureKeepsCurrentChannels(t *testing.T) {
	capture := &captureChannel{}
	manager := &Manager{channels: []Channel{capture}}
	err := manager.UpdateConfig(&config.Config{Weixin: config.WeixinConfig{Enabled: true}})
	if !errors.Is(err, ErrRuntimeStateStoreUnavailable) {
		t.Fatalf("UpdateConfig() error = %v", err)
	}
	if names := manager.GetChannelNames(); len(names) != 1 || names[0] != "capture" {
		t.Fatalf("GetChannelNames() = %v", names)
	}
	manager.NotifyRaw("still active")
	waitUntil(t, time.Second, func() bool { return capture.Last() == "still active" })
	manager.Close()
}

func TestManagerRevokeChannelRetiresOnlyMatchingChannel(t *testing.T) {
	telegram := &namedLifecycleChannel{name: "telegram", closed: make(chan struct{})}
	other := &namedLifecycleChannel{name: "other", closed: make(chan struct{})}
	manager := &Manager{
		channels: []Channel{telegram, other},
		deviceFilters: map[Channel]map[string]bool{
			telegram: {"wwan0": true},
			other:    {"wwan1": true},
		},
	}

	if !manager.RevokeChannel("telegram") {
		t.Fatal("RevokeChannel() = false, want true")
	}
	select {
	case <-telegram.closed:
	default:
		t.Fatal("Telegram channel was not closed")
	}
	select {
	case <-other.closed:
		t.Fatal("unrelated channel was closed")
	default:
	}
	if names := manager.GetChannelNames(); len(names) != 1 || names[0] != "other" {
		t.Fatalf("GetChannelNames() = %v", names)
	}
	if _, exists := manager.deviceFilters[telegram]; exists {
		t.Fatal("revoked channel retained its device filter")
	}
	if _, exists := manager.deviceFilters[other]; !exists {
		t.Fatal("unrelated channel lost its device filter")
	}
	manager.Close()
}

func TestManagerRevokeChannelDoesNotWaitForOtherChannelSend(t *testing.T) {
	telegram := &namedLifecycleChannel{name: "telegram", closed: make(chan struct{})}
	other := &blockingChannel{
		started: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{}),
	}
	manager := &Manager{channels: []Channel{telegram, other}}
	manager.NotifyRaw("in flight")
	select {
	case <-other.started:
	case <-time.After(time.Second):
		t.Fatal("other channel send did not start")
	}

	revoked := make(chan bool, 1)
	go func() { revoked <- manager.RevokeChannel("telegram") }()
	select {
	case ok := <-revoked:
		if !ok {
			t.Fatal("RevokeChannel() = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("Telegram revocation waited for another channel send")
	}
	select {
	case <-telegram.closed:
	default:
		t.Fatal("Telegram channel was not closed")
	}
	select {
	case <-other.closed:
		t.Fatal("other channel was closed")
	default:
	}

	close(other.release)
	manager.Close()
}

func TestManagerRevokeChannelStopsCommandsBeforeOwnSendCompletes(t *testing.T) {
	telegram := &revocableBlockingChannel{
		started: make(chan struct{}), release: make(chan struct{}),
		stopped: make(chan struct{}), closed: make(chan struct{}),
	}
	manager := &Manager{channels: []Channel{telegram}}
	manager.NotifyRaw("in flight")
	select {
	case <-telegram.started:
	case <-time.After(time.Second):
		t.Fatal("Telegram send did not start")
	}

	revoked := make(chan bool, 1)
	go func() { revoked <- manager.RevokeChannel("telegram") }()
	select {
	case <-telegram.stopped:
	case <-time.After(time.Second):
		t.Fatal("Telegram commands were not stopped before waiting")
	}
	select {
	case <-telegram.closed:
		t.Fatal("Telegram channel closed before its send completed")
	default:
	}

	close(telegram.release)
	select {
	case ok := <-revoked:
		if !ok {
			t.Fatal("RevokeChannel() = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("Telegram revocation did not finish")
	}
	select {
	case <-telegram.closed:
	default:
		t.Fatal("Telegram channel was not closed")
	}
}

func TestManagerNotifyIPRotatedUsesPlainTemplate(t *testing.T) {
	capture := &captureChannel{}
	m := &Manager{channels: []Channel{capture}}

	m.NotifyIPRotated("wwan0", "1.1.1.1", "2.2.2.2", 2*time.Second)
	waitUntil(t, time.Second, func() bool { return capture.Last() != "" })
	want := "公网切换\n设备    wwan0\n旧 IP    1.1.1.1\n新 IP    2.2.2.2\n耗时    2s"
	if got := capture.Last(); got != want {
		t.Fatalf("ip rotated text=%q, want %q", got, want)
	}
}

func TestManagerNotifyIncomingCallUsesPlainTemplate(t *testing.T) {
	capture := &captureChannel{}
	m := &Manager{channels: []Channel{capture}}

	m.NotifyIncomingCall("wwan0", "10086", "10010")
	time.Sleep(20 * time.Millisecond)
	want := notificationLines("来电通知", append(append([]string{"设备", "wwan0"}, phoneIdentityFields("主叫", "10086")...), "被叫", "10010")...)
	if got := capture.Last(); got != want {
		t.Fatalf("incoming call text=%q, want %q", got, want)
	}
}

func TestManagerNotifyIncomingCallOmitsEmptyCallee(t *testing.T) {
	capture := &captureChannel{}
	m := &Manager{channels: []Channel{capture}}

	m.NotifyIncomingCall("wwan1", "18599996654", "")
	time.Sleep(20 * time.Millisecond)
	want := notificationLines("来电通知", append([]string{"设备", "wwan1"}, phoneIdentityFields("主叫", "18599996654")...)...)
	if got := capture.Last(); got != want {
		t.Fatalf("incoming call text=%q, want %q", got, want)
	}
}

func TestManagerNotifyIncomingCallDeduplicatesRapidRepeats(t *testing.T) {
	capture := &captureChannel{}
	m := &Manager{channels: []Channel{capture}}
	m.NotifyIncomingCall("wwan0", "+1555550100", "")
	m.NotifyIncomingCall("wwan0", "+1555550100", "")
	waitUntil(t, time.Second, func() bool { return capture.Last() != "" })
	time.Sleep(20 * time.Millisecond)
	capture.mu.Lock()
	count := len(capture.calls)
	capture.mu.Unlock()
	if count != 1 {
		t.Fatalf("incoming broadcasts = %d, want 1", count)
	}
}

func TestManagerNotifyCallResultIsSeparateFromIncomingCall(t *testing.T) {
	capture := &captureChannel{}
	m := &Manager{channels: []Channel{capture}}
	at := time.Date(2026, 8, 13, 13, 0, 0, 0, time.UTC)

	m.NotifyIncomingCall("wwan0", "10086", "10010")
	m.NotifyCallResult("wwan0", "10086", "incoming", "missed", "remote_cancel", at)

	waitUntil(t, time.Second, func() bool {
		capture.mu.Lock()
		defer capture.mu.Unlock()
		return len(capture.calls) == 2
	})
	capture.mu.Lock()
	defer capture.mu.Unlock()
	events := make(map[string]NotificationContext, len(capture.calls))
	for _, call := range capture.calls {
		events[call.Event] = call
	}
	if _, ok := events["incoming_call"]; !ok {
		t.Fatal("incoming_call event missing")
	}
	result, ok := events["call_missed"]
	if !ok {
		t.Fatal("call_missed event missing")
	}
	if result.Timestamp != at {
		t.Fatalf("result timestamp=%s, want %s", result.Timestamp, at)
	}
	want := notificationLines("未接来电", append([]string{"设备", "wwan0"}, phoneIdentityFields("主叫", "10086")...)...)
	if result.Text != want {
		t.Fatalf("result text=%q, want %q", result.Text, want)
	}
}

func TestFormatCallResultMessageUsesIncomingCallStyle(t *testing.T) {
	tests := []struct {
		name      string
		deviceID  string
		peer      string
		direction string
		status    string
		reason    string
		want      string
	}{
		{
			name:      "outbound local hangup",
			deviceID:  "wwan1",
			peer:      "888",
			direction: "outbound",
			status:    "completed",
			reason:    "local_hangup",
			want:      notificationLines("已挂断", append([]string{"设备", "wwan1"}, phoneIdentityFields("被叫", "888")...)...),
		},
		{
			name:      "inbound remote hangup",
			deviceID:  "wwan0",
			peer:      "10086",
			direction: "inbound",
			status:    "completed",
			reason:    "remote_bye",
			want:      notificationLines("对方已挂断", append([]string{"设备", "wwan0"}, phoneIdentityFields("主叫", "10086")...)...),
		},
		{
			name:      "incoming missed",
			deviceID:  "wwan0",
			peer:      "10086",
			direction: "incoming",
			status:    "missed",
			reason:    "remote_cancel",
			want:      notificationLines("未接来电", append([]string{"设备", "wwan0"}, phoneIdentityFields("主叫", "10086")...)...),
		},
		{
			name:      "inbound rejected",
			deviceID:  "wwan0",
			peer:      "10010",
			direction: "inbound",
			status:    "rejected",
			reason:    "local_reject",
			want:      notificationLines("已拒接", append([]string{"设备", "wwan0"}, phoneIdentityFields("主叫", "10010")...)...),
		},
		{
			name:      "inbound busy",
			deviceID:  "wwan0",
			peer:      "10086",
			direction: "inbound",
			status:    "busy",
			reason:    "device_busy",
			want:      notificationLines("忙线", append([]string{"设备", "wwan0"}, phoneIdentityFields("主叫", "10086")...)...),
		},
		{
			name:      "completed without specific reason",
			deviceID:  "wwan1",
			peer:      "888",
			direction: "outbound",
			status:    "completed",
			reason:    "normal",
			want:      notificationLines("通话结束", append([]string{"设备", "wwan1"}, phoneIdentityFields("被叫", "888")...)...),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatCallResultMessage(callResultMessage{
				Device: tt.deviceID, Peer: tt.peer, Direction: tt.direction,
				Status: tt.status, Reason: tt.reason,
			}, "")
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestManagerNotifySMSLogsBroadcastSummary(t *testing.T) {
	logger.Setup(logger.LogConfig{Debug: true, Filename: filepath.Join(t.TempDir(), "app.log")})
	capture := &captureChannel{}
	m := &Manager{channels: []Channel{capture}}
	ch := logger.GlobalBroadcaster.Subscribe()
	defer logger.GlobalBroadcaster.Unsubscribe(ch)

	ts := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	m.NotifySMS("wwan0", "+8613800000000", "hello", ts)

	entry := waitLogEntry(t, ch, func(entry logger.LogEntry) bool {
		return entry.Message == "开始发送短信通知"
	})
	fields := readLogFields(t, entry)
	if fields["event"] != "sms_received" {
		t.Fatalf("event=%v want sms_received", fields["event"])
	}
	if fields["channel_count"] != float64(1) {
		t.Fatalf("channel_count=%v want 1", fields["channel_count"])
	}
}

func TestManagerNotifySMSWithSourceUsesProvidedSourceLabel(t *testing.T) {
	capture := &captureChannel{}
	m := &Manager{channels: []Channel{capture}, notificationLocation: time.UTC}
	notifier, ok := any(m).(interface {
		NotifySMSWithSource(deviceID, sender, content, source string, timestamp time.Time)
	})
	if !ok {
		t.Fatal("NotifySMSWithSource missing")
	}

	ts := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	notifier.NotifySMSWithSource("wwan0", "+8613800000000", "hello", "VoWiFi", ts)

	waitUntil(t, time.Second, func() bool { return capture.Last() != "" })
	want := wantSMSText("wwan0", "VoWiFi", "+8613800000000", "2026-04-13 12:00:00", "hello")
	if got := capture.Last(); got != want {
		t.Fatalf("text=%q, want %q", got, want)
	}
	if got := capture.LastContext().Event; got != "sms_received" {
		t.Fatalf("event=%q, want sms_received", got)
	}
}

func TestManagerNotifySMSConvertsNetworkTimeToNotificationLocation(t *testing.T) {
	capture := &captureChannel{}
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	londonSummer := time.FixedZone("Europe/London", 60*60)
	m := &Manager{channels: []Channel{capture}, notificationLocation: shanghai}
	timestamp := time.Date(2026, 8, 19, 9, 25, 36, 0, londonSummer)

	m.NotifySMSWithSource("wwan1", "888", "verification code", "VoWiFi", timestamp)

	waitUntil(t, time.Second, func() bool { return capture.Last() != "" })
	if !strings.Contains(capture.Last(), "时间    2026-08-19 16:25:36") {
		t.Fatalf("notification time was not converted: %q", capture.Last())
	}
	if got := capture.LastContext().Timestamp; !got.Equal(timestamp) || got.Location() != londonSummer {
		t.Fatalf("structured timestamp changed: %v", got)
	}
}

func webhookConfigForTest(url, template string) config.WebhookConfig {
	return config.WebhookConfig{
		Enabled:      true,
		URLs:         []string{url},
		TimeoutMs:    5000,
		RetryMax:     0,
		TextTemplate: template,
	}
}

func wantSMSText(deviceID, source, sender, at, content string) string {
	fields := []string{"设备", deviceID, "通道", source}
	fields = append(fields, phoneIdentityFields("号码", sender)...)
	fields = append(fields, "时间", at, "内容", content)
	return notificationLines("收到新短信", fields...)
}

func TestNotifySMSAndCallIncludeAttribution(t *testing.T) {
	capture := &captureChannel{}
	m := &Manager{channels: []Channel{capture}, notificationLocation: time.UTC}
	ts := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)

	m.NotifySMS("wwan0", "10010", "hello", ts)
	waitUntil(t, time.Second, func() bool { return capture.Last() != "" })
	if !strings.Contains(capture.Last(), "归属    中国联通 · 客服") {
		t.Fatalf("sms missing attribution: %q", capture.Last())
	}
	if got := capture.LastContext().Attribution; got != "中国联通 · 客服" {
		t.Fatalf("sms context attribution=%q", got)
	}

	m.NotifyIncomingCall("wwan0", "10086", "10010")
	waitUntil(t, time.Second, func() bool {
		return strings.Contains(capture.Last(), "来电通知")
	})
	if !strings.Contains(capture.Last(), "归属    中国移动 · 客服") {
		t.Fatalf("call missing attribution: %q", capture.Last())
	}
}
