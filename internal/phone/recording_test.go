package phone

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
	modemhost "github.com/yibaiba/hideck/internal/modemvoice/host"
)

func TestIncomingCaptureCapabilityDoesNotMaskRealRecordingFailures(t *testing.T) {
	unsupported := (&modemhost.Controller{}).StartCallCapture("dev-1", "incoming", "")
	if !errors.Is(unsupported, errors.ErrUnsupported) {
		t.Fatal(unsupported)
	}
	for _, failure := range []error{unsupported, errors.New("capture I/O failed")} {
		t.Run(failure.Error(), func(t *testing.T) {
			gateway, store := newFakeVoiceGateway(), newMemoryCallStore()
			gateway.captureError = failure
			service := newPhoneTestService(t, gateway, store, time.Second)
			service.transcoder = &recordingTranscoder{}
			service.handleIncoming(voicehost.IncomingCall{DeviceID: "dev-1", CallID: "incoming", Caller: "10010"})
			call := service.calls["incoming"]
			call.mixedAttempted, call.mixedAudioPath = true, filepath.Join(t.TempDir(), "mixed.wav")
			call.view.Status = StatusConnected
			service.finishCall(voicehost.CallEvent{Type: "CallEnded", CallID: "incoming"})
			service.finalizeRecording(voicehost.CallEvent{Type: "CallFinalized", CallID: "incoming"})
			record := store.record("incoming")
			wantError := !errors.Is(failure, errors.ErrUnsupported)
			if record.RecordingName != "mixed.mp3" || (record.RecordingError != "") != wantError {
				t.Fatalf("recording result: %+v", record)
			}
		})
	}
}

func TestFinalizeRecordingPublishesMixedMP3AndPCAPMetadata(t *testing.T) {
	gateway, store := newFakeVoiceGateway(), newMemoryCallStore()
	service := newPhoneTestService(t, gateway, store, time.Second)
	transcoder := &recordingTranscoder{}
	service.transcoder = transcoder
	call := recordingTestCall(service, "recording-1")
	call.mixedAttempted = true
	call.mixedAudioPath = filepath.Join(t.TempDir(), "call_dev_mixed.wav")
	service.finalizeRecording(voicehost.CallEvent{
		Type: "CallFinalized", CallID: call.view.CallID,
		PCAPPath: filepath.Join(t.TempDir(), "call_dev.pcap"), AudioCodec: "PCMU",
	})
	record := store.record(call.view.CallID)
	if transcoder.input != call.mixedAudioPath || record.RecordingName != "call_dev_mixed.mp3" ||
		record.PCAPName != "call_dev.pcap" || record.Codec != "PCMU" || record.RecordingError != "" {
		t.Fatalf("transcoder input=%q record=%+v", transcoder.input, record)
	}
}

func TestFinalizeRecordingKeepsFailureSeparateFromCompletedCall(t *testing.T) {
	gateway, store := newFakeVoiceGateway(), newMemoryCallStore()
	service := newPhoneTestService(t, gateway, store, time.Second)
	service.transcoder = &recordingTranscoder{err: errors.New("encoder failed")}
	call := recordingTestCall(service, "recording-failed-1")
	call.mixedAttempted = true
	call.mixedAudioPath = filepath.Join(t.TempDir(), "call_failed_mixed.wav")
	call.record.RecordingError = "capture warning"
	service.finalizeRecording(voicehost.CallEvent{
		Type: "CallFinalized", CallID: call.view.CallID, RecordingError: "capture warning",
	})
	record := store.record(call.view.CallID)
	if record.Status != StatusCompleted || record.RecordingName != "" ||
		!strings.Contains(record.RecordingError, "capture warning") ||
		!strings.Contains(record.RecordingError, "encoder failed") {
		t.Fatalf("record = %+v", record)
	}
	backlog, _, cancel := service.Subscribe(0)
	defer cancel()
	if len(backlog) == 0 || backlog[len(backlog)-1].Type != "recording_failed" {
		t.Fatalf("events = %+v", backlog)
	}
}

type recordingTranscoder struct {
	input string
	err   error
}

func TestFinalizeRecordingWaitsForTerminalCleanup(t *testing.T) {
	service := newPhoneTestService(t, newFakeVoiceGateway(), newMemoryCallStore(), time.Second)
	call := recordingTestCall(service, "concurrent-finish")
	call.terminalDone = make(chan struct{})
	done := make(chan struct{})
	go func() {
		service.finalizeRecording(voicehost.CallEvent{Type: "CallFinalized", CallID: call.view.CallID})
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("recording finalized before terminal cleanup")
	case <-time.After(20 * time.Millisecond):
	}
	close(call.terminalDone)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("recording did not finalize after terminal cleanup")
	}
}

func (transcoder *recordingTranscoder) ToMP3(_ context.Context, input string) (string, error) {
	transcoder.input = input
	if transcoder.err != nil {
		return "", transcoder.err
	}
	return strings.TrimSuffix(input, filepath.Ext(input)) + ".mp3", nil
}

func recordingTestCall(service *Service, callID string) *activeCall {
	now := time.Now()
	call := &activeCall{
		view: CallView{CallID: callID, DeviceID: "dev-1", Status: StatusCompleted, StartedAt: now},
		record: CallRecord{
			CallID: callID, DeviceID: "dev-1", Status: StatusCompleted, StartedAt: now,
		},
		terminal: true, terminalDone: make(chan struct{}), finalizedDone: make(chan struct{}),
	}
	close(call.terminalDone)
	service.mu.Lock()
	service.calls[callID] = call
	service.mu.Unlock()
	return call
}
