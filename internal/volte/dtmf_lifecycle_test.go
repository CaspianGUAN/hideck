package volte

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
)

type switchingDTMFHost struct {
	*FakeModem
	ownerMu sync.Mutex
	owner   *FakeModem
	started chan struct{}
	release chan struct{}
}

func (h *switchingDTMFHost) BindVoiceDTMF(string) (DTMFTransport, error) {
	h.ownerMu.Lock()
	defer h.ownerMu.Unlock()
	return pausedDTMF{fakeBoundDTMF{h.owner}, h.started, h.release}, nil
}

type pausedDTMF struct {
	fakeBoundDTMF
	started chan struct{}
	release chan struct{}
}

func (p pausedDTMF) VOICEStartContinuousDTMF(ctx context.Context, id, digit uint8) (uint8, error) {
	result, err := p.fakeBoundDTMF.VOICEStartContinuousDTMF(ctx, id, digit)
	close(p.started)
	<-p.release
	return result, err
}

func pausedDTMFController(t *testing.T, mode qmi.VoiceCallMode) (*Controller, *switchingDTMFHost, func()) {
	t.Helper()
	fake := newFakeModem()
	h := &switchingDTMFHost{FakeModem: fake, owner: fake, started: make(chan struct{}), release: make(chan struct{})}
	c := NewController(h)
	c.storeCall("wwan1", nativeCall{ID: "call-1", QMI: 7, Mode: mode, State: "connected"})
	var once sync.Once
	release := func() { once.Do(func() { close(h.release) }) }
	t.Cleanup(release)
	return c, h, release
}

func TestNativeDTMFUsesOriginalOwnerAfterReplacement(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "continuous", true: "burst_fallback"}[fallback], func(t *testing.T) {
			c, h, release := pausedDTMFController(t, qmi.VoiceCallModeUnknown)
			if fallback {
				h.startDTMFErr = qmiError(qmi.VOICEStartContinuousDTMF, qmi.QMIErrNotSupported)
			}
			done := make(chan error, 1)
			go func() { done <- c.SendCallDTMF("wwan1", "call-1", "5") }()
			<-h.started
			replacement := newFakeModem()
			h.ownerMu.Lock()
			h.owner = replacement
			h.ownerMu.Unlock()
			release()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			last := "stop:7"
			if fallback {
				last = "burst:7:5"
			}
			assertDTMFActions(t, h.FakeModem, []string{"start:7:5", last})
			assertDTMFActions(t, replacement, nil)
		})
	}
}

func TestNativeDTMFRetireDrainsStartedTone(t *testing.T) {
	c, h, release := pausedDTMFController(t, qmi.VoiceCallModeLTE)
	done := make(chan error, 1)
	go func() { done <- c.SendCallDTMF("wwan1", "call-1", "5") }()
	<-h.started
	retired := make(chan error, 1)
	go func() { retired <- c.RetireDevice("wwan1") }()
	select {
	case err := <-retired:
		t.Fatalf("retirement bypassed active DTMF: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-retired; err != nil {
		t.Fatal(err)
	}
	assertDTMFActions(t, h.FakeModem, []string{"start:7:5", "stop:7"})
	if c.sessionVoice("wwan1") != nil {
		t.Fatal("retired session retained")
	}
}

func TestNativeDTMFDoesNotStopReusedCallID(t *testing.T) {
	c, h, release := pausedDTMFController(t, qmi.VoiceCallModeLTE)
	done := make(chan error, 1)
	go func() { done <- c.SendCallDTMF("wwan1", "call-1", "5") }()
	<-h.started
	// The old call may still be in the local table while the QMI slot has
	// already been assigned to another call.
	c.storeCall("wwan1", nativeCall{ID: "call-2", QMI: 7, Mode: qmi.VoiceCallModeLTE, State: "connected"})
	release()
	if err := <-done; err == nil || !strings.Contains(err.Error(), "no longer current") {
		t.Fatalf("stale call error = %v", err)
	}
	assertDTMFActions(t, h.FakeModem, []string{"start:7:5"})
}
