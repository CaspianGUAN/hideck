package notify

import (
	"testing"
	"time"

	"github.com/yibaiba/hideck/internal/config"
)

func (c *captureChannel) messages() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.msgs...)
}

func TestBroadcastAppliesDeviceFilterToSMSAndCallsOnly(t *testing.T) {
	filtered, open := &captureChannel{}, &captureChannel{}
	m := &Manager{notificationLocation: time.UTC}
	m.installChannels([]Channel{filtered, open}, map[Channel]map[string]bool{
		filtered: deviceSet([]string{" wws27u1i4 ", ""}),
	})

	for _, ctx := range []NotificationContext{
		{Event: "sms_received", DeviceID: "wws27u2i4", Text: "sms from u2"},
		{Event: "incoming_call", DeviceID: "wws27u2i4", Text: "call to u2"},
		{Event: "call_missed", DeviceID: "wws27u2i4", Text: "missed on u2"},
		{Event: "sms_received", DeviceID: "wws27u1i4", Text: "sms from u1"},
		{Event: "ip_rotated", DeviceID: "wws27u2i4", Text: "ip on u2"},
	} {
		m.broadcastWithContext(ctx)
	}
	waitUntil(t, time.Second, func() bool { return len(open.messages()) == 5 && len(filtered.messages()) == 2 })
	for _, text := range filtered.messages() {
		if text != "sms from u1" && text != "ip on u2" {
			t.Fatalf("filtered channel received %q", text)
		}
	}
}

func TestIsChannelNameCoversEveryFactory(t *testing.T) {
	factories := (&Manager{}).channelFactories(&config.Config{})
	for _, factory := range factories {
		if !IsChannelName(factory.name) {
			t.Fatalf("factory %q missing from channelNames", factory.name)
		}
	}
	if len(factories) != len(channelNames) {
		t.Fatal("channelNames lists a channel without a factory")
	}
}
