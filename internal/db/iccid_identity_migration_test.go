package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/yibaiba/hideck/internal/mbn"
	"github.com/yibaiba/hideck/internal/outbound"
)

const issue46ICCID = "898600123456F7890123"
const issue46Legacy = "8986001234567890123"

func TestSIMICCIDMigrationRepairsDeviceSMSAndPolicies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "issue46.db")
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	const imsi, imei, peer = "460001234567890", "modem-46", "10086"
	now := time.Now().UTC().Truncate(time.Second)
	legacy := issue46Legacy
	create := func(row any) {
		t.Helper()
		if err := DB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	create(&Device{IMEI: imei, Alias: "wwan0", CurrentICCID: &legacy})
	create(&SIMCard{
		ICCID: legacy, IMSI: imsi, CurrentIMEI: ptrString(imei), Operator: "中国移动",
		RegStatusText: "registered", APN: "cmnet", LastSeen: now,
	})
	create(&SIMCard{ICCID: issue46ICCID, IMSI: imsi, LastSeen: now.Add(time.Second)})
	create(&SIMSubscription{IMSI: imsi, CurrentICCID: legacy, PhoneNumber: "+8613800000000"})
	first := SMS{ICCID: legacy, IMSI: imsi, Peer: peer, Type: 1, Status: 1, Content: "read", Timestamp: now}
	latest := SMS{ICCID: issue46ICCID, IMSI: imsi, Peer: peer, Type: 1, Status: 0, Content: "unread", Timestamp: now.Add(time.Second)}
	create(&first)
	create(&latest)
	create(&SMSContact{ICCID: legacy, IMSI: imsi, Peer: peer, LastSMSID: first.ID, LastTimestamp: first.Timestamp})
	create(&SMSContact{ICCID: issue46ICCID, IMSI: imsi, Peer: peer, LastSMSID: latest.ID, LastTimestamp: latest.Timestamp, UnreadCount: 1})
	create(&SMSDelivery{MessageID: "report46", ICCID: legacy, IMSI: imsi, Peer: peer})
	create(&CardPolicy{ICCID: legacy, Source: "user", VoWiFiEnabled: true, AirplaneEnabled: true})
	create(&CardPolicy{ICCID: issue46ICCID, Source: "auto"})
	create(&PendingPhoneNumber{ICCID: legacy, PhoneNumber: "13800000000"})
	create(&OutboundUsage{ICCID: legacy, Kind: "sms", Events: []outbound.Event{{At: now, Units: 1}}})
	create(&OutboundUsage{ICCID: issue46ICCID, Kind: "sms", Events: []outbound.Event{{At: now.Add(time.Second), Units: 2}}})
	create(&mbn.Policy{Key: "sim:" + legacy, Mode: "fixed", Profile: "saved"})
	create(&mbn.Operation{ID: "uncertain46", DeviceID: "wwan0", RequestID: "46", Status: "needs_attention", ExpectedOwner: mbn.Owner{IMEI: imei, ICCID: legacy}})
	// Exercise the real startup migration, including reopening and idempotence.
	for run := 0; run < 2; run++ {
		if err := Init(path); err != nil {
			t.Fatal(err)
		}
		identity, found, err := LookupSMSIdentityForDevice(SMSDeviceSelector{DeviceID: "wwan0", IMEI: imei})
		if err != nil || !found || identity.ICCID != issue46ICCID || identity.IMSI != imsi {
			t.Fatalf("device identity=%+v, found=%v, err=%v", identity, found, err)
		}
		resolved, err := ResolveICCIDForIMSI(imsi)
		if err != nil || resolved != issue46ICCID {
			t.Fatalf("IMSI identity=%q err=%v", resolved, err)
		}
		messages, err := GetSMSByICCID(issue46ICCID, 10)
		if err != nil || len(messages) != 2 {
			t.Fatalf("filtered messages=%v err=%v", messages, err)
		}
		var contact SMSContact
		if err := DB.First(&contact, "iccid = ? AND peer = ?", issue46ICCID, peer).Error; err != nil {
			t.Fatal(err)
		}
		if contact.UnreadCount != 1 || contact.LastContent != "unread" {
			t.Fatalf("contact=%+v", contact)
		}
		var sim SIMCard
		if err := DB.First(&sim, "iccid = ?", issue46ICCID).Error; err != nil {
			t.Fatal(err)
		}
		if sim.Operator != "中国移动" {
			t.Fatalf("operator=%q", sim.Operator)
		}
		if sim.CurrentIMEI == nil || *sim.CurrentIMEI != imei ||
			sim.RegStatusText != "registered" || sim.APN != "cmnet" {
			t.Fatalf("merged SIM metadata=%+v", sim)
		}
	}
	var usage OutboundUsage
	if err := DB.First(&usage, "iccid = ? AND kind = ?", issue46ICCID, "sms").Error; err != nil {
		t.Fatal(err)
	}
	if len(usage.Events) != 2 || usage.Events[0].Units != 1 || usage.Events[1].Units != 2 {
		t.Fatalf("usage=%+v", usage)
	}
	policy, err := GetCardPolicy(issue46ICCID)
	if err != nil || !policy.AirplaneEnabled || !policy.VoWiFiEnabled || policy.Source != "user" {
		t.Fatalf("policy=%+v err=%v", policy, err)
	}
	var op mbn.Operation
	if err := DB.First(&op, "id = ?", "uncertain46").Error; err != nil {
		t.Fatal(err)
	}
	if op.ExpectedOwner.ICCID != issue46ICCID || op.Status != "needs_attention" {
		t.Fatalf("operation=%+v", op)
	}
	var mbnPolicy mbn.Policy
	if err := DB.First(&mbnPolicy, "key = ?", "sim:"+issue46ICCID).Error; err != nil {
		t.Fatal(err)
	}
	if mbnPolicy.Profile != "saved" {
		t.Fatalf("MBN policy=%+v", mbnPolicy)
	}
}

func ptrString(value string) *string { return &value }

func TestSIMICCIDLiveReadRepairsOldOnlyIdentity(t *testing.T) {
	openTestDB(t)
	const imsi = "460009999999999"
	legacy := issue46Legacy
	if err := DB.Create(&SIMCard{ICCID: legacy, IMSI: imsi}).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Create(&Device{IMEI: "live46", CurrentICCID: &legacy}).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Create(&SMS{ICCID: legacy, IMSI: imsi, Peer: "10086", Type: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := UpsertSIMCardIdentity(issue46ICCID, imsi, "中国移动", ptrString("live46")); err != nil {
		t.Fatal(err)
	}
	identity, found, err := LookupDeviceSMSIdentity("live46")
	if err != nil || !found || identity.ICCID != issue46ICCID {
		t.Fatalf("identity=%+v found=%v err=%v", identity, found, err)
	}
	messages, err := GetSMSByICCID(issue46ICCID, 10)
	if err != nil || len(messages) != 1 {
		t.Fatalf("messages=%v err=%v", messages, err)
	}
}

func TestICCIDIdentityAliasesNeverGuessAcrossSIMs(t *testing.T) {
	for name, rows := range map[string][]iccidIdentityRow{
		"different IMSI":        {{issue46Legacy, "sim-a"}, {issue46ICCID, "sim-b"}},
		"missing IMSI":          {{issue46Legacy, ""}, {issue46ICCID, "sim-b"}},
		"two possible full IDs": {{issue46Legacy, "sim-a"}, {issue46ICCID, "sim-a"}, {"898600123456A7890123", "sim-a"}},
		"mixed evidence":        {{issue46Legacy, "sim-a"}, {issue46Legacy, "sim-b"}, {issue46ICCID, "sim-a"}},
		"matching prefix only":  {{"8986001234567890124", "sim-a"}, {issue46ICCID, "sim-a"}},
	} {
		t.Run(name, func(t *testing.T) {
			aliases := buildICCIDIdentityAliases(rows)
			if _, merged := aliases[issue46Legacy]; merged {
				t.Fatalf("unsafe merge: %+v", aliases)
			}
		})
	}
}
