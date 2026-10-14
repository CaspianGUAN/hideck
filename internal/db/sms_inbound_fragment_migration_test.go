package db

import (
	"fmt"
	"testing"
	"time"
)

func TestMigrateSMSInboundFragmentIdentityUsesCurrentDeviceSIM(t *testing.T) {
	openTestDB(t)
	if err := DB.Exec("DROP INDEX IF EXISTS " + smsInboundFragmentICCIDIndex).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS ` + legacySMSInboundFragmentIndex +
		` ON sms_inbound_fragment(device_id, imsi, session_key, sequence)`).Error; err != nil {
		t.Fatal(err)
	}
	iccid := "8944100000000000066"
	if err := DB.Create(&Device{IMEI: "860000000000066", Alias: "wwan6", CurrentICCID: &iccid}).Error; err != nil {
		t.Fatal(err)
	}
	row := SMSInboundFragment{
		DeviceID: "wwan6", IMSI: "234150000000066", SessionKey: "ref=66",
		Sequence: 1, Total: 2, Content: "first", ArrivedAt: time.Now(),
	}
	if err := DB.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateSMSInboundFragmentIdentity(DB); err != nil {
		t.Fatal(err)
	}
	var migrated SMSInboundFragment
	if err := DB.First(&migrated, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if migrated.ICCID != iccid {
		t.Fatalf("migrated ICCID=%q want %q", migrated.ICCID, iccid)
	}
	if DB.Migrator().HasIndex(&SMSInboundFragment{}, legacySMSInboundFragmentIndex) {
		t.Fatal("legacy fragment index still exists")
	}
	if !DB.Migrator().HasIndex(&SMSInboundFragment{}, smsInboundFragmentICCIDIndex) {
		t.Fatal("ICCID fragment index was not created")
	}
}

func TestMigrateSMSInboundFragmentIdentityMergesDuplicateSIMFragments(t *testing.T) {
	openTestDB(t)
	if err := DB.Exec("DROP INDEX IF EXISTS " + smsInboundFragmentICCIDIndex).Error; err != nil {
		t.Fatal(err)
	}
	iccid := "8944100000000000077"
	for index, deviceID := range []string{"wwan7", "wwan8"} {
		deviceICCID := iccid
		if err := DB.Create(&Device{
			IMEI: fmt.Sprintf("86000000000007%d", index), Alias: deviceID, CurrentICCID: &deviceICCID,
		}).Error; err != nil {
			t.Fatal(err)
		}
		row := SMSInboundFragment{
			DeviceID: deviceID, IMSI: "234150000000077", SessionKey: "ref=77",
			Sequence: 1, Reference: 77, Total: 2, Content: "first", ArrivedAt: time.Now(),
			RPMR: index + 10, CallID: fmt.Sprintf("retry-%d", index),
			ToURI:         fmt.Sprintf("sip:local-%d@example.test", index),
			ServiceCenter: fmt.Sprintf("+44770090000%d", index),
			AckSent:       index == 1,
		}
		if err := DB.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := MigrateSMSInboundFragmentIdentity(DB); err != nil {
		t.Fatal(err)
	}
	var rows []SMSInboundFragment
	if err := DB.Where("iccid = ?", iccid).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].AckSent {
		t.Fatalf("merged fragments=%+v", rows)
	}
}
