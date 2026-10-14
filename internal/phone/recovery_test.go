package phone

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func TestDisconnectedMediaHangsUpAfterGraceAndCanRecover(t *testing.T) {
	gateway, store := newFakeVoiceGateway(), newMemoryCallStore()
	service := newPhoneTestService(t, gateway, store, 30*time.Millisecond)
	addActiveCallForRecovery(service, "call-recover", "media-recover")
	service.handleMediaState("media-recover", webrtc.PeerConnectionStateDisconnected)
	service.handleMediaState("media-recover", webrtc.PeerConnectionStateConnected)
	select {
	case callID := <-gateway.hangupCalls:
		t.Fatalf("recovered media unexpectedly hung up %s", callID)
	case <-time.After(50 * time.Millisecond):
	}

	service.handleMediaState("media-recover", webrtc.PeerConnectionStateDisconnected)
	select {
	case callID := <-gateway.hangupCalls:
		if callID != "call-recover" {
			t.Fatalf("hung up call = %q", callID)
		}
	case <-time.After(time.Second):
		t.Fatal("disconnected media was not hung up after grace")
	}
}

func TestMediaFailureBeforeBindingHangsUpAfterBinding(t *testing.T) {
	gateway, store := newFakeVoiceGateway(), newMemoryCallStore()
	service := newPhoneTestService(t, gateway, store, 30*time.Millisecond)
	addActiveCallForRecovery(service, "call-late-media", "")
	addStubMedia(t, service, "media-late", "admin", "lease-late")

	service.handleMediaState("media-late", webrtc.PeerConnectionStateFailed)
	_, reservation, err := service.reserveControlledMedia("admin", "media-late", "lease-late")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.assignControl("call-late-media", reservation); err != nil {
		t.Fatal(err)
	}

	select {
	case callID := <-gateway.hangupCalls:
		if callID != "call-late-media" {
			t.Fatalf("hung up call = %q", callID)
		}
	case <-time.After(time.Second):
		t.Fatal("pending media failure did not enter the disconnect hangup path")
	}
}

func TestFailedUnboundMediaIsReleasedAfterGrace(t *testing.T) {
	service := newPhoneTestService(t, newFakeVoiceGateway(), newMemoryCallStore(), 20*time.Millisecond)
	addStubMedia(t, service, "media-unbound", "admin", "lease-unbound")

	service.handleMediaState("media-unbound", webrtc.PeerConnectionStateFailed)
	deadline := time.Now().Add(time.Second)
	for service.media.Get("media-unbound") != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if service.media.Get("media-unbound") != nil {
		t.Fatal("failed unbound media was not released after grace")
	}
}

func TestRecoveredUnboundMediaIsNotReleased(t *testing.T) {
	service := newPhoneTestService(t, newFakeVoiceGateway(), newMemoryCallStore(), 20*time.Millisecond)
	addStubMedia(t, service, "media-recovered", "admin", "lease-recovered")

	service.handleMediaState("media-recovered", webrtc.PeerConnectionStateDisconnected)
	service.handleMediaState("media-recovered", webrtc.PeerConnectionStateConnected)
	time.Sleep(40 * time.Millisecond)
	if service.media.Get("media-recovered") == nil {
		t.Fatal("recovered unbound media was released")
	}
}

func TestCancelMediaRequiresOwnershipAndRejectsBoundSession(t *testing.T) {
	service := newPhoneTestService(t, newFakeVoiceGateway(), newMemoryCallStore(), time.Second)
	addStubMedia(t, service, "media-cancel", "admin", "lease-cancel")
	if err := service.CancelMedia("admin", "media-cancel", "wrong"); err == nil {
		t.Fatal("foreign lease unexpectedly canceled media")
	}
	if err := service.CancelMedia("admin", "media-cancel", "lease-cancel"); err != nil {
		t.Fatalf("cancel unbound media: %v", err)
	}
	if service.media.Get("media-cancel") != nil {
		t.Fatal("canceled media remains registered")
	}

	addStubMedia(t, service, "media-bound", "admin", "lease-bound")
	addActiveCallForRecovery(service, "call-bound", "media-bound")
	if err := service.CancelMedia("admin", "media-bound", "lease-bound"); err == nil {
		t.Fatal("bound media was canceled")
	}
	if service.media.Get("media-bound") == nil {
		t.Fatal("bound media was removed after rejected cancellation")
	}
}

func TestCallSetupReservesMediaAgainstCancelAndTimedCleanup(t *testing.T) {
	gateway := newFakeVoiceGateway()
	gateway.beginStarted = make(chan struct{}, 1)
	gateway.beginRelease = make(chan struct{})
	service := newPhoneTestService(t, gateway, newMemoryCallStore(), 20*time.Millisecond)
	addStubMedia(t, service, "media-setup", "admin", "lease-setup")

	result := make(chan error, 1)
	go func() {
		_, err := service.StartCall(StartCallRequest{
			Owner: "admin", DeviceID: "dev-1", Callee: "10010",
			MediaID: "media-setup", Lease: "lease-setup",
		})
		result <- err
	}()
	select {
	case <-gateway.beginStarted:
	case <-time.After(time.Second):
		t.Fatal("call setup did not reach the gateway")
	}
	if err := service.CancelMedia("admin", "media-setup", "lease-setup"); err == nil || !strings.Contains(err.Error(), "active call operation") {
		t.Fatalf("cancel in-progress media error = %v", err)
	}
	service.handleMediaState("media-setup", webrtc.PeerConnectionStateFailed)
	time.Sleep(40 * time.Millisecond)
	if service.media.Get("media-setup") == nil {
		t.Fatal("reserved media was removed before call binding completed")
	}
	close(gateway.beginRelease)
	if err := <-result; err != nil {
		t.Fatalf("start call: %v", err)
	}
	select {
	case callID := <-gateway.hangupCalls:
		if callID != "outbound-1" {
			t.Fatalf("hung up call = %q", callID)
		}
	case <-time.After(time.Second):
		t.Fatal("failed reserved media did not enter bound-call recovery")
	}
}

func TestServiceCloseHangsUpActiveCallAndUnsubscribes(t *testing.T) {
	gateway, store := newFakeVoiceGateway(), newMemoryCallStore()
	service, err := NewService(ServiceOptions{
		Gateway: gateway, Store: store, WebRTCUDPAddress: "127.0.0.1:0", RecoveryGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	addActiveCallForRecovery(service, "call-close", "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if callID := <-gateway.hangupCalls; callID != "call-close" {
		t.Fatalf("closed call = %q", callID)
	}
	gateway.mu.Lock()
	unsubscribed := gateway.unsubscribed
	gateway.mu.Unlock()
	if unsubscribed != 2 {
		t.Fatalf("unsubscribe count = %d, want 2", unsubscribed)
	}
}

func addActiveCallForRecovery(service *Service, callID, mediaID string) {
	call := &activeCall{
		view:   CallView{CallID: callID, DeviceID: "dev-1", Status: StatusConnected, MediaID: mediaID},
		record: CallRecord{CallID: callID, DeviceID: "dev-1", Status: StatusConnected, StartedAt: time.Now()},
		owner:  "admin", lease: "lease-1", mediaID: mediaID,
		terminalDone: make(chan struct{}), finalizedDone: make(chan struct{}),
	}
	service.mu.Lock()
	service.calls[callID] = call
	service.deviceCalls[call.view.DeviceID] = callID
	if mediaID != "" {
		service.mediaCalls[mediaID] = callID
	}
	service.mu.Unlock()
}
