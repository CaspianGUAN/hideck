package host

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
	"github.com/yibaiba/hideck/internal/modemvoice"
)

type dtmfPort struct {
	testPort
	tones   []string
	failure error
}

func (p *dtmfPort) ExecuteATContext(ctx context.Context, command string, timeout time.Duration) (string, error) {
	if strings.HasPrefix(command, "AT+VTS=") {
		p.tones = append(p.tones, command)
		return "OK", p.failure
	}
	return p.testPort.ExecuteATContext(ctx, command, timeout)
}

func dtmfController(t *testing.T) (*Controller, *device, *dtmfPort) {
	t.Helper()
	p := &dtmfPort{testPort: testPort{calls: `+CLCC: 1,0,0,0,0,"10010",129`}}
	s, err := modemvoice.NewSession(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if _, err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &device{id: "d", ctx: context.Background(), session: s, phase: "ready",
		resources: &Resources{Check: func(context.Context) error { return nil }},
		call:      &call{trackedID: s.Calls()[0].ID, snapshot: voicehost.CallSnapshot{CallID: "modemvoice-test", State: "connected"}},
	}
	c := New(Options{})
	c.devices[d.id] = d
	return c, d, p
}

func TestHostDTMFRoutesOnlyCurrentReadyCall(t *testing.T) {
	c, d, p := dtmfController(t)
	if err := c.SendCallDTMF("d", "modemvoice-test", "5"); err != nil {
		t.Fatal(err)
	}
	if len(p.tones) != 1 || p.tones[0] != `AT+VTS="5",3` {
		t.Fatal(p.tones)
	}
	for _, ids := range [][2]string{{"other", "modemvoice-test"}, {"d", "old-call"}} {
		if err := c.SendCallDTMF(ids[0], ids[1], "5"); err == nil {
			t.Fatal("foreign call accepted")
		}
	}
	d.resources.Check = func(context.Context) error { return errors.New("SIM changed") }
	if err := c.SendCallDTMF("d", "modemvoice-test", "5"); err == nil {
		t.Fatal("stale SIM accepted")
	}
	if len(p.tones) != 1 {
		t.Fatal("rejected request sent tone")
	}
}

func TestHostDTMFPropagatesFailureAndCallEnd(t *testing.T) {
	c, d, p := dtmfController(t)
	p.failure = context.DeadlineExceeded
	if err := c.SendCallDTMF("d", "modemvoice-test", "#"); !errors.Is(err, p.failure) {
		t.Fatal(err)
	}
	if len(p.tones) != 1 {
		t.Fatal("tone retried")
	}
	p.calls = ""
	if err := c.SendCallDTMF("d", "modemvoice-test", "#"); err == nil {
		t.Fatal("ended call accepted")
	}
	if d.call != nil {
		t.Fatal("CLCC call end was not applied")
	}
	if len(p.tones) != 1 {
		t.Fatal("tone sent after call ended")
	}
}
