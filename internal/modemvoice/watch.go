package modemvoice

import (
	"context"
	"errors"
	"time"
)

const defaultPollInterval = time.Second

type WatchOptions struct {
	Wakeups   <-chan struct{}
	Interval  time.Duration
	OnChanges func([]Change)
	OnError   func(error)
}

// Watch polls serially: slow SMS/AT transactions cannot create overlapping
// CLCC requests. URCs accelerate a poll but never invent connected/ended state.
// Cancel the generation context and join Watch before switching SIM ownership.
func (c *Client) Watch(ctx context.Context, options WatchOptions) error {
	tracker := NewTracker()
	return watchSnapshots(ctx, options, watchSource{refresh: func(ctx context.Context) ([]Change, error) {
		calls, err := c.Calls(ctx)
		if err != nil {
			return nil, err
		}
		return tracker.applyCalls(calls), nil
	}})
}

type watchSource struct {
	wake    <-chan struct{}
	refresh func(context.Context) ([]Change, error)
}

func watchSnapshots(ctx context.Context, options WatchOptions, source watchSource) error {
	if ctx == nil || options.OnChanges == nil || options.OnError == nil {
		return errors.New("modem voice: watcher requires context and observers")
	}
	if options.Interval < 0 {
		return errors.New("modem voice: negative poll interval")
	}
	interval := options.Interval
	if interval == 0 {
		interval = defaultPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		changes, err := source.refresh(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			options.OnError(err)
		} else if len(changes) != 0 {
			options.OnChanges(changes)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-options.Wakeups:
			if !ok {
				return errors.New("modem voice: AT event source closed")
			}
		case <-ticker.C:
		case <-source.wake:
		}
	}
}
