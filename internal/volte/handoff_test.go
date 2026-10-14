package volte

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
)

type handoffHost struct {
	*FakeModem
	queryErr, releaseErr error
	afterHangup          *qmi.VoiceAllCallInfo
	hangups              int
	hangupStarted        chan struct{}
	finishHangup         chan struct{}
}

func (h *handoffHost) VOICEGetAllCallInfo(ctx context.Context, id string) (*qmi.VoiceAllCallInfo, error) {
	if h.queryErr != nil {
		return nil, h.queryErr
	}
	return h.FakeModem.VOICEGetAllCallInfo(ctx, id)
}

func (h *handoffHost) VOICEHangup(ctx context.Context, id string, call uint8) error {
	h.hangups++
	if h.hangupStarted != nil {
		close(h.hangupStarted)
		<-h.finishHangup
	}
	if err := h.FakeModem.VOICEHangup(ctx, id, call); err != nil {
		return err
	}
	if h.afterHangup != nil {
		h.mu.Lock()
		h.allCalls = h.afterHangup
		h.mu.Unlock()
	}
	return nil
}

func (h *handoffHost) ReleaseIMSClients(id string) error {
	if h.releaseErr != nil {
		return h.releaseErr
	}
	return h.FakeModem.ReleaseIMSClients(id)
}

func newHandoffTest(t *testing.T) (*Controller, *handoffHost) {
	t.Helper()
	h := &handoffHost{FakeModem: newFakeModem()}
	c := NewControllerWithBackup(h, t.TempDir())
	if err := c.Enable(context.Background(), "wwan1"); err != nil {
		t.Fatal(err)
	}
	h.allCalls = &qmi.VoiceAllCallInfo{Calls: []qmi.VoiceCallInfo{{ID: 1, State: qmiCallConversation, Direction: qmiDirMO}}}
	c.storeCall("wwan1", nativeCall{ID: "old-call", QMI: 1, State: "connected", Start: time.Now()})
	return c, h
}

func TestHandoffFailurePreservesNativeSession(t *testing.T) {
	for _, failure := range []string{"hangup_timeout", "control_lost", "still_active", "disconnecting", "new_call", "query_failure"} {
		t.Run(failure, func(t *testing.T) {
			c, h := newHandoffTest(t)
			s := c.sess["wwan1"]
			switch failure {
			case "hangup_timeout":
				h.hangupErr = context.DeadlineExceeded
			case "control_lost":
				h.hangupErr = errors.New("QMI service not ready")
			case "new_call":
				h.afterHangup = &qmi.VoiceAllCallInfo{Calls: []qmi.VoiceCallInfo{{ID: 2, State: qmiCallIncoming, Direction: qmiDirMT}}}
			case "disconnecting":
				h.afterHangup = &qmi.VoiceAllCallInfo{Calls: []qmi.VoiceCallInfo{{ID: 1, State: qmiCallDisconnecting, Direction: qmiDirMO}}}
			case "query_failure":
				h.queryErr = context.DeadlineExceeded
			}
			if err := c.DisableForHandoff(context.Background(), "wwan1"); err == nil {
				t.Fatal("unconfirmed hangup allowed backend handoff")
			}
			if c.sess["wwan1"] != s || h.ReleaseCount != 0 || c.ActiveCall("wwan1") == nil {
				t.Fatal("failure discarded native call/session")
			}
			h.hangupErr, h.queryErr = nil, nil
			h.afterHangup = &qmi.VoiceAllCallInfo{}
			if err := c.DisableForHandoff(context.Background(), "wwan1"); err != nil {
				t.Fatal("handoff retry", err)
			}
		})
	}
}

func TestHandoffReleasesMediaAndAllowsNativeReenable(t *testing.T) {
	c, h := newHandoffTest(t)
	m, err := startCallMedia("", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	c.media.put("old-call", m)
	bridge := m.bridge
	h.afterHangup = &qmi.VoiceAllCallInfo{}
	if err := c.DisableForHandoff(context.Background(), "wwan1"); err != nil {
		t.Fatal(err)
	}
	if c.ActiveCall("wwan1") != nil || c.media.get("old-call") != nil || h.ReleaseCount != 1 {
		t.Fatal("handoff retained old call or media")
	}
	select {
	case <-bridge.closed:
	default:
		t.Fatal("audio still open during handoff")
	}
	if _, err := c.BeginCall(context.Background(), voicehost.BeginCallRequest{DeviceID: "wwan1", Callee: "10010"}); err == nil {
		t.Fatal("old VoLTE backend allowed dial after handoff")
	}
	if err := c.Enable(context.Background(), "wwan1"); err != nil {
		t.Fatal("cannot reenable VoLTE after handoff", err)
	}
	if !c.Status("wwan1").Ready() {
		t.Fatal("VoLTE did not recover")
	}
	c.Disable("wwan1")
}

func TestHandoffReleaseFailureCanBeRetried(t *testing.T) {
	c, h := newHandoffTest(t)
	h.afterHangup = &qmi.VoiceAllCallInfo{}
	h.releaseErr = errors.New("release failed")
	if err := c.DisableForHandoff(context.Background(), "wwan1"); !errors.Is(err, h.releaseErr) {
		t.Fatal("release error was hidden", err)
	}
	if c.sess["wwan1"] == nil {
		t.Fatal("failed release discarded session")
	}
	h.releaseErr = nil
	if err := c.DisableForHandoff(context.Background(), "wwan1"); err != nil {
		t.Fatal(err)
	}
	if h.hangups != 1 || c.sess["wwan1"] != nil {
		t.Fatal("retry repeated physical hangup or retained session")
	}
}

func TestDialWaitsForHandoffAndCannotReviveOldBackend(t *testing.T) {
	c, h := newHandoffTest(t)
	h.hangupStarted, h.finishHangup = make(chan struct{}), make(chan struct{})
	h.afterHangup = &qmi.VoiceAllCallInfo{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- c.DisableForHandoff(ctx, "wwan1") }()
	select {
	case <-h.hangupStarted:
	case <-ctx.Done():
		close(h.finishHangup)
		t.Fatal("handoff did not start")
	}
	dialed := make(chan error, 1)
	go func() {
		_, err := c.BeginCall(ctx, voicehost.BeginCallRequest{DeviceID: "wwan1", Callee: "10010"})
		dialed <- err
	}()
	close(h.finishHangup)
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if err := <-dialed; err == nil {
		t.Fatal("concurrent dial revived the old backend during handoff")
	}
	if c.ActiveCall("wwan1") != nil {
		t.Fatal("handoff retained a newly dialed call")
	}
}
