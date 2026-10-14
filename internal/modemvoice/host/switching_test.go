package host

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReenableDuringCleanupWaitsAndStartsNewGeneration(t *testing.T) {
	var prepared atomic.Int32
	cleanupStarted, releaseCleanup := make(chan struct{}), make(chan struct{})
	var cleanupOnce, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCleanup) }) }
	t.Cleanup(release)
	c := New(Options{Listen: listenTest, Prepare: func(context.Context, string) (*Resources, error) {
		n := prepared.Add(1)
		return &Resources{Port: &testPort{}, Check: func(ctx context.Context) error { return ctx.Err() },
			Close: func(context.Context) error {
				if n == 1 {
					cleanupOnce.Do(func() { close(cleanupStarted) })
					<-releaseCleanup
				}
				return nil
			}}, nil
	}})
	c.Enable(context.Background(), "d")
	waitFor(t, func() bool { return c.DeviceStatus("d")["ready"] == true })
	old := c.get("d")
	stopped := make(chan error, 1)
	go func() { stopped <- c.Disable(context.Background(), "d") }()
	select {
	case <-cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not start")
	}
	c.Enable(context.Background(), "d")
	if c.get("d") == old {
		t.Fatal("enable was lost while canceled generation was cleaning up")
	}
	if prepared.Load() != 1 {
		t.Fatal("new runtime prepared before old cleanup")
	}
	release()
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return prepared.Load() == 2 && c.DeviceStatus("d")["ready"] == true })
	if err := c.Disable(context.Background(), "d"); err != nil {
		t.Fatal(err)
	}
}

func TestAddingAndStoppingDeviceDoesNotStopAnotherRuntime(t *testing.T) {
	var closedA, closedB atomic.Int32
	c := New(Options{Listen: listenTest, Prepare: func(_ context.Context, id string) (*Resources, error) {
		return &Resources{Port: &testPort{}, Check: func(ctx context.Context) error { return ctx.Err() },
			Close: func(context.Context) error {
				if id == "a" {
					closedA.Add(1)
				} else {
					closedB.Add(1)
				}
				return nil
			}}, nil
	}})
	c.Enable(context.Background(), "a")
	waitFor(t, func() bool { return c.DeviceStatus("a")["ready"] == true })
	first := c.get("a")
	c.Enable(context.Background(), "b")
	waitFor(t, func() bool { return c.DeviceStatus("b")["ready"] == true })
	if err := c.Disable(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	if c.get("a") != first || first.ctx.Err() != nil || closedA.Load() != 0 || closedB.Load() != 1 {
		t.Fatal("second device changed the first runtime")
	}
	if err := c.Disable(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
}
