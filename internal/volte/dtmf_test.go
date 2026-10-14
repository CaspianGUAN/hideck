package volte

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
)

func TestNativeDTMFLTEUsesContinuousTone(t *testing.T) {
	ctl, host := dtmfController(t, qmi.VoiceCallModeLTE)
	if err := ctl.SendCallDTMF("wwan1", "call-1", "5"); err != nil {
		t.Fatal(err)
	}
	want := []string{"start:7:5", "stop:7"}
	assertDTMFActions(t, host, want)
}

func TestNativeDTMFCDMAUsesBurst(t *testing.T) {
	ctl, host := dtmfController(t, qmi.VoiceCallModeCDMA)
	if err := ctl.SendCallDTMF("wwan1", "call-1", "#"); err != nil {
		t.Fatal(err)
	}
	assertDTMFActions(t, host, []string{"burst:7:#"})
}

func TestNativeDTMFUnknownModeUsesContinuousWhenSupported(t *testing.T) {
	ctl, host := dtmfController(t, qmi.VoiceCallModeUnknown)
	if err := ctl.SendCallDTMF("wwan1", "call-1", "6"); err != nil {
		t.Fatal(err)
	}
	assertDTMFActions(t, host, []string{"start:7:6", "stop:7"})
}

func TestNativeDTMFUnknownModeFallsBackOnlyWhenUnsupported(t *testing.T) {
	codes := []uint16{
		qmi.QMIErrOpNetworkUnsupported,
		qmi.QMIErrOpDeviceUnsupported,
		qmi.QMIErrInvalidQmiCmd,
		qmi.QMIErrNotSupported,
	}
	for _, code := range codes {
		t.Run(qmiError(qmi.VOICEStartContinuousDTMF, code).Error(), func(t *testing.T) {
			ctl, host := dtmfController(t, qmi.VoiceCallModeUnknown)
			host.startDTMFErr = qmiError(qmi.VOICEStartContinuousDTMF, code)
			if err := ctl.SendCallDTMF("wwan1", "call-1", "9"); err != nil {
				t.Fatal(err)
			}
			assertDTMFActions(t, host, []string{"start:7:9", "burst:7:9"})
		})
	}
}

func TestNativeDTMFKnown3GPPModesDoNotFallBackToBurst(t *testing.T) {
	modes := []qmi.VoiceCallMode{
		qmi.VoiceCallModeGSM,
		qmi.VoiceCallModeUMTS,
		qmi.VoiceCallModeLTE,
		qmi.VoiceCallModeTDS,
		qmi.VoiceCallModeWLAN,
	}
	for _, mode := range modes {
		t.Run(fmt.Sprintf("mode_%d", mode), func(t *testing.T) {
			ctl, host := dtmfController(t, mode)
			host.startDTMFErr = qmiError(qmi.VOICEStartContinuousDTMF, qmi.QMIErrOpNetworkUnsupported)
			err := ctl.SendCallDTMF("wwan1", "call-1", "2")
			if err == nil || !strings.Contains(err.Error(), "start continuous DTMF") {
				t.Fatalf("error = %v", err)
			}
			assertDTMFActions(t, host, []string{"start:7:2"})
		})
	}
}

func TestNativeDTMFTransportFailureDoesNotFallBack(t *testing.T) {
	ctl, host := dtmfController(t, qmi.VoiceCallModeUnknown)
	host.startDTMFErr = errors.New("QMI transport closed")
	if err := ctl.SendCallDTMF("wwan1", "call-1", "3"); err == nil {
		t.Fatal("transport error was hidden")
	}
	assertDTMFActions(t, host, []string{"start:7:3"})
}

func TestNativeDTMFFallbackFailureKeepsBothErrors(t *testing.T) {
	ctl, host := dtmfController(t, qmi.VoiceCallModeUnknown)
	startErr := qmiError(qmi.VOICEStartContinuousDTMF, qmi.QMIErrNotSupported)
	burstErr := errors.New("burst failed")
	host.startDTMFErr = startErr
	host.burstDTMFErr = burstErr
	err := ctl.SendCallDTMF("wwan1", "call-1", "8")
	if !errors.Is(err, startErr) || !errors.Is(err, burstErr) {
		t.Fatalf("fallback error = %v", err)
	}
	assertDTMFActions(t, host, []string{"start:7:8", "burst:7:8"})
}

func TestNativeDTMFStopFailureIsReturnedWithoutFallback(t *testing.T) {
	ctl, host := dtmfController(t, qmi.VoiceCallModeUnknown)
	host.stopDTMFErr = errors.New("stop failed")
	err := ctl.SendCallDTMF("wwan1", "call-1", "4")
	if err == nil || !strings.Contains(err.Error(), "stop continuous DTMF") {
		t.Fatalf("error = %v", err)
	}
	assertDTMFActions(t, host, []string{"start:7:4", "stop:7"})
}

func TestNativeDTMFSerializesConcurrentKeys(t *testing.T) {
	ctl, host := dtmfController(t, qmi.VoiceCallModeLTE)
	var wg sync.WaitGroup
	for _, digit := range []string{"1", "2"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ctl.SendCallDTMF("wwan1", "call-1", digit); err != nil {
				t.Errorf("SendCallDTMF(%s): %v", digit, err)
			}
		}()
	}
	wg.Wait()
	actions := dtmfActions(host)
	if len(actions) != 4 || !strings.HasPrefix(actions[0], "start:") ||
		!strings.HasPrefix(actions[1], "stop:") || !strings.HasPrefix(actions[2], "start:") ||
		!strings.HasPrefix(actions[3], "stop:") {
		t.Fatalf("interleaved DTMF actions: %v", actions)
	}
}

func TestNativeDTMFRejectsInvalidKeyBeforeQMI(t *testing.T) {
	ctl, host := dtmfController(t, qmi.VoiceCallModeLTE)
	if err := ctl.SendCallDTMF("wwan1", "call-1", "12"); err == nil {
		t.Fatal("invalid DTMF accepted")
	}
	assertDTMFActions(t, host, nil)
}

func TestVoiceStatusPersistsCallModeForDTMF(t *testing.T) {
	ctl, host := enableVoice(t)
	host.fireVoice(&qmi.VoiceAllCallInfo{Calls: []qmi.VoiceCallInfo{{
		ID: 7, State: qmiCallConversation, Direction: qmiDirMO, Mode: qmi.VoiceCallModeLTE,
	}}})
	call, ok := ctl.lookupActive("wwan1")
	if !ok || call.Mode != qmi.VoiceCallModeLTE {
		t.Fatalf("call mode = %v, found=%v", call.Mode, ok)
	}
}

func TestBeginCallPreservesModeFromSynchronousIndication(t *testing.T) {
	ctl, host := enableVoice(t)
	host.dialVoice = &qmi.VoiceAllCallInfo{Calls: []qmi.VoiceCallInfo{{
		ID: 1, State: qmiCallOriginating, Direction: qmiDirMO, Mode: qmi.VoiceCallModeLTE,
	}}}
	snapshot, err := ctl.BeginCall(context.Background(), voicehost.BeginCallRequest{DeviceID: "wwan1", Callee: "10000"})
	if err != nil {
		t.Fatal(err)
	}
	call, ok := ctl.lookup("wwan1", snapshot.CallID)
	if !ok || call.Mode != qmi.VoiceCallModeLTE {
		t.Fatalf("call mode = %v, found=%v", call.Mode, ok)
	}
}

func dtmfController(t *testing.T, mode qmi.VoiceCallMode) (*Controller, *FakeModem) {
	t.Helper()
	host := newFakeModem()
	ctl := NewController(host)
	ctl.storeCall("wwan1", nativeCall{ID: "call-1", QMI: 7, Mode: mode, State: "connected"})
	return ctl, host
}

func qmiError(messageID, code uint16) error {
	return &qmi.QMIError{Service: qmi.ServiceVOICE, MessageID: messageID, Result: 1, ErrorCode: code}
}

func assertDTMFActions(t *testing.T, host *FakeModem, want []string) {
	t.Helper()
	got := dtmfActions(host)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("DTMF actions = %v, want %v", got, want)
	}
}

func dtmfActions(host *FakeModem) []string {
	host.mu.Lock()
	defer host.mu.Unlock()
	return append([]string(nil), host.dtmfActions...)
}
