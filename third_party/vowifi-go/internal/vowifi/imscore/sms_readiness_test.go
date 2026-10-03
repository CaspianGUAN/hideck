package imscore

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/internal/vowifi/smsdelivery"
)

func TestProtectedSMSReadinessRequiresPortSFlow(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	registration, registrationPeer := net.Pipe()
	push, pushPeer := net.Pipe()
	t.Cleanup(func() {
		_ = registrationPeer.Close()
		_ = pushPeer.Close()
	})
	service.mu.Lock()
	service.registrationTCP = registration
	service.registrationTCPProtected = true
	service.mu.Unlock()

	if got := service.SMSReadiness(); got.ReceiverReady || got.Ready {
		t.Fatalf("readiness without port-s = %+v", got)
	}
	if !service.trackProtectedConnection(push) {
		t.Fatal("track port-s")
	}
	if got := service.SMSReadiness(); !got.ReceiverReady || !got.Ready {
		t.Fatalf("readiness with port-s = %+v", got)
	}
	service.untrackProtectedConnection(push)
	if got := service.SMSReadiness(); got.ReceiverReady || got.Ready {
		t.Fatalf("readiness after port-s closed = %+v", got)
	}
	service.portSOnDemandObserved.Store(true)
	service.recordPortSOpened(push, time.Now())
	service.recordPortSClosed(push, io.EOF, time.Now())
	if got := service.SMSReadiness(); !got.ReceiverReady || !got.Ready {
		t.Fatalf("on-demand port-s listener readiness = %+v", got)
	}
}

func TestProtectedSMSReadinessAllowsMOSendWithoutPortSFlow(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	registration, registrationPeer := net.Pipe()
	t.Cleanup(func() { _ = registrationPeer.Close() })
	service.mu.Lock()
	service.registrationTCP = registration
	service.registrationTCPProtected = true
	service.mu.Unlock()

	got := service.SMSReadiness()
	if got.Ready || got.ReceiverReady || !got.MOReady {
		t.Fatalf("readiness without port-s = %+v, want MT unready and MO ready", got)
	}
	if _, err := service.prepareSendEnv(nil, "+15551234567", "test", SendOptions{}); err != nil {
		t.Fatalf("prepareSendEnv without port-s: %v", err)
	}
}

func TestSMSReadinessReportsMissingSMSCBeforeReceiver(t *testing.T) {
	got := evaluateSMSReadiness(true, true, true, false, "")
	if got.MOReady || got.Reason != smsReadyReasonSMSCNotConfigured {
		t.Fatalf("readiness without receiver or SMSC = %+v, want missing SMSC", got)
	}
}

func TestProtectedSMSHealthTreatsOnlyCleanEOFAsOnDemand(t *testing.T) {
	tests := []struct {
		name       string
		closeError error
		wantReady  bool
	}{
		{name: "clean EOF", closeError: io.EOF, wantReady: true},
		{name: "peer reset", closeError: errors.New("read tcp: connection reset by peer")},
		{name: "timeout", closeError: errors.New("read tcp: operation timed out")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newProtectedKeepaliveTestService(t)
			registration, registrationPeer := net.Pipe()
			push, pushPeer := net.Pipe()
			t.Cleanup(func() {
				_ = registrationPeer.Close()
				_ = pushPeer.Close()
			})
			service.mu.Lock()
			service.registrationTCP = registration
			service.registrationTCPProtected = true
			service.mu.Unlock()
			now := time.Now()
			service.recordPortSOpened(push, now)
			if !service.trackProtectedConnection(push) {
				t.Fatal("track port-s")
			}
			service.recordPortSClosed(push, test.closeError, now)
			service.untrackProtectedConnection(push)
			got := service.SMSReadiness()
			if got.Ready || got.HealthReady != test.wantReady {
				t.Fatalf("readiness = %+v, want health ready %t", got, test.wantReady)
			}
		})
	}
}

func TestProtectedSMSReadyCallbackWaitsForPortSFlow(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	service.regStatus.Store(registrationRegistered)
	registration, registrationPeer := net.Pipe()
	push, pushPeer := net.Pipe()
	t.Cleanup(func() {
		_ = registrationPeer.Close()
		_ = pushPeer.Close()
	})
	service.mu.Lock()
	service.registrationTCP = registration
	service.registrationTCPProtected = true
	service.mu.Unlock()

	called := 0
	service.SetOnSMSReady(func() { called++ })
	if called != 0 {
		t.Fatalf("callback before port-s = %d", called)
	}
	if !service.trackProtectedConnection(push) {
		t.Fatal("track port-s")
	}
	if called != 1 {
		t.Fatalf("callback after port-s = %d, want 1", called)
	}
}

func TestEvaluateSMSReadinessRequiresEveryPrerequisite(t *testing.T) {
	tests := []struct {
		name       string
		registered bool
		profile    bool
		transport  bool
		receiver   bool
		smsc       string
		ready      bool
		reason     string
	}{
		{name: "registration", profile: true, transport: true, receiver: true, smsc: "+123", reason: smsReadyReasonNotRegistered},
		{name: "profile", registered: true, transport: true, receiver: true, smsc: "+123", reason: smsReadyReasonProfileNotReady},
		{name: "transport", registered: true, profile: true, receiver: true, smsc: "+123", reason: smsReadyReasonTransportNotReady},
		{name: "receiver", registered: true, profile: true, transport: true, smsc: "+123", reason: smsReadyReasonReceiverNotReady},
		{name: "smsc", registered: true, profile: true, transport: true, receiver: true, ready: true, reason: smsReadyReasonSMSCNotConfigured},
		{name: "ready", registered: true, profile: true, transport: true, receiver: true, smsc: "+123", ready: true, reason: smsReadyReasonReady},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := evaluateSMSReadiness(test.registered, test.profile, test.transport, test.receiver, test.smsc)
			if got.Ready != test.ready || got.Reason != test.reason {
				t.Fatalf("readiness = %+v", got)
			}
		})
	}
}

func TestSMSReadinessRequiresNegotiatedIdentityAndContact(t *testing.T) {
	service, err := New(&IMSConfig{SMSC: "+123"})
	if err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	service.regState = regRegistered
	service.smsReceiverReady = true
	service.regSession = &registerSession{contactUser: "binding"}
	service.mu.Unlock()
	if got := service.SMSReadiness(); got.Ready || got.Reason != smsReadyReasonProfileNotReady {
		t.Fatalf("readiness without associated identity = %+v", got)
	}
	service.mu.Lock()
	service.regSession.publicID = "sip:+15551234567@ims.example"
	service.externalTransport = true
	service.mu.Unlock()
	if got := service.SMSReadiness(); !got.Ready || !got.ProfileReady {
		t.Fatalf("readiness with registered profile = %+v", got)
	}
}

func TestSMSReadinessObserverReceivesCurrentAndChangedState(t *testing.T) {
	service, err := New(&IMSConfig{SMSC: "+123"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var states []SMSReadiness
	service.SetOnSMSReadinessChanged(func(state SMSReadiness) {
		states = append(states, state)
	})
	service.setSMSReceiverReady(true)
	if len(states) != 2 {
		t.Fatalf("observer states = %d, want 2", len(states))
	}
	if states[0].ReceiverReady || !states[1].ReceiverReady {
		t.Fatalf("observer states = %+v", states)
	}
}

func TestPrepareSendEnvironmentReturnsTypedNotReadyError(t *testing.T) {
	service, err := New(&IMSConfig{SMSC: "+123"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.prepareSendEnv(nil, "+15551234567", "test", SendOptions{})
	if !errors.Is(err, smsdelivery.ErrSMSNotReady) {
		t.Fatalf("prepareSendEnv() error = %v, want ErrSMSNotReady", err)
	}
}

func TestProtectedSMSReadinessTrustsRegisteredOutboundFlow(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	registration, registrationPeer := net.Pipe()
	t.Cleanup(func() { _ = registrationPeer.Close() })
	service.mu.Lock()
	service.registrationTCP = registration
	service.registrationTCPProtected = true
	service.sipOutbound = true
	service.outboundContactRegistered = true
	service.mu.Unlock()

	if got := service.SMSReadiness(); !got.ReceiverReady || !got.Ready {
		t.Fatalf("readiness over a registered outbound flow = %+v", got)
	}
	service.mu.Lock()
	service.outboundContactRegistered = false
	service.mu.Unlock()
	if got := service.SMSReadiness(); got.ReceiverReady {
		t.Fatalf("readiness without a registered outbound flow = %+v", got)
	}
}
