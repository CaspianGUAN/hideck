package imscore

import "testing"

// A periodic REGISTER refresh keeps the binding; the UI logged every refresh
// as "IMS registration is not ready" for the second it took (AIS, 2026-10-15).
func TestRegisterRefreshStaysRegistered(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	service.mu.Lock()
	service.regState = regReregister
	service.mu.Unlock()
	if readiness := service.SMSReadiness(); !readiness.Registered || readiness.Reason == smsReadyReasonNotRegistered {
		t.Fatalf("readiness during refresh = %+v", readiness)
	}
	if !service.registrationInProgress() {
		t.Fatal("refresh is not reported as a registration in progress")
	}

	service.mu.Lock()
	service.regState = regRegistering
	service.mu.Unlock()
	if service.SMSReadiness().Registered {
		t.Fatal("initial registration reported as registered")
	}
}
