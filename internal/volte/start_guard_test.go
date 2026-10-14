package volte

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnableChecksOwnershipAfterAcquiringDeviceLock(t *testing.T) {
	c := NewControllerWithBackup(newFakeModem(), t.TempDir())
	locked, unlock := make(chan struct{}), make(chan struct{})
	go func() {
		_ = c.withDevice("wwan0", func() error { close(locked); <-unlock; return nil })
	}()
	<-locked
	var changed, checked atomic.Bool
	stale := errors.New("device mode changed")
	done := make(chan error, 1)
	go func() {
		done <- c.EnableChecked(context.Background(), EnableRequest{DeviceID: "wwan0", Validate: func() error {
			checked.Store(true)
			if changed.Load() {
				return stale
			}
			return nil
		}})
	}()
	select {
	case err := <-done:
		t.Fatalf("enable escaped device lock: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	checkedBeforeUnlock := checked.Load()
	changed.Store(true)
	close(unlock)
	if err := <-done; !errors.Is(err, stale) || checkedBeforeUnlock {
		t.Fatalf("ownership check did not run under device lock: %v", err)
	}
	if c.Status("wwan0").Phase != PhaseIdle {
		t.Fatal("stale request installed a native session")
	}
}

func TestCanceledEnableDoesNotCreateSession(t *testing.T) {
	c := NewControllerWithBackup(newFakeModem(), t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Enable(ctx, "wwan0"); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled enable returned", err)
	}
	if c.Status("wwan0").Phase != PhaseIdle {
		t.Fatal("canceled enable installed a session")
	}
}
