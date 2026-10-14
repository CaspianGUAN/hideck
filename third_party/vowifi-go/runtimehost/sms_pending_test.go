package runtimehost

import "testing"

func TestSMSReceptionPendingPropagation(t *testing.T) {
	state := State{IMSReady: true}
	applySMSReadiness(&state, SMSReadiness{Registered: true, ReceiverPending: true, MOReady: true})
	if !state.SMSRXPending || state.SMSReady || state.SMSHealthReady || !state.SMSMOReady || state.LastEvent != "sms_pending" {
		t.Fatalf("pending = %+v", state)
	}
	applySMSReadiness(&state, SMSReadiness{Registered: true, Ready: true, HealthReady: true, MOReady: true})
	if state.SMSRXPending || !state.SMSReady || !state.SMSHealthReady {
		t.Fatalf("ready = %+v", state)
	}
	applySMSReadiness(&state, SMSReadiness{})
	if state.SMSRXPending || state.SMSReady || state.SMSHealthReady {
		t.Fatalf("failed = %+v", state)
	}
}

func TestIMSRegistrationWaitsForCurrentSMSReadinessSnapshot(t *testing.T) {
	observer := &instanceObserver{hasSMSReadiness: true, latestSMSReadiness: SMSReadiness{Registered: false}}
	state := State{IMSReady: true}
	observer.applyLatestSMSReadiness(&state)
	if !state.SMSRXPending || state.SMSReady || state.SMSHealthReady {
		t.Fatalf("old readiness became an outage: %+v", state)
	}
	observer.latestSMSReadiness = SMSReadiness{Registered: true, ReceiverPending: true}
	observer.applyLatestSMSReadiness(&state)
	if !state.SMSRXPending {
		t.Fatal("lost pending reception")
	}
	observer.latestSMSReadiness = SMSReadiness{Registered: true}
	observer.applyLatestSMSReadiness(&state)
	if state.SMSRXPending {
		t.Fatal("explicit receiver failure became pending")
	}
}
