package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/device"
)

func TestSMSAndCallNotificationsShowDeviceName(t *testing.T) {
	pool := device.NewPool(&config.Config{})
	pool.AttachWorkerForTest(&device.Worker{ID: "wws27u3i4", Config: config.DeviceConfig{ID: "wws27u3i4", Name: "Skinny-2"}})
	capture := &captureChannel{}
	m := &Manager{channels: []Channel{capture}, notificationLocation: time.UTC, pool: pool}
	ts := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

	m.NotifySMSReceipt(device.InboundSMS{DeviceID: "wws27u3i4", Sender: "MontSMS", Content: "code", Time: ts}, "VoWiFi")
	waitUntil(t, time.Second, func() bool { return capture.Last() != "" })
	if !strings.Contains(capture.Last(), "设备    Skinny-2 (wws27u3i4)") {
		t.Fatalf("sms text = %q", capture.Last())
	}

	m.NotifyIncomingCall("wws27u3i4", "10086", "")
	waitUntil(t, time.Second, func() bool { return strings.Contains(capture.Last(), "来电通知") })
	if !strings.Contains(capture.Last(), "设备    Skinny-2 (wws27u3i4)") {
		t.Fatalf("incoming call text = %q", capture.Last())
	}

	m.NotifyCallResult("wws27u3i4", "10086", "incoming", "missed", "", ts)
	waitUntil(t, time.Second, func() bool { return strings.Contains(capture.Last(), "未接来电") })
	if !strings.Contains(capture.Last(), "设备    Skinny-2 (wws27u3i4)") {
		t.Fatalf("call text = %q", capture.Last())
	}

	// Without a name the line keeps the bare ID.
	m.NotifySMSReceipt(device.InboundSMS{DeviceID: "wwan9", Sender: "x", Content: "y", Time: ts}, "VoWiFi")
	waitUntil(t, time.Second, func() bool { return strings.Contains(capture.Last(), "wwan9") })
	if !strings.Contains(capture.Last(), "设备    wwan9\n") {
		t.Fatalf("unnamed device text = %q", capture.Last())
	}
}
