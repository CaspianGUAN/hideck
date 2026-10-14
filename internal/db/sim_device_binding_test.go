package db

import (
	"testing"
	"time"
)

func TestSIMMoveReassignsDeviceAndCardOwnership(t *testing.T) {
	openTestDB(t)
	const (
		imeiA = "860000000000101"
		imeiB = "860000000000102"
		cardA = "8944100000000000101"
		cardB = "8944100000000000102"
	)
	for _, device := range []Device{{IMEI: imeiA, Alias: "wwan0"}, {IMEI: imeiB, Alias: "wwan1"}} {
		if err := DB.Create(&device).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := UpsertSIMCardIdentity(cardA, "imsi-a", "", ptrString(imeiA)); err != nil {
		t.Fatal(err)
	}
	if err := UpsertSIMCardIdentity(cardB, "imsi-b", "", ptrString(imeiB)); err != nil {
		t.Fatal(err)
	}
	if err := UpsertSIMCardIdentity(cardA, "imsi-a", "", ptrString(imeiB)); err != nil {
		t.Fatal(err)
	}

	assertDeviceSIMBinding(t, imeiA, "")
	assertDeviceSIMBinding(t, imeiB, cardA)
	assertSIMDeviceBinding(t, cardA, imeiB)
	assertSIMDeviceBinding(t, cardB, "")
}

func TestDetachAndReaddDeviceAliasDoesNotReuseOldSIM(t *testing.T) {
	openTestDB(t)
	const (
		alias   = "wwan0"
		oldIMEI = "860000000000111"
		newIMEI = "860000000000112"
		oldCard = "8944100000000000111"
		newCard = "8944100000000000112"
	)
	if err := DB.Create(&Device{IMEI: oldIMEI, Alias: alias}).Error; err != nil {
		t.Fatal(err)
	}
	if err := UpsertSIMCardIdentity(oldCard, "imsi-old", "", ptrString(oldIMEI)); err != nil {
		t.Fatal(err)
	}
	if err := DB.Create(&SMS{
		ICCID: oldCard, IMSI: "imsi-old", Peer: "10086", Content: "history",
		Type: 1, Timestamp: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := DetachDeviceAlias(alias); err != nil {
		t.Fatal(err)
	}
	assertDeviceSIMBinding(t, oldIMEI, "")
	assertSIMDeviceBinding(t, oldCard, "")

	if err := DB.Create(&Device{IMEI: newIMEI, Alias: alias}).Error; err != nil {
		t.Fatal(err)
	}
	if err := UpsertSIMCardIdentity(newCard, "imsi-new", "", ptrString(newIMEI)); err != nil {
		t.Fatal(err)
	}
	identity, found, err := LookupSMSIdentityForDevice(SMSDeviceSelector{DeviceID: alias})
	if err != nil || !found || identity.ICCID != newCard || identity.IMSI != "imsi-new" {
		t.Fatalf("re-added device identity=%+v found=%v err=%v", identity, found, err)
	}
	var historyCount int64
	if err := DB.Model(&SMS{}).Where("iccid = ?", oldCard).Count(&historyCount).Error; err != nil {
		t.Fatal(err)
	}
	if historyCount != 1 {
		t.Fatalf("old SIM history count=%d want 1", historyCount)
	}
}

func assertDeviceSIMBinding(t *testing.T, imei, wantICCID string) {
	t.Helper()
	var device Device
	if err := DB.First(&device, "imei = ?", imei).Error; err != nil {
		t.Fatal(err)
	}
	got := ""
	if device.CurrentICCID != nil {
		got = *device.CurrentICCID
	}
	if got != wantICCID {
		t.Fatalf("device %s ICCID=%q want %q", imei, got, wantICCID)
	}
}

func assertSIMDeviceBinding(t *testing.T, iccid, wantIMEI string) {
	t.Helper()
	var sim SIMCard
	if err := DB.First(&sim, "iccid = ?", iccid).Error; err != nil {
		t.Fatal(err)
	}
	got := ""
	if sim.CurrentIMEI != nil {
		got = *sim.CurrentIMEI
	}
	if got != wantIMEI {
		t.Fatalf("SIM %s IMEI=%q want %q", iccid, got, wantIMEI)
	}
}
