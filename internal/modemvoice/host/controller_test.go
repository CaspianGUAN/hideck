package host

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
	"github.com/yibaiba/hideck/internal/modemvoice/media"
)

type testPort struct {
	mu             sync.Mutex
	calls          string
	dialed, hungup int
}

func (p *testPort) ExecuteATContext(ctx context.Context, command string, timeout time.Duration) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case command == "AT+CLCC":
		return p.calls + "\r\nOK\r\n", nil
	case strings.HasPrefix(command, "ATD"):
		p.dialed++
		p.calls = `+CLCC: 3,0,2,0,0,"10010",129`
		return "OK", nil
	case command == "AT+CHUP":
		p.hungup++
		p.calls = ""
		return "OK", nil
	case command == "ATA":
		p.calls = `+CLCC: 3,1,0,0,0,"10010",129`
		return "OK", nil
	default:
		return "", errors.New("unexpected command")
	}
}
func (*testPort) SubscribeVoiceChanges() (<-chan struct{}, func()) {
	return make(chan struct{}), func() {}
}
func (*testPort) WaitATIdle(context.Context) error { return nil }

type testPCM struct {
	done chan struct{}
	once sync.Once
}

func (p *testPCM) ReadFrame() ([]int16, error) { <-p.done; return nil, io.EOF }
func (*testPCM) WriteFrame([]int16) error      { return nil }
func (p *testPCM) Close() error                { p.once.Do(func() { close(p.done) }); return nil }

type testRoute struct {
	fail     bool
	closed   atomic.Bool
	closeErr error
}

func (r *testRoute) Start(context.Context) (io.Closer, error) {
	if r.fail {
		return nil, errors.New("real adapter unavailable")
	}
	return r, nil
}
func (r *testRoute) Close() error { r.closed.Store(true); return r.closeErr }
func (*testRoute) OpenPCM(context.Context) (media.PCM, error) {
	return &testPCM{done: make(chan struct{})}, nil
}

func listenTest() (net.PacketConn, error) {
	return net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
}
func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition timed out")
		}
		time.Sleep(time.Millisecond)
	}
}
func testController(t *testing.T, route *testRoute) (*Controller, *testPort) {
	t.Helper()
	p := &testPort{}
	c := New(Options{Listen: listenTest, Prepare: func(context.Context, string) (*Resources, error) {
		return &Resources{Port: p, Check: func(context.Context) error { return nil }, Route: func() (media.AudioRoute, error) { return route, nil }, Close: func(context.Context) error { return nil }}, nil
	}})
	c.Enable(context.Background(), "d")
	waitFor(t, func() bool { return c.DeviceStatus("d")["ready"] == true })
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := c.Disable(ctx, "d"); err != nil {
			t.Error(err)
		}
	})
	return c, p
}

func TestOutboundRequiresRealAudioAndCLCCAnswer(t *testing.T) {
	route := &testRoute{}
	c, p := testController(t, route)
	events := make(chan voicehost.CallEvent, 8)
	unsubscribe := c.SubscribeCallEvents(func(event voicehost.CallEvent) { events <- event })
	defer unsubscribe()
	relayConn, err := listenTest()
	if err != nil {
		t.Fatal(err)
	}
	defer relayConn.Close()
	snap, err := c.BeginCall(context.Background(), voicehost.BeginCallRequest{DeviceID: "d", Callee: "10010", SDP: offer(relayConn)})
	if err != nil {
		t.Fatal(err)
	}
	if snap.State != "calling" {
		t.Fatal("AT OK manufactured answer", snap)
	}
	p.mu.Lock()
	p.calls = `+CLCC: 3,0,0,0,0,"10010",129`
	p.mu.Unlock()
	waitFor(t, func() bool { return c.ActiveCall("d").State == "connected" })
	if err := c.HangupCall(context.Background(), "d", snap.CallID); err != nil {
		t.Fatal(err)
	}
	if !route.closed.Load() || c.ActiveCall("d") != nil {
		t.Fatal("media or call leaked")
	}
	ended := false
finalized:
	for {
		select {
		case event := <-events:
			if event.Type == "CallEnded" {
				if event.Reason != "local_hangup" {
					t.Fatalf("local hangup attributed to remote: %+v", event)
				}
				ended = true
			}
			if event.Type == "CallFinalized" {
				if !ended || event.CallID != snap.CallID || event.AudioCodec != "PCMU" {
					t.Fatalf("invalid finalization order or metadata: %+v", event)
				}
				break finalized
			}
		case <-time.After(time.Second):
			t.Fatal("call recording was not finalized")
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dialed != 1 || p.hungup != 1 {
		t.Fatalf("dial=%d hangup=%d", p.dialed, p.hungup)
	}
}

func TestAudioFailureDoesNotDial(t *testing.T) {
	c, p := testController(t, &testRoute{fail: true})
	r, err := listenTest()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_, err = c.BeginCall(context.Background(), voicehost.BeginCallRequest{DeviceID: "d", Callee: "10010", SDP: offer(r)})
	if err == nil || !strings.Contains(err.Error(), "adapter unavailable") {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dialed != 0 {
		t.Fatal("dialed without audio")
	}
}

func TestRejectedDialDoesNotHangupExistingPhysicalCall(t *testing.T) {
	c, p := testController(t, &testRoute{})
	r, err := listenTest()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	p.mu.Lock()
	p.calls = `+CLCC: 3,0,0,0,0,"10010",129`
	p.mu.Unlock()
	_, err = c.BeginCall(context.Background(), voicehost.BeginCallRequest{DeviceID: "d", Callee: "10010", SDP: offer(r)})
	if err == nil {
		t.Fatal("existing call should reject dial")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dialed != 0 || p.hungup != 0 {
		t.Fatalf("changed existing call: dial=%d hangup=%d", p.dialed, p.hungup)
	}
}

func TestPrepareFailureCanBeRetriedAfterCleanup(t *testing.T) {
	var attempts atomic.Int32
	c := New(Options{Listen: listenTest, Prepare: func(context.Context, string) (*Resources, error) {
		attempts.Add(1)
		return nil, errors.New("missing adb")
	}})
	c.Enable(context.Background(), "d")
	<-c.get("d").done
	c.Enable(context.Background(), "d")
	<-c.get("d").done
	if attempts.Load() != 2 {
		t.Fatal("failed preparation permanently blocks retry")
	}
	if err := c.Disable(context.Background(), "d"); err != nil {
		t.Fatal(err)
	}
}

func TestReplacementSIMWaitsForPreviousRuntimeCleanup(t *testing.T) {
	var generation, prepared atomic.Int32
	cleanupStarted, releaseCleanup := make(chan struct{}), make(chan struct{})
	var once sync.Once
	c := New(Options{Listen: listenTest, Identity: func(string) string {
		if generation.Load() == 0 {
			return "old"
		}
		return "new"
	},
		Prepare: func(context.Context, string) (*Resources, error) {
			n := prepared.Add(1)
			return &Resources{Port: &testPort{}, Check: func(ctx context.Context) error { return ctx.Err() },
				Route: func() (media.AudioRoute, error) { return &testRoute{}, nil }, Close: func(context.Context) error {
					if n == 1 {
						once.Do(func() { close(cleanupStarted) })
						<-releaseCleanup
					}
					return nil
				}}, nil
		}})
	c.Enable(context.Background(), "d")
	waitFor(t, func() bool { return c.DeviceStatus("d")["ready"] == true })
	generation.Store(1)
	c.Enable(context.Background(), "d")
	select {
	case <-cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("old generation did not stop")
	}
	if prepared.Load() != 1 {
		t.Fatal("prepared replacement while old runtime still owned")
	}
	close(releaseCleanup)
	waitFor(t, func() bool { return prepared.Load() == 2 && c.DeviceStatus("d")["ready"] == true })
	if err := c.Disable(context.Background(), "d"); err != nil {
		t.Fatal(err)
	}
}

func TestIncomingOfferAndCallbacksCanQueryAndReject(t *testing.T) {
	c, p := testController(t, &testRoute{})
	ended := make(chan error, 1)
	u := c.SubscribeIncomingCalls(func(in voicehost.IncomingCall) {
		if c.ActiveCall(in.DeviceID) == nil || in.OfferSDP == "" {
			ended <- errors.New("incoming has no media offer")
			return
		}
		ended <- c.RejectIncomingCall(voicehost.RejectRequest{DeviceID: in.DeviceID, CallID: in.CallID})
	})
	defer u()
	p.mu.Lock()
	p.calls = `+CLCC: 3,1,4,0,0,"10010",129`
	p.mu.Unlock()
	select {
	case err := <-ended:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("callback deadlocked")
	}
}
