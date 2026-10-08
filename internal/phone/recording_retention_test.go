package phone

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordingRetentionRemovesOldFilesAndClearsRecords(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	write := func(name string, age time.Duration) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	write("call_old.pcap", 31*24*time.Hour)
	write("call_old_mixed.mp3", 40*24*time.Hour)
	write("call_new.pcap", 29*24*time.Hour)
	if err := os.Mkdir(filepath.Join(dir, "keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	var cutoff time.Time
	runRecordingRetention(context.Background(), RecordingRetentionOptions{
		Directory: dir, Days: 30,
		ClearMedia: func(_ context.Context, value time.Time) (int64, error) {
			cutoff = value
			return 1, nil
		},
	}, now)
	if want := now.AddDate(0, 0, -30); !cutoff.Equal(want) {
		t.Fatalf("cutoff = %s, want %s", cutoff, want)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if len(names) != 2 || names[0] != "call_new.pcap" || names[1] != "keep" {
		t.Fatalf("remaining = %v", names)
	}
}

func TestPruneRecordingsIgnoresMissingDirectory(t *testing.T) {
	removed, err := PruneRecordings(filepath.Join(t.TempDir(), "absent"), time.Now())
	if err != nil || removed != 0 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
}
