package modem

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/modemvoice"
)

func TestVoiceSessionReleasesStoppedATOwner(t *testing.T) {
	for _, backend := range []string{"at", "mbim"} {
		t.Run(backend, func(t *testing.T) {
			cfg := config.DeviceConfig{ID: "voice", DeviceBackend: backend, ATPort: "/dev/test-at"}
			factory := New
			if backend == "mbim" {
				factory = NewSMSAuxiliary
			}
			m, err := factory(cfg)
			if err != nil {
				t.Fatal(err)
			}
			m.setRunning(true)
			s, err := modemvoice.NewSession(context.Background(), m)
			if err != nil {
				t.Fatal(err)
			}
			if !m.StopAndWait(time.Second) {
				t.Fatal("AT owner did not finish stopping")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			for range 2 {
				if err := s.Close(ctx); err != nil {
					t.Fatal("fully stopped owner prevented cleanup", err)
				}
			}
		})
	}
}

func TestVoiceFenceWaitsForStoppedOwnerLoop(t *testing.T) {
	m := newRunningTestManager(t)
	release := make(chan struct{})
	m.loopWG.Add(1)
	go func() { defer m.loopWG.Done(); <-release }()
	t.Cleanup(func() { close(release); m.StopAndWait(time.Second) })
	m.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := m.WaitATIdle(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unfinished serial loop accepted as drained: %v", err)
	}
}

func TestStopDuringVoiceFenceWaitsForLoopAndAllowsRetry(t *testing.T) {
	m := newRunningTestManager(t)
	release := make(chan struct{})
	m.loopWG.Add(1)
	go func() { defer m.loopWG.Done(); <-release }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.WaitATIdle(ctx) }()
	select {
	case <-m.cmdChan:
	case <-ctx.Done():
		close(release)
		t.Fatal("fence not queued")
	}
	m.Stop()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("pending serial work lost cancellation: %v", err)
	}
	close(release)
	if !m.StopAndWait(time.Second) {
		t.Fatal("loop did not stop")
	}
	if err := m.WaitATIdle(context.Background()); err != nil {
		t.Fatal("retry after loop exit", err)
	}
}

func TestVoiceFenceDoesNotTreatUnhealthyRunningOwnerAsStopped(t *testing.T) {
	m := newRunningTestManager(t)
	defer m.Stop()
	m.markUnhealthy()
	if err := m.WaitATIdle(context.Background()); err == nil {
		t.Fatal("unhealthy running owner accepted as drained")
	}
}

func TestStoppedATOwnerCannotStartAgain(t *testing.T) {
	m := newRunningTestManager(t)
	if !m.StopAndWait(time.Second) {
		t.Fatal("owner did not stop")
	}
	if err := m.Start(); err == nil {
		t.Fatal("stopped owner restarted after publishing completion")
	}
}
