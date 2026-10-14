package vowifihost

import (
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost"
)

func TestHealthWaitsForFirstReceptionEvidenceWithoutCountingOutage(t *testing.T) {
	store := newWiFiCallingHealthStore()
	start := time.Now()
	store.Begin("sim", start)
	state := runtimehost.State{IMSReady: true, SMSRXPending: true, UpdatedAt: start}
	store.Observe("sim", state)
	got, _ := store.Snapshot("sim", start.Add(time.Hour))
	if got.State != "pending" || got.Measured || got.HealthySeconds != 0 || got.InterruptedSeconds != 0 {
		t.Fatalf("pending health = %+v", got)
	}
	state.SMSRXPending, state.SMSReady, state.SMSHealthReady = false, true, true
	state.UpdatedAt = start.Add(time.Hour)
	store.Observe("sim", state)
	got, _ = store.Snapshot("sim", start.Add(2*time.Hour))
	if got.State != "healthy" || !got.Measured || got.HealthySeconds != 3600 || got.InterruptedSeconds != 0 {
		t.Fatalf("verified health = %+v", got)
	}
}

func TestHealthDoesNotHideExistingInterruptionWithPendingEvidence(t *testing.T) {
	store := newWiFiCallingHealthStore()
	start := time.Now()
	store.Observe("sim", runtimehost.State{IMSReady: true, SMSReady: true, UpdatedAt: start})
	store.Observe("sim", runtimehost.State{Phase: "interrupted", UpdatedAt: start.Add(time.Second)})
	store.Observe("sim", runtimehost.State{IMSReady: true, SMSRXPending: true, UpdatedAt: start.Add(2 * time.Second)})
	got, _ := store.Snapshot("sim", start.Add(time.Minute))
	if got.State != "recovering" || got.InterruptionCount != 1 || got.InterruptedSeconds != 59 {
		t.Fatalf("pending hid known outage = %+v", got)
	}
}

func TestHealthPendingExposesFailureBeforeFirstSMS(t *testing.T) {
	store := newWiFiCallingHealthStore()
	start := time.Now()
	store.Observe("sim", runtimehost.State{IMSReady: true, SMSRXPending: true, UpdatedAt: start})
	store.Observe("sim", runtimehost.State{Phase: "error", LastError: "connection reset", UpdatedAt: start.Add(time.Second)})
	got, _ := store.Snapshot("sim", start.Add(time.Minute))
	if got.State != "unavailable" || got.LastReason != "connection reset" {
		t.Fatalf("hidden failure = %+v", got)
	}
	store.Observe("sim", runtimehost.State{IMSReady: true, SMSRXPending: true, UpdatedAt: start.Add(time.Minute)})
	got, _ = store.Snapshot("sim", start.Add(2*time.Minute))
	if !got.Measured || got.State != "recovering" || got.InterruptedSeconds != 119 {
		t.Fatalf("new registration hid previous known failure = %+v", got)
	}
}
