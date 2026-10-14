package host

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
	"github.com/yibaiba/hideck/internal/modemvoice/media"
)

type faultPort struct {
	*testPort
	failAnswer, failHangup, keepAfterHangup atomic.Bool
	replaceAfterHangup                      atomic.Bool
	answerConnects                          bool
}

func (p *faultPort) ExecuteATContext(ctx context.Context, command string, timeout time.Duration) (string, error) {
	if command == "ATA" && p.failAnswer.Load() {
		if p.answerConnects {
			_, _ = p.testPort.ExecuteATContext(ctx, command, timeout)
		}
		return "", context.DeadlineExceeded
	}
	if command == "AT+CHUP" {
		if p.failHangup.Load() {
			return "", context.DeadlineExceeded
		}
		if p.keepAfterHangup.Load() {
			return "OK", nil
		}
	}
	response, err := p.testPort.ExecuteATContext(ctx, command, timeout)
	if command == "AT+CHUP" && p.replaceAfterHangup.Load() {
		p.mu.Lock()
		p.calls = `+CLCC: 3,1,4,0,0,"10086",129`
		p.mu.Unlock()
	}
	return response, err
}

func incomingFaultController(t *testing.T, port *faultPort, route *testRoute) *Controller {
	t.Helper()
	port.calls = `+CLCC: 3,1,4,0,0,"10010",129`
	c := New(Options{Listen: listenTest, Prepare: func(context.Context, string) (*Resources, error) {
		return &Resources{Port: port, Check: func(context.Context) error { return nil },
			Route: func() (media.AudioRoute, error) { return route, nil }, Close: func(context.Context) error { return nil }}, nil
	}})
	c.Enable(context.Background(), "d")
	t.Cleanup(func() {
		port.failHangup.Store(false)
		port.keepAfterHangup.Store(false)
		port.replaceAfterHangup.Store(false)
		if err := c.Disable(context.Background(), "d"); err != nil {
			t.Error(err)
		}
	})
	waitFor(t, func() bool { return c.ActiveCall("d") != nil })
	return c
}

func TestAnswerTimeoutRetainsConfirmedActiveCall(t *testing.T) {
	p := &faultPort{testPort: &testPort{}, answerConnects: true}
	p.failAnswer.Store(true)
	c := incomingFaultController(t, p, &testRoute{})
	call := c.ActiveCall("d")
	conn, err := listenTest()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = c.AnswerIncomingCall(context.Background(), voicehost.AnswerRequest{DeviceID: "d", CallID: call.CallID, SDP: offer(conn)})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	active := c.ActiveCall("d")
	if active == nil || active.CallID != call.CallID || active.State != "connected" {
		t.Fatalf("physical call lost after ATA timeout: %+v", active)
	}
	if err := c.HangupCall(context.Background(), "d", call.CallID); err != nil {
		t.Fatal(err)
	}
}

func TestUnansweredCallCanRetryAfterATATimeout(t *testing.T) {
	p := &faultPort{testPort: &testPort{}}
	p.failAnswer.Store(true)
	c := incomingFaultController(t, p, &testRoute{})
	call := c.ActiveCall("d")
	first, err := listenTest()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	_, err = c.AnswerIncomingCall(context.Background(), voicehost.AnswerRequest{DeviceID: "d", CallID: call.CallID, SDP: offer(first)})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	second, err := listenTest()
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	p.failAnswer.Store(false)
	if _, err = c.AnswerIncomingCall(context.Background(), voicehost.AnswerRequest{DeviceID: "d", CallID: call.CallID, SDP: offer(second)}); err != nil {
		t.Fatal("explicit retry should reuse the call's audio route", err)
	}
}

func TestAudioFailureKeepsIncomingCallForRetry(t *testing.T) {
	p := &faultPort{testPort: &testPort{}}
	route := &testRoute{fail: true}
	c := incomingFaultController(t, p, route)
	call := c.ActiveCall("d")
	conn, err := listenTest()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req := voicehost.AnswerRequest{DeviceID: "d", CallID: call.CallID, SDP: offer(conn)}
	if _, err := c.AnswerIncomingCall(context.Background(), req); err == nil {
		t.Fatal("expected real audio error")
	}
	if current := c.ActiveCall("d"); current == nil || current.CallID != call.CallID {
		t.Fatal("incoming call was discarded")
	}
	route.fail = false
	answer, err := c.AnswerIncomingCall(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if answer.OfferSDP == "" || answer.OfferSDP != c.ActiveCall("d").ClientSDP {
		t.Fatal("replacement RTP endpoint was not returned to the phone service")
	}
}

func TestDisableRetainsCallUntilPhysicalHangupConfirmed(t *testing.T) {
	for _, kind := range []string{"error", "still-active"} {
		t.Run(kind, func(t *testing.T) {
			p := &faultPort{testPort: &testPort{}}
			p.failHangup.Store(kind == "error")
			p.keepAfterHangup.Store(kind == "still-active")
			c := incomingFaultController(t, p, &testRoute{})
			id := c.ActiveCall("d").CallID
			if err := c.Disable(context.Background(), "d"); err == nil {
				t.Fatal("unconfirmed hangup must fail")
			}
			if call := c.ActiveCall("d"); call == nil || call.CallID != id || c.get("d").ctx.Err() != nil {
				t.Fatal("failed stop discarded call ownership")
			}
			p.failHangup.Store(false)
			p.keepAfterHangup.Store(false)
			if err := c.Disable(context.Background(), "d"); err != nil {
				t.Fatal(err)
			}
			if c.ActiveCall("d") != nil || c.get("d") != nil {
				t.Fatal("confirmed hangup did not finish cleanup")
			}
		})
	}
}

func TestConfirmedHangupStillExposesAudioCleanupError(t *testing.T) {
	failure := errors.New("USB audio route did not close")
	c, _ := testController(t, &testRoute{closeErr: failure})
	conn, err := listenTest()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	call, err := c.BeginCall(context.Background(), voicehost.BeginCallRequest{DeviceID: "d", Callee: "10010", SDP: offer(conn)})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.HangupCall(context.Background(), "d", call.CallID); !errors.Is(err, failure) {
		t.Fatal("cleanup error lost", err)
	}
	if c.ActiveCall("d") != nil {
		t.Fatal("physically ended call remains active")
	}
}

func TestDisableDoesNotDiscardNewIncomingCallDuringHangup(t *testing.T) {
	p := &faultPort{testPort: &testPort{}}
	c := incomingFaultController(t, p, &testRoute{})
	id := c.ActiveCall("d").CallID
	p.replaceAfterHangup.Store(true)
	if err := c.Disable(context.Background(), "d"); err == nil {
		t.Fatal("new physical call must prevent teardown")
	}
	if active := c.ActiveCall("d"); active == nil || active.CallID == id || active.Peer != "10086" || c.get("d").ctx.Err() != nil {
		t.Fatalf("replacement incoming call lost: %+v", active)
	}
}
