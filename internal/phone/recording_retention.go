package phone

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/yibaiba/hideck/pkg/logger"
)

const (
	recordingRetentionInterval = 6 * time.Hour
	// MaxRecordingRetentionDays bounds the configurable retention (10 years).
	MaxRecordingRetentionDays = 3650
)

// RecordingRetentionOptions configures the periodic recording cleanup.
type RecordingRetentionOptions struct {
	Directory string
	Days      int
	// ClearMedia drops the file names of calls that started before cutoff.
	ClearMedia func(ctx context.Context, cutoff time.Time) (int64, error)
}

// RecordingRetention deletes call recordings and PCAPs older than its
// retention period. Days can change while it runs; 0 keeps everything.
type RecordingRetention struct {
	options RecordingRetentionOptions
	days    atomic.Int64
	wake    chan struct{}
}

// StartRecordingRetention runs the cleanup now and every six hours until ctx
// ends, and again whenever SetDays changes the period.
func StartRecordingRetention(ctx context.Context, options RecordingRetentionOptions) *RecordingRetention {
	retention := &RecordingRetention{options: options, wake: make(chan struct{}, 1)}
	retention.days.Store(int64(options.Days))
	if options.Directory == "" {
		return retention
	}
	go func() {
		ticker := time.NewTicker(recordingRetentionInterval)
		defer ticker.Stop()
		for {
			if days := retention.Days(); days > 0 {
				runRecordingRetention(ctx, options, days, time.Now())
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-retention.wake:
			}
		}
	}()
	return retention
}

// Days returns the current retention period; 0 keeps everything.
func (r *RecordingRetention) Days() int {
	if r == nil {
		return 0
	}
	return int(r.days.Load())
}

// SetDays changes the retention period and runs a cleanup with it.
func (r *RecordingRetention) SetDays(days int) {
	if r == nil {
		return
	}
	r.days.Store(int64(days))
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func runRecordingRetention(ctx context.Context, options RecordingRetentionOptions, days int, now time.Time) {
	cutoff := now.AddDate(0, 0, -days)
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
		logger.Info("已清理过期通话录音", "retention_days", days, "files", removed, "records", cleared)
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
