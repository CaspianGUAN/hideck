package host

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestPrepareRestartingNeverPublishesReadyAndCanBeCanceled(t *testing.T) {
	c := New(Options{Prepare: func(context.Context, string) (*Resources, error) { return nil, ErrPrepareRestarting }})
	c.Enable(context.Background(), "d")
	waitFor(t, func() bool { return c.DeviceStatus("d")["phase"] == "restarting" })
	if c.DeviceStatus("d")["ready"] != false {
		t.Fatal("reset request became ready")
	}
	if err := c.Disable(context.Background(), "d"); err != nil {
		t.Fatal(err)
	}
	if c.get("d") != nil {
		t.Fatal("restart preparation retained after mode switch")
	}
}

func TestRestartedDeviceCanPrepareNewSession(t *testing.T) {
	var attempts atomic.Int32
	c := New(Options{Prepare: func(context.Context, string) (*Resources, error) {
		if attempts.Add(1) == 1 {
			return nil, ErrPrepareRestarting
		}
		return &Resources{Port: &testPort{}, Check: func(ctx context.Context) error { return ctx.Err() }}, nil
	}})
	c.Enable(context.Background(), "d")
	waitFor(t, func() bool { return c.DeviceStatus("d")["phase"] == "restarting" })
	<-c.get("d").done
	c.Enable(context.Background(), "d")
	waitFor(t, func() bool { return c.DeviceStatus("d")["ready"] == true })
	if attempts.Load() != 2 {
		t.Fatal(attempts.Load())
	}
	if err := c.Disable(context.Background(), "d"); err != nil {
		t.Fatal(err)
	}
}
