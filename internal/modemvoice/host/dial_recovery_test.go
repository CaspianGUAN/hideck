package host

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
	"github.com/yibaiba/hideck/internal/modemvoice/media"
)

type uncertainDialPort struct {
	*faultPort
	afterDial, failRefresh atomic.Bool
	createsCall            bool
}

func (p *uncertainDialPort) ExecuteATContext(ctx context.Context, command string, timeout time.Duration) (string, error) {
	if strings.HasPrefix(command, "ATD") {
		if p.createsCall {
			_, _ = p.testPort.ExecuteATContext(ctx, command, timeout)
		}
		p.afterDial.Store(true)
		return "", context.DeadlineExceeded
	}
	if command == "AT+CLCC" && p.afterDial.Load() && p.failRefresh.Load() {
		return "", context.DeadlineExceeded
	}
	return p.faultPort.ExecuteATContext(ctx, command, timeout)
}

func uncertainDialController(t *testing.T, port *uncertainDialPort) *Controller {
	t.Helper()
	c := New(Options{Listen: listenTest, Prepare: func(context.Context, string) (*Resources, error) {
		return &Resources{Port: port, Check: func(context.Context) error { return nil },
			Route: func() (media.AudioRoute, error) { return &testRoute{}, nil }}, nil
	}})
	c.Enable(context.Background(), "d")
	t.Cleanup(func() {
		port.failRefresh.Store(false)
		port.failHangup.Store(false)
		port.keepAfterHangup.Store(false)
		if err := c.Disable(context.Background(), "d"); err != nil {
			t.Error(err)
		}
	})
	waitFor(t, func() bool { return c.DeviceStatus("d")["ready"] == true })
	return c
}

func TestFailedDialRetainsCallUntilHangupConfirmed(t *testing.T) {
	for _, kind := range []string{"hangup_error", "still_active", "refresh_error"} {
		t.Run(kind, func(t *testing.T) {
			p := &uncertainDialPort{faultPort: &faultPort{testPort: &testPort{}}, createsCall: true}
			p.failHangup.Store(kind == "hangup_error")
			p.keepAfterHangup.Store(kind == "still_active")
			p.failRefresh.Store(kind == "refresh_error")
			c := uncertainDialController(t, p)
			conn, err := listenTest()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			snapshot, err := c.BeginCall(context.Background(), voicehost.BeginCallRequest{DeviceID: "d", Callee: "10010", SDP: offer(conn)})
			if !errors.Is(err, context.DeadlineExceeded) || snapshot.CallID == "" {
				t.Fatalf("lost error or recovery handle: %+v %v", snapshot, err)
			}
			if active := c.ActiveCall("d"); active == nil || active.CallID != snapshot.CallID {
				t.Fatal("physical call discarded")
			}
			p.failHangup.Store(false)
			p.keepAfterHangup.Store(false)
			p.failRefresh.Store(false)
			if err := c.HangupCall(context.Background(), "d", snapshot.CallID); err != nil {
				t.Fatal(err)
			}
			if c.ActiveCall("d") != nil {
				t.Fatal("confirmed hangup did not clear call")
			}
		})
	}
}

func TestFailedDialClearsOnlyAfterSuccessfulEmptyCLCC(t *testing.T) {
	p := &uncertainDialPort{faultPort: &faultPort{testPort: &testPort{}}}
	p.failRefresh.Store(true)
	c := uncertainDialController(t, p)
	conn, err := listenTest()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	snapshot, err := c.BeginCall(context.Background(), voicehost.BeginCallRequest{DeviceID: "d", Callee: "10010", SDP: offer(conn)})
	if err == nil || snapshot.CallID == "" || c.ActiveCall("d") == nil {
		t.Fatal("unconfirmed dial discarded")
	}
	p.failRefresh.Store(false)
	waitFor(t, func() bool { return c.ActiveCall("d") == nil })
}
