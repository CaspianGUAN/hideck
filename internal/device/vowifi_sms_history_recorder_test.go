package device

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost/eventhost"
	"github.com/yibaiba/hideck/internal/db"
)

func TestApplyVoWiFiSMSMemoryPressure(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		retained vowifiSMSRecordResult
		want     *bool
	}{
		{name: "persist error", err: errors.New("db down"), want: boolPtr(true)},
		{name: "stored", retained: vowifiSMSRecordResult{Stored: true}, want: boolPtr(false)},
		{name: "duplicate", retained: vowifiSMSRecordResult{Duplicate: true}, want: boolPtr(false)},
		{name: "suppressed", retained: vowifiSMSRecordResult{Suppressed: true}, want: boolPtr(false)},
		{name: "empty identity"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var (
				set   *bool
				calls int
			)
			applyVoWiFiSMSMemoryPressure(func(full bool) {
				calls++
				value := full
				set = &value
			}, test.err, test.retained)
			if test.want == nil {
				if calls != 0 {
					t.Fatalf("unexpected set calls=%d full=%v", calls, set)
				}
				return
			}
			if calls != 1 || set == nil || *set != *test.want {
				t.Fatalf("full=%v calls=%d, want %v", set, calls, *test.want)
			}
		})
	}
}

func boolPtr(v bool) *bool { return &v }

func TestVoWiFiSMSHistoryRecorderPersistsSentSMS(t *testing.T) {
	initDevicePhoneNumberTestDB(t)
	if err := db.UpdateSIMCardVoWiFiPhoneNumberByIMSI("imsi-vowifi-1", "+8613800000000"); err != nil {
		t.Fatalf("UpdateSIMCardVoWiFiPhoneNumberByIMSI() error=%v", err)
	}
	p := NewPool(nil)
	p.workers["dev-1"] = &Worker{ID: "dev-1", Backend: &workerPhoneBackendStub{imsi: "imsi-vowifi-1"}}

	at := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	err := vowifiSMSHistoryRecorder{pool: p}.RecordSent(eventhost.SMSSent{
		DevID:      "dev-1",
		TargetURI:  "+10010",
		Content:    "hello",
		Time:       at,
		TotalParts: 1,
	})
	if err != nil {
		t.Fatalf("RecordSent() error=%v", err)
	}

	var sms db.SMS
	if err := db.DB.Where("imsi = ? AND type = ?", "imsi-vowifi-1", 2).First(&sms).Error; err != nil {
		t.Fatalf("First(sent sms) error=%v", err)
	}
	if sms.Sender != "+8613800000000" || sms.Recipient != "+10010" || sms.Content != "hello" || sms.Status != 2 {
		t.Fatalf("sent sms=%+v", sms)
	}
}

func TestVoWiFiSMSHistoryRecorderUsesCapturedSentSIM(t *testing.T) {
	initDevicePhoneNumberTestDB(t)
	const (
		imsi     = "234150000000077"
		oldICCID = "8944100000000000077"
		newICCID = "8944100000000000078"
	)
	for _, iccid := range []string{oldICCID, newICCID} {
		if err := db.UpsertSIMCardIdentity(iccid, imsi, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	p := NewPool(nil)
	w := &Worker{ID: "dev-sent"}
	setWorkerSMSIdentity(w, SMSIdentity{ICCID: newICCID, IMSI: imsi})
	p.workers[w.ID] = w

	err := vowifiSMSHistoryRecorder{pool: p}.RecordSent(eventhost.SMSSent{
		DevID: w.ID, ICCID: oldICCID, IMSI: imsi,
		TargetURI: "+10010", Content: "sent before switch", Time: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var sms db.SMS
	if err := db.DB.Where("content = ?", "sent before switch").First(&sms).Error; err != nil {
		t.Fatal(err)
	}
	if sms.ICCID != oldICCID {
		t.Fatalf("sent SMS ICCID=%q want captured %q", sms.ICCID, oldICCID)
	}
}

func TestVoWiFiSMSHistoryRecorderPersistsReceivedSMS(t *testing.T) {
	initDevicePhoneNumberTestDB(t)
	if err := db.UpdateSIMCardVoWiFiPhoneNumberByIMSI("imsi-vowifi-2", "+8613900000000"); err != nil {
		t.Fatalf("UpdateSIMCardVoWiFiPhoneNumberByIMSI() error=%v", err)
	}
	p := NewPool(nil)
	p.workers["dev-2"] = &Worker{ID: "dev-2", Backend: &workerPhoneBackendStub{imsi: "imsi-vowifi-2"}}

	at := time.Date(2026, 6, 3, 12, 1, 0, 0, time.UTC)
	_, err := vowifiSMSHistoryRecorder{pool: p}.RecordReceived(eventhost.SMSReceived{
		DevID:   "dev-2",
		Sender:  "+10086",
		Content: "inbound",
		Time:    at,
	})
	if err != nil {
		t.Fatalf("RecordReceived() error=%v", err)
	}

	var sms db.SMS
	if err := db.DB.Where("imsi = ? AND type = ?", "imsi-vowifi-2", 1).First(&sms).Error; err != nil {
		t.Fatalf("First(received sms) error=%v", err)
	}
	if sms.Sender != "+10086" || sms.Recipient != "+8613900000000" || sms.Content != "inbound" || sms.Status != 0 {
		t.Fatalf("received sms=%+v", sms)
	}
}

func TestVoWiFiSMSHistoryRecorderKeepsCapturedSIMAfterProfileSwitch(t *testing.T) {
	for _, multipart := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "multipart"}[multipart], func(t *testing.T) {
			initDevicePhoneNumberTestDB(t)
			const (
				imsi     = "234150000000046"
				oldICCID = "8944100000000000046"
				newICCID = "8944100000000000047"
			)
			if err := db.UpsertSIMCardIdentity(oldICCID, imsi, "", nil); err != nil {
				t.Fatal(err)
			}
			if err := db.UpsertSIMCardIdentity(newICCID, imsi, "", nil); err != nil {
				t.Fatal(err)
			}
			event := eventhost.SMSReceived{
				DevID: "dev-switch", Sender: "+10086", Content: "captured identity",
				Time: time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC),
			}
			if multipart {
				event.FragmentSessionKey = "ref=46"
			}
			result, err := (vowifiSMSHistoryRecorder{}).recordReceived(event, SMSIdentity{
				ICCID: oldICCID, IMSI: imsi,
			})
			if err != nil || !result.Stored {
				t.Fatalf("recordReceived() result=%+v err=%v", result, err)
			}
			var stored db.SMS
			if err := db.DB.Where("content = ?", event.Content).Take(&stored).Error; err != nil {
				t.Fatal(err)
			}
			if stored.ICCID != oldICCID {
				t.Fatalf("stored ICCID=%q, want captured %q", stored.ICCID, oldICCID)
			}
		})
	}
}

func TestResolveReceivingIdentityDoesNotMixPartialCacheWithBackend(t *testing.T) {
	p := NewPool(nil)
	w := &Worker{
		ID: "dev-switching",
		Backend: &workerPhoneBackendStub{
			imsi: "234150000000099",
		},
	}
	w.state.Identity.Ready = true
	w.state.Identity.ICCID = "8944100000000000098"
	p.workers[w.ID] = w

	got := (vowifiSMSHistoryRecorder{pool: p}).resolveReceivingIdentity(w.ID)
	if got.ICCID != "8944100000000000098" || got.IMSI != "" {
		t.Fatalf("identity=%+v, want one coherent partial cache snapshot", got)
	}
}

func TestVoWiFiSMSHistoryRecorderUsesCapturedSIMPhoneNumber(t *testing.T) {
	initDevicePhoneNumberTestDB(t)
	const (
		imsi     = "234150000000097"
		oldICCID = "8944100000000000097"
		newICCID = "8944200000000000096"
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
	if err := db.UpdateSIMCardVoWiFiPhoneNumberByIMSI(imsi, "+447700900096"); err != nil {
		t.Fatal(err)
	}
	var subscription db.SIMSubscription
	if err := db.DB.First(&subscription, "imsi = ?", imsi).Error; err != nil {
		t.Fatal(err)
	}
	if subscription.CurrentICCID != newICCID {
		t.Fatalf("subscription ICCID=%q, want %q", subscription.CurrentICCID, newICCID)
	}
	phone, err := db.GetPhoneNumberForSMSIdentity(db.SMSIdentity{ICCID: oldICCID, IMSI: imsi})
	if err != nil || phone != "" {
		t.Fatalf("captured identity phone=%q err=%v, want no cross-profile fallback", phone, err)
	}

	result, err := (vowifiSMSHistoryRecorder{}).recordReceived(eventhost.SMSReceived{
		Sender: "+10086", Content: "old profile", Time: time.Now(),
	}, SMSIdentity{ICCID: oldICCID, IMSI: imsi})
	if err != nil || !result.Stored {
		t.Fatalf("recordReceived() result=%+v err=%v", result, err)
	}
	var stored db.SMS
	if err := db.DB.Where("content = ?", "old profile").Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.LocalPhone != "" {
		t.Fatalf("local phone=%q, must not use the different profile number", stored.LocalPhone)
	}
}

func TestVoWiFiSMSHistoryRecorderReconcilesDegradedMultipartSMS(t *testing.T) {
	initDevicePhoneNumberTestDB(t)
	const imsi = "imsi-vowifi-fragment"
	if err := db.UpdateSIMCardVoWiFiPhoneNumberByIMSI(imsi, "+447840844894"); err != nil {
		t.Fatalf("UpdateSIMCardVoWiFiPhoneNumberByIMSI() error=%v", err)
	}
	p := NewPool(nil)
	p.workers["dev-fragment"] = &Worker{
		ID: "dev-fragment", Backend: &workerPhoneBackendStub{imsi: imsi},
	}
	recorder := vowifiSMSHistoryRecorder{pool: p}
	event := eventhost.SMSReceived{
		DevID: "dev-fragment", Sender: "giffgaff", Content: "[incomplete] first",
		Time:               time.Date(2026, 8, 10, 14, 30, 5, 0, time.UTC),
		FragmentSessionKey: "sender=giffgaff|ref=212", Incomplete: true,
	}
	degraded, err := recorder.RecordReceived(event)
	if err != nil || !degraded.Stored || degraded.Reconciled {
		t.Fatalf("degraded result=%+v err=%v", degraded, err)
	}
	event.Content = "complete message"
	event.Time = event.Time.Add(3 * time.Minute)
	event.Incomplete = false
	complete, err := recorder.RecordReceived(event)
	if err != nil || !complete.Stored || !complete.Reconciled || complete.Duplicate {
		t.Fatalf("complete result=%+v err=%v", complete, err)
	}

	var messages []db.SMS
	if err := db.DB.Where("imsi = ? AND type = ?", imsi, 1).Find(&messages).Error; err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Content != "complete message" || messages[0].Incomplete {
		t.Fatalf("messages=%+v", messages)
	}
	var contact db.SMSContact
	if err := db.DB.Where("imsi = ? AND peer = ?", imsi, "giffgaff").First(&contact).Error; err != nil {
		t.Fatal(err)
	}
	if contact.LastContent != "complete message" || contact.UnreadCount != 1 {
		t.Fatalf("contact=%+v", contact)
	}
}

func TestVoWiFiSMSHistoryRecorderSkipsSuppressedReceivedSMS(t *testing.T) {
	initDevicePhoneNumberTestDB(t)
	p := NewPool(nil)
	p.workers["dev-ota"] = &Worker{ID: "dev-ota", Backend: &workerPhoneBackendStub{imsi: "imsi-ota"}}

	_, err := vowifiSMSHistoryRecorder{pool: p}.RecordReceived(eventhost.SMSReceived{
		DevID:   "dev-ota",
		Sender:  "+10086",
		Content: "[SIM OTA 23.048]\ndecrypt=not_attempted\nsecurity=可能加密\nraw=0011",
		Time:    time.Now(),
	})
	if err != nil {
		t.Fatalf("RecordReceived() error=%v", err)
	}

	var count int64
	if err := db.DB.Model(&db.SMS{}).Where("imsi = ? AND type = ?", "imsi-ota", 1).Count(&count).Error; err != nil {
		t.Fatalf("Count(received sms) error=%v", err)
	}
	if count != 0 {
		t.Fatalf("suppressed received sms count=%d want 0", count)
	}
}

func TestWorkerProcessSMSSkipsSuppressedReceivedSMS(t *testing.T) {
	initDevicePhoneNumberTestDB(t)
	p := NewPool(nil)
	w := &Worker{
		ID:      "dev-worker-ota",
		Pool:    p,
		Backend: &workerPhoneBackendStub{imsi: "imsi-worker-ota"},
	}

	w.processSMS("+10086", "[SIM OTA 23.048]\ndecrypt=not_attempted\nsecurity=可能加密\nraw=0011", time.Now())

	var count int64
	if err := db.DB.Model(&db.SMS{}).Where("imsi = ? AND type = ?", "imsi-worker-ota", 1).Count(&count).Error; err != nil {
		t.Fatalf("Count(received sms) error=%v", err)
	}
	if count != 0 {
		t.Fatalf("suppressed worker sms count=%d want 0", count)
	}
}

func TestVoWiFiSMSHistoryRecorderPersistsSendFailure(t *testing.T) {
	initDevicePhoneNumberTestDB(t)
	p := NewPool(nil)
	p.workers["dev-fail"] = &Worker{ID: "dev-fail", Backend: &workerPhoneBackendStub{imsi: "imsi-fail"}}

	err := vowifiSMSHistoryRecorder{pool: p}.RecordSendFailure(VoWiFiSMSSendFailure{
		DeviceID: "dev-fail", Target: "+10010", Content: "failed sms", At: time.Now(),
	})
	if err != nil {
		t.Fatalf("RecordSendFailure() error=%v", err)
	}

	var sms db.SMS
	if err := db.DB.Where("imsi = ? AND type = ? AND status = ?", "imsi-fail", 2, 3).First(&sms).Error; err != nil {
		t.Fatalf("First(failed sms) error=%v", err)
	}
	if sms.Recipient != "+10010" || sms.Content != "failed sms" {
		t.Fatalf("failed sms=%+v", sms)
	}
}

func TestVoWiFiSMSHistoryRecorderKeepsCapturedIdentityForSendFailure(t *testing.T) {
	initDevicePhoneNumberTestDB(t)
	p := NewPool(nil)
	w := &Worker{ID: "dev-fail-swap", Backend: &workerPhoneBackendStub{imsi: "imsi-new"}}
	p.workers[w.ID] = w
	setWorkerSMSIdentity(w, SMSIdentity{ICCID: "8944000000000000999", IMSI: "imsi-new"})

	err := vowifiSMSHistoryRecorder{pool: p}.RecordSendFailure(VoWiFiSMSSendFailure{
		DeviceID: w.ID, Target: "+10010", Content: "failed before swap",
		Identity: SMSIdentity{ICCID: "8944000000000000111", IMSI: "imsi-old"}, At: time.Now(),
	})
	if err != nil {
		t.Fatalf("RecordSendFailure() error=%v", err)
	}

	var sms db.SMS
	if err := db.DB.Where("iccid = ? AND imsi = ?", "8944000000000000111", "imsi-old").First(&sms).Error; err != nil {
		t.Fatalf("First(failed sms) error=%v", err)
	}
	if sms.Recipient != "+10010" || sms.Content != "failed before swap" {
		t.Fatalf("failed sms=%+v", sms)
	}
}

func TestVoWiFiSMSHistoryRecorderPersistsLocalNumberLearned(t *testing.T) {
	initDevicePhoneNumberTestDB(t)
	p := NewPool(nil)
	p.workers["dev-3"] = &Worker{ID: "dev-3", Backend: &workerPhoneBackendStub{imsi: "imsi-vowifi-3"}}

	err := vowifiSMSHistoryRecorder{pool: p}.RecordLocalNumberLearned(eventhost.LocalNumberLearned{
		DevID:  "dev-3",
		IMSI:   "imsi-vowifi-3",
		Number: "+8613700000000",
		Source: "register",
	})
	if err != nil {
		t.Fatalf("RecordLocalNumberLearned() error=%v", err)
	}

	sub := loadDeviceTestSIMSubscriptionByIMSI(t, "imsi-vowifi-3")
	if sub.VowifiPhoneNumber != "+8613700000000" || sub.PhoneNumber != "+8613700000000" {
		t.Fatalf("subscription=%+v", sub)
	}
}

func TestRecordLocalNumberLearnedStagesByICCIDWhenIMSIEmpty(t *testing.T) {
	initDevicePhoneNumberTestDB(t)

	p := NewPool(nil)
	defer p.cancel()
	w := &Worker{ID: "dev-1"}
	w.state.Identity.Ready = true
	w.state.Identity.ICCID = "8944000000000000111"
	p.workers["dev-1"] = w

	rec := vowifiSMSHistoryRecorder{pool: p}
	err := rec.RecordLocalNumberLearned(eventhost.LocalNumberLearned{
		DevID: "dev-1", IMSI: "", Number: "+447700900200", Source: "P-Associated-URI",
	})
	if err != nil {
		t.Fatalf("RecordLocalNumberLearned error=%v", err)
	}
	got, err := db.GetPhoneNumberByIMSIOrICCID("", "8944000000000000111")
	if err != nil {
		t.Fatalf("GetPhoneNumberByIMSIOrICCID error=%v", err)
	}
	if got != "+447700900200" {
		t.Fatalf("phone=%q, want +447700900200 staged by ICCID", got)
	}
}

func TestVoWiFiRuntimeDispatcherPersistsSMSSentWithoutNotifier(t *testing.T) {
	initDevicePhoneNumberTestDB(t)
	p := NewPool(nil)
	p.workers["dev-dispatch"] = &Worker{ID: "dev-dispatch", Backend: &workerPhoneBackendStub{imsi: "imsi-dispatch"}}

	poolVoWiFiRuntimeDispatcher{pool: p}.Dispatch(context.Background(), eventhost.SMSSent{
		DevID:     "dev-dispatch",
		TargetURI: "+10010",
		Content:   "sent through dispatcher",
		Time:      time.Now(),
	})

	var count int64
	if err := db.DB.Model(&db.SMS{}).Where("imsi = ? AND type = ? AND status = ?", "imsi-dispatch", 2, 2).Count(&count).Error; err != nil {
		t.Fatalf("Count(sent sms) error=%v", err)
	}
	if count != 1 {
		t.Fatalf("sent sms count=%d want 1", count)
	}
}

func TestVoWiFiSMSHistoryRecorderSkipsDuplicateReceivedSMS(t *testing.T) {
	initDevicePhoneNumberTestDB(t)
	if err := db.UpdateSIMCardVoWiFiPhoneNumberByIMSI("imsi-vowifi-dup", "+8613900000000"); err != nil {
		t.Fatalf("UpdateSIMCardVoWiFiPhoneNumberByIMSI() error=%v", err)
	}
	p := NewPool(nil)
	p.workers["dev-dup"] = &Worker{ID: "dev-dup", Backend: &workerPhoneBackendStub{imsi: "imsi-vowifi-dup"}}

	rec := vowifiSMSHistoryRecorder{pool: p}
	at := time.Date(2026, 6, 29, 23, 58, 55, 0, time.UTC)
	first, err := rec.RecordReceived(eventhost.SMSReceived{
		DevID:   "dev-dup",
		Sender:  "+447751284582",
		Content: "ij991818短信登录验证码，5分钟内有效，请勿泄露。",
		Time:    at,
	})
	if err != nil {
		t.Fatalf("first RecordReceived() error=%v", err)
	}
	if !first.Stored || first.Duplicate || first.Suppressed {
		t.Fatalf("first result=%+v, want stored only", first)
	}

	second, err := rec.RecordReceived(eventhost.SMSReceived{
		DevID:   "dev-dup",
		Sender:  "+447751284582",
		Content: "ij991818短信登录验证码，5分钟内有效，请勿泄露。",
		Time:    at.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("second RecordReceived() error=%v", err)
	}
	if second.Stored || !second.Duplicate || second.Suppressed {
		t.Fatalf("second result=%+v, want duplicate only", second)
	}

	var count int64
	if err := db.DB.Model(&db.SMS{}).Where("imsi = ? AND type = ?", "imsi-vowifi-dup", 1).Count(&count).Error; err != nil {
		t.Fatalf("Count(received sms) error=%v", err)
	}
	if count != 1 {
		t.Fatalf("received sms count=%d want 1", count)
	}
}

type countingVoWiFiNotifier struct {
	smsCount int
	rawCount int
}

func (n *countingVoWiFiNotifier) NotifySMS(deviceID, sender, content string, timestamp time.Time) {
	n.smsCount++
}

func (n *countingVoWiFiNotifier) NotifySMSWithSource(deviceID, sender, content, source string, timestamp time.Time) {
	n.smsCount++
}

func (n *countingVoWiFiNotifier) NotifyRaw(msg string) {
	n.rawCount++
}

func (n *countingVoWiFiNotifier) NotifyIPRotated(deviceID, oldIP, newIP string, duration time.Duration) {
}

func TestVoWiFiRuntimeDispatcherSkipsDuplicateReceivedNotification(t *testing.T) {
	initDevicePhoneNumberTestDB(t)
	if err := db.UpdateSIMCardVoWiFiPhoneNumberByIMSI("imsi-vowifi-dispatch-dup", "+8613900000000"); err != nil {
		t.Fatalf("UpdateSIMCardVoWiFiPhoneNumberByIMSI() error=%v", err)
	}
	p := NewPool(nil)
	p.workers["dev-dispatch-dup"] = &Worker{ID: "dev-dispatch-dup", Backend: &workerPhoneBackendStub{imsi: "imsi-vowifi-dispatch-dup"}}
	notifier := &countingVoWiFiNotifier{}
	p.SetNotifier(notifier)

	ev := eventhost.SMSReceived{
		DevID:   "dev-dispatch-dup",
		Sender:  "+447751284582",
		Content: "ij991818短信登录验证码，5分钟内有效，请勿泄露。",
		Time:    time.Date(2026, 6, 29, 23, 58, 55, 0, time.UTC),
	}
	poolVoWiFiRuntimeDispatcher{pool: p}.Dispatch(context.Background(), ev)
	ev.Time = ev.Time.Add(73 * time.Second)
	poolVoWiFiRuntimeDispatcher{pool: p}.Dispatch(context.Background(), ev)

	if notifier.smsCount != 1 {
		t.Fatalf("sms notification count=%d want 1", notifier.smsCount)
	}
	var count int64
	if err := db.DB.Model(&db.SMS{}).Where("imsi = ? AND type = ?", "imsi-vowifi-dispatch-dup", 1).Count(&count).Error; err != nil {
		t.Fatalf("Count(received sms) error=%v", err)
	}
	if count != 1 {
		t.Fatalf("received sms count=%d want 1", count)
	}
}
