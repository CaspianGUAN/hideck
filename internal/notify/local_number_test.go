package notify

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yibaiba/hideck/internal/db"
	"github.com/yibaiba/hideck/internal/device"
)

func TestNotifySMSReceiptNamesTheReceivingSIM(t *testing.T) {
	if err := db.Init(filepath.Join(t.TempDir(), "notify_local_number.db")); err != nil {
		t.Fatalf("db.Init() error=%v", err)
	}
	t.Cleanup(func() { db.DB = nil })
	if err := db.RecordVoWiFiPhoneNumber("234870000000001", "8944000000000001234", "+447700900123"); err != nil {
		t.Fatalf("record phone: %v", err)
	}
	ts := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		sms  device.InboundSMS
		want string
	}{
		{"stored number", device.InboundSMS{IMSI: "234870000000001", ICCID: "8944000000000001234"}, "+447700900123"},
		{"ICCID tail only", device.InboundSMS{ICCID: "8964050000000005678"}, "ICCID 尾号 5678"},
		{"no identity", device.InboundSMS{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture := &captureChannel{}
			m := &Manager{channels: []Channel{capture}, notificationLocation: time.UTC}
			sms := tt.sms
			sms.DeviceID, sms.Sender, sms.Content, sms.Time = "wwan0", "MontSMS", "code 1234", ts
			m.NotifySMSReceipt(sms, "VoWiFi")
			waitUntil(t, time.Second, func() bool { return capture.Last() != "" })
			text, ctx := capture.Last(), capture.LastContext()
			if !strings.Contains(ctx.LocalNumber, tt.want) || (tt.want == "") != (ctx.LocalNumber == "") {
				t.Fatalf("LocalNumber=%q, want %q", ctx.LocalNumber, tt.want)
			}
			if tt.want == "" {
				if strings.Contains(text, "本机") {
					t.Fatalf("text names a local SIM without identity: %q", text)
				}
				return
			}
			if !strings.Contains(text, "本机    ") || !strings.Contains(text, tt.want) {
				t.Fatalf("text missing local SIM %q: %q", tt.want, text)
			}
			if strings.Contains(text, sms.ICCID) && sms.ICCID != "" {
				t.Fatalf("text leaked the full ICCID: %q", text)
			}
		})
	}
}

func TestWebhookTemplateRendersLocalNumber(t *testing.T) {
	w, err := NewWebhookChannel(webhookConfigForTest("http://127.0.0.1:1", "{{local_number}}: {{text}}"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := w.renderText(NotificationContext{Text: "hi", LocalNumber: "+447700900123"})
	if err != nil || got != "+447700900123: hi" {
		t.Fatalf("renderText=%q err=%v", got, err)
	}
}

func TestLocalSIMLabelPrefersCapturedICCIDOverReusedIMSI(t *testing.T) {
	if err := db.Init(filepath.Join(t.TempDir(), "notify_reused_imsi.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.DB = nil })
	const (
		imsi     = "234150000000044"
		oldICCID = "8944100000000000044"
		newICCID = "8944100000000000045"
	)
	if err := db.RecordVoWiFiPhoneNumber("", oldICCID, "+447700900044"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertSIMCard(newICCID, imsi, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateSIMCardVoWiFiPhoneNumberByIMSI(imsi, "+447700900045"); err != nil {
		t.Fatal(err)
	}
	if got := localSIMLabel(imsi, oldICCID, "GB"); got != "+447700900044" {
		t.Fatalf("localSIMLabel()=%q, want captured SIM number", got)
	}
}

func TestLocalSIMLabelRejectsPhoneBoundToDifferentICCID(t *testing.T) {
	if err := db.Init(filepath.Join(t.TempDir(), "notify_profile_conflict.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.DB = nil })
	const (
		imsi     = "234150000000047"
		oldICCID = "8944100000000000046"
		newICCID = "8944200000000000047"
	)
	if err := db.DB.Create(&db.SIMCard{ICCID: oldICCID, IMSI: imsi}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&db.SIMCard{ICCID: newICCID, IMSI: imsi}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&db.SIMSubscription{IMSI: imsi, CurrentICCID: newICCID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateSIMCardVoWiFiPhoneNumberByIMSI(imsi, "+447700900047"); err != nil {
		t.Fatal(err)
	}
	if got := localSIMLabel(imsi, oldICCID, "GB"); got != "ICCID 尾号 0046" {
		t.Fatalf("localSIMLabel()=%q, want captured ICCID fallback", got)
	}
}
