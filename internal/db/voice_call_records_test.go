package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/yibaiba/hideck/internal/phone"
	"gorm.io/gorm"
)

func TestVoiceCallStoreUpsertsAndListsNewestFirst(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "calls.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&VoiceCallRecord{}); err != nil {
		t.Fatal(err)
	}
	store := NewVoiceCallStore(database)
	ctx := context.Background()
	older := time.Now().Add(-time.Minute).UTC()
	newer := time.Now().UTC()
	if err := store.Upsert(ctx, phone.CallRecord{
		CallID: "call-old", DeviceID: "dev-1", Direction: "inbound",
		Status: phone.StatusRinging, StartedAt: older,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(ctx, phone.CallRecord{
		CallID: "call-new", DeviceID: "dev-2", ICCID: "iccid-2", Direction: "outbound",
		Peer: "888", Status: phone.StatusCalling, StartedAt: newer,
	}); err != nil {
		t.Fatal(err)
	}
	endedAt := newer.Add(5 * time.Second)
	if err := store.Upsert(ctx, phone.CallRecord{
		CallID: "call-new", DeviceID: "dev-2", ICCID: "iccid-2", Direction: "outbound",
		Peer: "888", Status: phone.StatusCompleted, StartedAt: newer, AnsweredAt: &newer,
		EndedAt: &endedAt, DurationSeconds: 5, EndReason: "local_hangup", Codec: "PCMU",
		RecordingName: "call-new.mp3", PCAPName: "call-new.pcap",
	}); err != nil {
		t.Fatal(err)
	}
	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].CallID != "call-new" || records[1].CallID != "call-old" {
		t.Fatalf("records = %+v", records)
	}
	if records[0].Status != phone.StatusCompleted || records[0].RecordingName != "call-new.mp3" || records[0].PCAPName != "call-new.pcap" {
		t.Fatalf("updated record = %+v", records[0])
	}
}

func TestVoiceCallStoreAbandonsIncompleteRecords(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "calls.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&VoiceCallRecord{}); err != nil {
		t.Fatal(err)
	}
	store := NewVoiceCallStore(database)
	ctx := context.Background()
	started := time.Now().Add(-20 * time.Second).UTC()
	if err := store.Upsert(ctx, phone.CallRecord{
		CallID: "ghost-ring", DeviceID: "wwan0", Direction: "inbound",
		Peer: "+1555550100", Status: phone.StatusRinging, StartedAt: started,
	}); err != nil {
		t.Fatal(err)
	}
	ended := started.Add(20 * time.Second)
	if err := store.AbandonIncomplete(ctx, ended, "process_restart"); err != nil {
		t.Fatal(err)
	}
	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Status != phone.StatusMissed || records[0].EndReason != "process_restart" {
		t.Fatalf("abandoned = %+v", records)
	}
	if records[0].EndedAt == nil || records[0].DurationSeconds != 20 {
		t.Fatalf("abandoned timestamps = %+v", records[0])
	}
}

func TestVoiceCallStoreKeepsBothRecordsWhenQMISlotIsReused(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "calls.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&VoiceCallRecord{}); err != nil {
		t.Fatal(err)
	}
	store := NewVoiceCallStore(database)
	ctx := context.Background()
	firstStart := time.Now().Add(-2 * time.Minute).UTC()
	firstEnd := firstStart.Add(116 * time.Second)
	if err := store.Upsert(ctx, phone.CallRecord{
		CallID: "volte-wwan0-1-100-1", DeviceID: "wwan0", Direction: "inbound",
		Peer: "13200000002", Status: phone.StatusRejected, StartedAt: firstStart,
		EndedAt: &firstEnd, DurationSeconds: 116, EndReason: "rejected",
	}); err != nil {
		t.Fatal(err)
	}
	secondStart := time.Now().UTC()
	secondEnd := secondStart.Add(29 * time.Second)
	if err := store.Upsert(ctx, phone.CallRecord{
		CallID: "volte-wwan0-1-200-2", DeviceID: "wwan0", Direction: "outbound",
		Peer: "10000", Status: phone.StatusFailed, StartedAt: secondStart,
		EndedAt: &secondEnd, DurationSeconds: 29, EndReason: "local_hangup",
	}); err != nil {
		t.Fatal(err)
	}
	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2 (QMI slot reuse must not overwrite history)", len(records))
	}
	if records[0].CallID != "volte-wwan0-1-200-2" || records[1].CallID != "volte-wwan0-1-100-1" {
		t.Fatalf("records = %+v", records)
	}
	if records[1].Status != phone.StatusRejected || records[1].Peer != "13200000002" {
		t.Fatalf("first inbound overwritten: %+v", records[1])
	}
}

func TestVoiceCallStoreClearsMediaOfOldCalls(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "calls.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&VoiceCallRecord{}); err != nil {
		t.Fatal(err)
	}
	store := NewVoiceCallStore(database)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, record := range []phone.CallRecord{
		{CallID: "old", DeviceID: "dev-1", Direction: "inbound", Status: phone.StatusCompleted,
			StartedAt: now.AddDate(0, 0, -40), RecordingName: "old.wav", PCAPName: "old.pcap"},
		{CallID: "new", DeviceID: "dev-1", Direction: "inbound", Status: phone.StatusCompleted,
			StartedAt: now.AddDate(0, 0, -1), RecordingName: "new.wav", PCAPName: "new.pcap"},
	} {
		if err := store.Upsert(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	cleared, err := store.ClearMediaBefore(ctx, now.AddDate(0, 0, -30))
	if err != nil || cleared != 1 {
		t.Fatalf("cleared=%d err=%v", cleared, err)
	}
	records, err := store.List(ctx, 10)
	if err != nil || len(records) != 2 {
		t.Fatalf("records=%v err=%v", records, err)
	}
	for _, record := range records {
		hasMedia := record.RecordingName != "" || record.PCAPName != ""
		if (record.CallID == "old") == hasMedia {
			t.Fatalf("record %s recording=%q pcap=%q", record.CallID, record.RecordingName, record.PCAPName)
		}
	}
}
