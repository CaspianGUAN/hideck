package host

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDisableRetriesFailedRuntimeCleanup(t *testing.T) {
	var attempts atomic.Int32
	failure := errors.New("ADB transport temporarily offline")
	c := New(Options{Listen: listenTest, Prepare: func(context.Context, string) (*Resources, error) {
		return &Resources{Port: &testPort{}, Check: func(ctx context.Context) error { return ctx.Err() },
			Close: func(context.Context) error {
				if attempts.Add(1) < 3 {
					return failure
				}
				return nil
			}}, nil
	}})
	c.Enable(context.Background(), "d")
	waitFor(t, func() bool { return c.DeviceStatus("d")["ready"] == true })
	for want := int32(1); want <= 2; want++ {
		if err := c.Disable(context.Background(), "d"); !errors.Is(err, failure) {
			t.Fatalf("stop error = %v", err)
		}
		if attempts.Load() != want {
			t.Fatalf("cleanup attempts = %d, want %d", attempts.Load(), want)
		}
		if c.get("d") == nil {
			t.Fatal("failed cleanup ownership discarded")
		}
	}
	if err := c.Disable(context.Background(), "d"); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 3 || c.get("d") != nil {
		t.Fatal("recovered cleanup did not remove stopped runtime")
	}
}

func TestEnableWaitsForExplicitCleanupRetry(t *testing.T) {
	var prepared, oldCloses, newCloses atomic.Int32
	retrying, releaseRetry := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRetry) }) }
	t.Cleanup(release)
	c := New(Options{Listen: listenTest, Prepare: func(context.Context, string) (*Resources, error) {
		generation := prepared.Add(1)
		return &Resources{Port: &testPort{}, Check: func(ctx context.Context) error { return ctx.Err() },
			Close: func(context.Context) error {
				if generation != 1 {
					newCloses.Add(1)
					return nil
				}
				if oldCloses.Add(1) == 1 {
					return errors.New("ADB unavailable")
				}
				close(retrying)
				<-releaseRetry
				return nil
			}}, nil
	}})
	c.Enable(context.Background(), "d")
	waitFor(t, func() bool { return c.DeviceStatus("d")["ready"] == true })
	if err := c.Disable(context.Background(), "d"); err == nil {
		t.Fatal("lost initial failure")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- c.Disable(context.Background(), "d") }()
	select {
	case <-retrying:
	case <-time.After(time.Second):
		t.Fatal("explicit stop did not retry cleanup")
	}
	c.Enable(context.Background(), "d")
	if prepared.Load() != 1 {
		t.Fatal("prepared new runtime before old cleanup finished")
	}
	release()
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return prepared.Load() == 2 && c.DeviceStatus("d")["ready"] == true })
	if oldCloses.Load() != 2 || newCloses.Load() != 0 {
		t.Fatal("old cleanup ran twice or touched new runtime")
	}
	if err := c.Disable(context.Background(), "d"); err != nil {
		t.Fatal(err)
	}
	if newCloses.Load() != 1 {
		t.Fatal("replacement did not retain its own cleanup")
	}
}

func TestTransferredCleanupCannotBeRetriedByOldStop(t *testing.T) {
	var closes atomic.Int32
	failure := errors.New("old cleanup failed")
	old := &device{id: "d", cleanupErr: failure, resources: &Resources{Close: func(context.Context) error {
		closes.Add(1)
		return nil
	}}}
	replacement := &device{id: "d", previous: old}
	c := New(Options{})
	c.devices["d"] = replacement
	c.takePendingCleanup(replacement)
	if err := c.finishDisable(context.Background(), old, true); !errors.Is(err, failure) {
		t.Fatalf("old failure = %v", err)
	}
	if closes.Load() != 0 || c.get("d") != replacement {
		t.Fatal("old stop touched replacement ownership")
	}
	if err := c.cleanup(replacement); err != nil {
		t.Fatal(err)
	}
	if closes.Load() != 1 {
		t.Fatal("replacement could not finish transferred cleanup")
	}
}
