package db

import (
	"strings"
	"testing"
)

func TestSIMICCIDMigrationKeepsOtherCardsAndRollsBackFailures(t *testing.T) {
	openTestDB(t)
	const imsi = "460001111111111"
	legacy := issue46Legacy
	for _, row := range []any{
		&SIMCard{ICCID: legacy, IMSI: imsi},
		&SIMCard{ICCID: issue46ICCID, IMSI: imsi},
		&Device{IMEI: "atomic46", CurrentICCID: &legacy},
		&SMS{ICCID: legacy, IMSI: imsi, Peer: "10086", Type: 1},
		&CardPolicy{ICCID: legacy, Source: "user", AirplaneEnabled: true},
	} {
		if err := DB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	// A failure after SIM/device/policy updates must restore every original row.
	if err := DB.Exec("CREATE TRIGGER fail_iccid46 BEFORE UPDATE OF iccid ON sms BEGIN SELECT RAISE(ABORT, 'test46'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateSIMICCIDIdentities(DB); err == nil || !strings.Contains(err.Error(), "test46") {
		t.Fatalf("expected migration failure, got %v", err)
	}
	var count int64
	if err := DB.Model(&SIMCard{}).Where("iccid IN ?", []string{legacy, issue46ICCID}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("SIM merge was not rolled back: %d", count)
	}
	identity, found, err := LookupDeviceSMSIdentity("atomic46")
	if err != nil || !found || identity.ICCID != legacy {
		t.Fatalf("binding changed after failure: %+v %v", identity, err)
	}
	if err := DB.First(&CardPolicy{}, "iccid = ?", legacy).Error; err != nil {
		t.Fatalf("policy not rolled back: %v", err)
	}
}

func TestSIMICCIDMigrationDoesNotMergeDifferentIMSIs(t *testing.T) {
	openTestDB(t)
	legacy := issue46Legacy
	for _, row := range []any{
		&SIMCard{ICCID: legacy, IMSI: "first"},
		&SIMCard{ICCID: issue46ICCID, IMSI: "second"},
		&Device{IMEI: "different46", CurrentICCID: &legacy},
		&SMS{ICCID: legacy, IMSI: "first", Peer: "10086", Type: 1},
		&SMS{ICCID: issue46ICCID, IMSI: "second", Peer: "10086", Type: 1},
	} {
		if err := DB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := MigrateSIMICCIDIdentities(DB); err != nil {
		t.Fatal(err)
	}
	identity, found, err := LookupDeviceSMSIdentity("different46")
	if err != nil || !found || identity.ICCID != legacy || identity.IMSI != "first" {
		t.Fatalf("cross-SIM binding: %+v %v", identity, err)
	}
	messages, err := GetSMSByICCID(legacy, 10)
	if err != nil || len(messages) != 1 || messages[0].IMSI != "first" {
		t.Fatalf("cross-SIM messages: %+v %v", messages, err)
	}
}

func TestSIMICCIDCanonicalCasePreservesInteriorHex(t *testing.T) {
	openTestDB(t)
	const raw = "898600123456f7890123"
	for _, row := range []any{
		&SIMCard{ICCID: raw, IMSI: "case46"},
		&SMS{ICCID: raw, IMSI: "case46", Peer: "10086", Type: 1},
	} {
		if err := DB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := MigrateSIMICCIDIdentities(DB); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveICCIDForIMSI("case46")
	if err != nil || resolved != issue46ICCID {
		t.Fatalf("case identity=%q %v", resolved, err)
	}
	messages, err := GetSMSByICCID(raw, 10)
	if err != nil || len(messages) != 1 || messages[0].ICCID != issue46ICCID {
		t.Fatalf("case lookup=%+v %v", messages, err)
	}
}

func TestSIMICCIDMigrationKeepsConflictingPhysicalOwners(t *testing.T) {
	openTestDB(t)
	legacy := issue46Legacy
	for _, row := range []any{
		&SIMCard{ICCID: legacy, IMSI: "shared", CurrentIMEI: ptrString("modem-a")},
		&SIMCard{ICCID: issue46ICCID, IMSI: "shared", CurrentIMEI: ptrString("modem-b")},
		&Device{IMEI: "modem-a", CurrentICCID: &legacy},
	} {
		if err := DB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := MigrateSIMICCIDIdentities(DB); err != nil {
		t.Fatal(err)
	}
	identity, found, err := LookupDeviceSMSIdentity("modem-a")
	if err != nil || !found || identity.ICCID != legacy {
		t.Fatalf("physical identity changed: %+v %v", identity, err)
	}
}
