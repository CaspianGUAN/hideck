package vowifihost

import (
	"sync"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost"
)

type blockingSMSRestoreAdapter struct {
	*runtimeReadinessAdapter
	entered chan struct{}
	release chan struct{}
}

func (a *blockingSMSRestoreAdapter) RestoreSMSMode(deviceID string) {
	if deviceID == "slow" {
		close(a.entered)
		<-a.release
	}
}

func TestFailedRuntimeCleanupDoesNotBlockOtherDevices(t *testing.T) {
	m := NewManager()
	a := &blockingSMSRestoreAdapter{
		runtimeReadinessAdapter: &runtimeReadinessAdapter{},
		entered:                 make(chan struct{}), release: make(chan struct{}),
	}
	m.ConfigureAdapter(a)
	var release sync.Once
	unblock := func() { release.Do(func() { close(a.release) }) }
	defer unblock()
	inst := &runtimehost.Instance{}
	m.RuntimeStore().SetInstance("slow", inst)
	cleaned := make(chan struct{})
	go func() {
		m.releaseFailedRuntime("slow", inst, runtimehost.State{SessionState: "error", LastError: "transport lost"})
		close(cleaned)
	}()
	waitDeviceStateTest(t, a.entered)

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		claim := m.BeginStart("healthy")
		if !claim.Accepted || !m.RecordStartupStateForEpoch("healthy", claim.Epoch, runtimehost.State{SessionState: "starting"}) {
			t.Error("other device cannot start or publish state")
		}
		m.InvalidateRuntime("healthy", "remove_worker")
		if m.ShouldRun("healthy", claim.Epoch) {
			t.Error("other device was not invalidated")
		}
	}()
	waitDeviceStateTest(t, finished)
	unblock()
	waitDeviceStateTest(t, cleaned)
}

func TestStateLockRetainsDeviceIdentityAndSerializesInvalidation(t *testing.T) {
	m := NewManager()
	claim := m.BeginStart("device")
	lock := m.stateLock("device")
	if lock != m.stateLock(" device ") || lock == m.stateLock("other") {
		t.Fatal("locks must use normalized, distinct device identities")
	}
	lock.Lock()
	started, done := make(chan struct{}), make(chan struct{})
	go func() {
		close(started)
		m.InvalidateRuntime("device", "remove_worker")
		close(done)
	}()
	<-started
	select {
	case <-done:
		lock.Unlock()
		t.Fatal("invalidation bypassed the device state lock")
	case <-time.After(20 * time.Millisecond):
	}
	lock.Unlock()
	waitDeviceStateTest(t, done)
	next := m.BeginStart("device")
	if !next.Accepted || m.ShouldRun("device", claim.Epoch) || lock != m.stateLock("device") {
		t.Fatal("replacement lost its stable lock or accepted the retired epoch")
	}
}

func waitDeviceStateTest(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("device state operation blocked")
	}
}
