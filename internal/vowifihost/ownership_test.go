package vowifihost

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost"
)

func TestRetiredVoWiFiEventsCannotChangeReplacementStartup(t *testing.T) {
	m := NewManager()
	const id = "dev-ownership"
	oldClaim := m.BeginStart(id)
	old := &runtimehost.Instance{}
	observer := m.runtimeStateObserver(id, oldClaim.Epoch, newRuntimeReadinessTracker(m, runtimeReadinessConfig{DeviceID: id}))
	if !m.ClaimStarted(id, oldClaim.Epoch, old) {
		t.Fatal("old claim rejected")
	}
	m.InvalidateRuntime(id, "remove_worker")
	m.StopInstanceForTeardown(context.Background(), id, "remove")
	newClaim := m.BeginStart(id)
	m.BeginWiFiCallingHealth(id)
	state := runtimehost.State{DeviceID: id, Phase: runtimehost.PhaseSIMReady, UpdatedAt: time.Now()}
	m.RecordStartupStateForEpoch(id, newClaim.Epoch, state)
	observer.OnRuntimeHostEvent(context.Background(), runtimehost.Event{
		Session: old, State: runtimehost.State{DeviceID: id, Phase: "error", LastError: "old error", UpdatedAt: time.Now().Add(time.Second)},
	})
	m.FailStart(id, oldClaim.Epoch, runtimehost.State{}, errors.New("late prepare failure"))
	got, _ := m.State(id)
	if got.Phase != state.Phase || got.LastError != "" || !m.Starting(id) {
		t.Fatalf("old startup changed replacement: %+v", got)
	}
	if m.RecordStartupStateForEpoch(id, oldClaim.Epoch, state) {
		t.Fatal("retired epoch accepted state")
	}
}

func TestVoWiFiEpochSurvivesClearAndChangesOnRetry(t *testing.T) {
	m := NewManager()
	const id = "epoch"
	first := m.BeginStart(id)
	m.InvalidateRuntime(id, "remove")
	m.RecordStartupState(id, runtimehost.State{Phase: "stopped", UpdatedAt: time.Now()})
	m.ClearStartupStateAndBroadcast(id)
	if m.CurrentEpoch(id) <= first.Epoch {
		t.Fatal("invalidation epoch was discarded")
	}
	next := m.BeginStart(id)
	m.FailStart(id, next.Epoch, runtimehost.State{}, errors.New("retryable"))
	retry := m.BeginStart(id)
	if retry.Epoch <= next.Epoch || m.ClaimStarted(id, first.Epoch, &runtimehost.Instance{}) {
		t.Fatal("retry reused retired ownership")
	}
}

func TestVoWiFiStaleStartDoesNotInvokeRuntime(t *testing.T) {
	m := NewManager()
	claim := m.BeginStart("dev")
	m.InvalidateRuntime("dev", "disabled")
	m.SetRuntimeStartForTest(func(context.Context, runtimehost.StartRequest) (*runtimehost.Instance, error) {
		t.Fatal("stale startup performed runtime I/O")
		return nil, nil
	})
	_, err := m.StartRuntime(context.Background(), RuntimeStartRequest{DeviceID: "dev", Epoch: claim.Epoch})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stale startup error: %v", err)
	}
}

func TestVoWiFiInvalidationCancelsRunAndRejectsOldRecovery(t *testing.T) {
	m := NewManager()
	started := make(chan context.Context, 1)
	done := make(chan error, 1)
	m.SetLifecycleRunForTest(func(ctx context.Context, cmd LifecycleCommand) error {
		started <- ctx
		<-ctx.Done()
		return ctx.Err()
	})
	go func() { done <- m.Enable(context.Background(), "dev") }()
	ctx := <-started
	oldGeneration := m.CurrentLifecycleGeneration("dev")
	m.InvalidateRuntime("dev", "remove_worker")
	if ctx.Err() == nil || !errors.Is(<-done, context.Canceled) {
		t.Fatal("invalidation failed to cancel startup")
	}
	ran := false
	m.SetLifecycleRunForTest(func(context.Context, LifecycleCommand) error { ran = true; return nil })
	if err := m.Recover(context.Background(), LifecycleRecoverRequest{DeviceID: "dev", Generation: oldGeneration}); err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("old recovery ran after deletion")
	}
	if err := m.Enable(context.Background(), "dev"); err != nil || !ran {
		t.Fatal("replacement could not start", err)
	}
}

func TestVoWiFiRetiredRecoveryDoesNotPublishResult(t *testing.T) {
	m := NewManager()
	started, release := make(chan struct{}), make(chan struct{})
	var reported atomic.Bool
	m.SetLifecycleRunForTest(func(context.Context, LifecycleCommand) error {
		close(started)
		<-release
		return errors.New("old runtime error")
	})
	m.BeginDesiredRecover("dev", time.Now())
	req := DesiredRecoverRequest{DeviceID: "dev", Generation: m.NextLifecycleGeneration("dev"),
		OnResult: func(string, string, error) { reported.Store(true) }}
	done := make(chan struct{})
	go func() { m.runDesiredRecover(context.Background(), req); close(done) }()
	<-started
	m.InvalidateRuntime("dev", "remove_worker")
	close(release)
	<-done
	if m.HasDesiredRecoverState("dev") || reported.Load() {
		t.Fatal("retired recovery changed new ownership")
	}
}

func TestVoWiFiStaleQueuedCommandCannotAdvanceGeneration(t *testing.T) {
	c := NewLifecycleController()
	d := c.device("dev")
	old := lifecycleSubmission{command: LifecycleCommand{DeviceID: "dev", Kind: LifecycleCommandEnable}, invalidation: c.invalidationVersion(d)}
	c.Invalidate("dev")
	generation := c.CurrentGeneration("dev")
	ctx, _, clear := c.bindCommandRun(context.Background(), d, old)
	defer clear()
	if ctx.Err() == nil || c.CurrentGeneration("dev") != generation {
		t.Fatal("retired command changed replacement generation")
	}
}

func TestVoWiFiPreemptionWaitsForPriorHardwareCleanup(t *testing.T) {
	for _, kind := range []LifecycleCommandKind{LifecycleCommandRestart, LifecycleCommandSwitchBegin} {
		t.Run(kind.String(), func(t *testing.T) {
			c := NewLifecycleController()
			started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			done := make(chan error, 2)
			var replacementRan atomic.Bool
			c.TestRun = func(ctx context.Context, cmd LifecycleCommand) error {
				if cmd.Kind != LifecycleCommandEnable {
					replacementRan.Store(true)
					return nil
				}
				close(started)
				<-ctx.Done()
				close(canceled)
				<-release
				return ctx.Err()
			}
			go func() {
				done <- c.Submit(context.Background(), LifecycleCommand{DeviceID: "dev", Kind: LifecycleCommandEnable})
			}()
			<-started
			go func() { done <- c.Submit(context.Background(), LifecycleCommand{DeviceID: "dev", Kind: kind}) }()
			<-canceled
			if replacementRan.Load() {
				t.Error("new command ran before old hardware cleanup")
			}
			close(release)
			for i := 0; i < 2; i++ {
				if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			if !replacementRan.Load() {
				t.Fatal("new command never ran")
			}
		})
	}
}
