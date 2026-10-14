package phone

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
)

type uncertainCallGateway struct{ *fakeVoiceGateway }

func (g uncertainCallGateway) AnswerIncomingCall(_ context.Context, req voicehost.AnswerRequest) (voicehost.AnswerResult, error) {
	g.emitEvent(voicehost.CallEvent{Type: "CallAnswered", DeviceID: req.DeviceID, CallID: req.CallID, Time: time.Now()})
	return voicehost.AnswerResult{}, context.DeadlineExceeded
}

func (g uncertainCallGateway) BeginCall(ctx context.Context, req voicehost.BeginCallRequest) (voicehost.CallSnapshot, error) {
	snapshot, _ := g.fakeVoiceGateway.BeginCall(ctx, req)
	return snapshot, context.DeadlineExceeded
}

func TestRecoveredAnswerStartsRecording(t *testing.T) {
	gateway := newFakeVoiceGateway()
	gateway.captureError = errors.ErrUnsupported
	service := newPhoneTestService(t, gateway, newMemoryCallStore(), time.Second)
	service.gateway = uncertainCallGateway{gateway}
	service.recordingDir = t.TempDir()
	gateway.emitIncoming(voicehost.IncomingCall{DeviceID: "dev-1", CallID: "incoming", Caller: "10010", OfferSDP: testPlainSDP})
	gateway.activeSnapshots["dev-1"] = voicehost.CallSnapshot{DeviceID: "dev-1", CallID: "incoming", State: "connected", ClientSDP: testPlainSDP}
	addStubMedia(t, service, "first", "admin", "first-lease")
	_, err := service.Answer(context.Background(), ControlRequest{Owner: "admin", CallID: "incoming", MediaID: "first", Lease: "first-lease"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("answer error = %v", err)
	}
	if active := service.Active(""); len(active) != 1 || !active[0].ReadOnly {
		t.Fatalf("unclaimed connected call must offer explicit takeover: %+v", active)
	}
	addStubMedia(t, service, "recovered", "admin", "recovered-lease")
	_, _, err = service.RefreshMedia(RefreshRequest{Owner: "admin", CallID: "incoming", MediaID: "recovered", Takeover: true})
	if err != nil {
		t.Fatal(err)
	}
	service.mu.RLock()
	attempted, recorder := service.calls["incoming"].mixedAttempted, service.calls["incoming"].mixedRecorder
	service.mu.RUnlock()
	if !attempted || recorder == nil {
		t.Fatal("recovered call has no recording")
	}
	addStubMedia(t, service, "next", "admin", "next-lease")
	_, _, err = service.RefreshMedia(RefreshRequest{Owner: "admin", CallID: "incoming", MediaID: "next", Lease: "recovered-lease"})
	if err != nil {
		t.Fatal(err)
	}
	service.mu.RLock()
	reused := service.calls["incoming"].mixedRecorder == recorder
	service.mu.RUnlock()
	if !reused {
		t.Fatal("second media recovery replaced the existing recording")
	}
}

func TestFailedDialWithSnapshotRemainsControllable(t *testing.T) {
	gateway := newFakeVoiceGateway()
	service := newPhoneTestService(t, gateway, newMemoryCallStore(), time.Second)
	service.gateway = uncertainCallGateway{gateway}
	addStubMedia(t, service, "media", "admin", "lease")
	view, err := service.StartCall(StartCallRequest{DeviceID: "dev-1", Callee: "10010", Owner: "admin", MediaID: "media", Lease: "lease"})
	if !errors.Is(err, context.DeadlineExceeded) || view.CallID == "" {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	if err := service.Hangup(context.Background(), "admin", view.CallID, "lease"); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-gateway.hangupCalls:
		if id != view.CallID {
			t.Fatal("hung up another call")
		}
	default:
		t.Fatal("retained call was not sent to backend for hangup")
	}
}
