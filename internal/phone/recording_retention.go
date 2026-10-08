package phone

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/yibaiba/hideck/pkg/logger"
)

const recordingRetentionInterval = 6 * time.Hour

// RecordingRetentionOptions configures the periodic recording cleanup.
type RecordingRetentionOptions struct {
	Directory string
	Days      int
	// ClearMedia drops the file names of calls that started before cutoff.
	ClearMedia func(ctx context.Context, cutoff time.Time) (int64, error)
}

// StartRecordingRetention deletes recordings and PCAPs older than the
// retention period now and every six hours until ctx ends. Days <= 0 keeps
// everything.
func StartRecordingRetention(ctx context.Context, options RecordingRetentionOptions) {
	if options.Days <= 0 || options.Directory == "" {
		return
	}
	go func() {
		ticker := time.NewTicker(recordingRetentionInterval)
		defer ticker.Stop()
		for {
			runRecordingRetention(ctx, options, time.Now())
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func runRecordingRetention(ctx context.Context, options RecordingRetentionOptions, now time.Time) {
	cutoff := now.AddDate(0, 0, -options.Days)
	removed, err := PruneRecordings(options.Directory, cutoff)
	if err != nil {
		logger.Warn("清理过期通话录音失败", "dir", options.Directory, "err", err)
	}
	var cleared int64
	if options.ClearMedia != nil {
		var clearErr error
		cleared, clearErr = options.ClearMedia(ctx, cutoff)
		if clearErr != nil {
			logger.Warn("清除过期通话记录的录音链接失败", "err", clearErr)
		}
	}
	if removed > 0 || cleared > 0 {
		logger.Info("已清理过期通话录音", "retention_days", options.Days, "files", removed, "records", cleared)
	}
}

// PruneRecordings removes regular files in dir last modified before cutoff.
func PruneRecordings(dir string, cutoff time.Time) (int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	var errs []error
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}
