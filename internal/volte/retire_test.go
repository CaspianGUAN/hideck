package volte

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
)

type delayedRetireQueryHost struct {
	*FakeModem
	pause            atomic.Bool
	started, release chan struct{}
}

func (h *delayedRetireQueryHost) VOICEGetAllCallInfo(ctx context.Context, id string) (*qmi.VoiceAllCallInfo, error) {
	if h.pause.CompareAndSwap(true, false) {
		close(h.started)
		<-h.release
		return &qmi.VoiceAllCallInfo{Calls: []qmi.VoiceCallInfo{{ID: 1, State: qmiCallConversation, Direction: qmiDirMO}}}, nil
	}
	return h.FakeModem.VOICEGetAllCallInfo(ctx, id)
}

func TestRetireDeviceRejectsLateCallQuery(t *testing.T) {
	h := &delayedRetireQueryHost{FakeModem: newFakeModem(), started: make(chan struct{}), release: make(chan struct{})}
	c := NewControllerWithBackup(h, t.TempDir())
	if err := c.Enable(context.Background(), "wwan1"); err != nil {
		t.Fatal(err)
	}
	h.pause.Store(true)
	done := make(chan struct{})
	go func() { c.ReconcileCalls(context.Background(), "wwan1"); close(done) }()
	<-h.started
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(h.release) }); <-done }
	t.Cleanup(release)
	if err := c.RetireDevice("wwan1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Enable(context.Background(), "wwan1"); err != nil {
		t.Fatal(err)
	}
	release()
	if c.ActiveCall("wwan1") != nil {
		t.Fatal("late call query populated the replacement session")
	}
}

func TestRetireDeviceReaddSubscribesAndRejectsOldCallbacks(t *testing.T) {
	h := newFakeModem()
	c := NewControllerWithBackup(h, t.TempDir())
	const id = "wwan1"
	if err := c.Enable(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	old := c.sess[id]
	h.mu.Lock()
	oldReg, oldSvc, oldVoice := h.imsRegHandler, h.imsSvcHandler, h.voiceHandler
	h.imsRegHandler, h.imsSvcHandler, h.voiceHandler = nil, nil, nil
	h.mu.Unlock()
	if err := c.RetireDevice(id); err != nil {
		t.Fatal(err)
	}
	if c.Status(id).Phase != PhaseIdle {
		t.Fatal("retired state retained")
	}
	if err := c.Enable(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	reg, svc, voice := h.imsRegHandler, h.imsSvcHandler, h.voiceHandler
	h.mu.Unlock()
	if reg == nil || svc == nil || voice == nil || c.sess[id] == old {
		t.Fatal("replacement did not subscribe with a fresh session")
	}
	before := c.Status(id)
	oldReg(&qmi.IMSARegistrationStatus{})
	oldSvc(&qmi.IMSAServicesStatus{HasVoiceServiceStatus: true})
	info := &qmi.VoiceAllCallInfo{Calls: []qmi.VoiceCallInfo{{ID: 1, State: qmiCallConversation, Direction: qmiDirMO}}}
	oldVoice(info)
	if c.Status(id) != before || c.ActiveCall(id) != nil {
		t.Fatal("old callback changed replacement state")
	}
	voice(info)
	if c.ActiveCall(id) == nil {
		t.Fatal("replacement voice callback was not accepted")
	}
	if err := c.RetireDevice(id); err != nil {
		t.Fatal(err)
	}
}

func TestRetireDeviceClosesAllCallMediaWithoutHardwareIO(t *testing.T) {
	c, h := newHandoffTest(t)
	c.storeCall("wwan1", nativeCall{ID: "second-call", QMI: 2, State: "held"})
	for _, id := range []string{"old-call", "second-call"} {
		m, err := startCallMedia("", nil, true)
		if err != nil {
			t.Fatal(err)
		}
		c.media.put(id, m)
		bridge := m.bridge
		t.Cleanup(func() {
			select {
			case <-bridge.closed:
			default:
				t.Error("retired audio still open", id)
				_ = m.Close()
			}
		})
	}
	if err := c.RetireDevice("wwan1"); err != nil {
		t.Fatal(err)
	}
	if c.ActiveCall("wwan1") != nil || c.media.get("old-call") != nil || c.media.get("second-call") != nil {
		t.Fatal("retired call or media retained")
	}
	if h.hangups != 0 || h.ReleaseCount != 0 {
		t.Fatal("local retirement performed hardware I/O")
	}
}

func TestRetireDeviceDrainsInFlightVoiceCallback(t *testing.T) {
	c, h := enableVoice(t)
	started, release := make(chan struct{}), make(chan struct{})
	var callID string
	c.SubscribeCallEvents(func(event voicehost.CallEvent) {
		callID = event.CallID
		close(started)
		<-release
	})
	callbackDone := make(chan struct{})
	go func() {
		h.voiceHandler(&qmi.VoiceAllCallInfo{Calls: []qmi.VoiceCallInfo{{ID: 1, State: qmiCallIncoming, Direction: qmiDirMT}}})
		close(callbackDone)
	}()
	<-started
	retired := make(chan error, 1)
	go func() { retired <- c.RetireDevice("wwan1") }()
	select {
	case err := <-retired:
		close(release)
		<-callbackDone
		t.Fatalf("retirement did not wait for callback: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-callbackDone
	if err := <-retired; err != nil {
		t.Fatal(err)
	}
	if c.media.get(callID) != nil || c.ActiveCall("wwan1") != nil {
		t.Fatal("in-flight callback left media/call after retirement")
	}
}
