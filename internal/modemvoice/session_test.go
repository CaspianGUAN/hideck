package modemvoice

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type sessionPort struct {
	mu           sync.Mutex
	response     string
	commands     []string
	wakeups      chan struct{}
	unsubscribed int
	fences       int
	fenceErr     error
	commandErr   error
}

func newSessionPort() *sessionPort { return &sessionPort{wakeups: make(chan struct{}, 1)} }

func (p *sessionPort) ExecuteATContext(ctx context.Context, cmd string, _ time.Duration) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.commands = append(p.commands, cmd)
	if cmd == "AT+CLCC" {
		return p.response, nil
	}
	return "", p.commandErr
}

func (p *sessionPort) SubscribeVoiceChanges() (<-chan struct{}, func()) {
	return p.wakeups, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.unsubscribed++
	}
}

func (p *sessionPort) WaitATIdle(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fences++
	return p.fenceErr
}

func newTestSession(t *testing.T, p Port) *Session {
	t.Helper()
	s, err := NewSession(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return s
}

func TestSessionRejectsOldGenerationBeforeWriting(t *testing.T) {
	p := newSessionPort()
	p.response = "+CLCC: 1,1,4,0,0,\"10010\",129"
	old := newTestSession(t, p)
	if _, err := old.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	id := old.Calls()[0].ID
	if err := old.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	fresh := newTestSession(t, p)
	if _, err := fresh.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := len(p.commands)
	if _, err := old.Hangup(context.Background(), id); !errors.Is(err, ErrSessionClosed) {
		t.Fatal(err)
	}
	if _, err := fresh.Hangup(context.Background(), id); !errors.Is(err, ErrCallChanged) {
		t.Fatal(err)
	}
	if len(p.commands) != before {
		t.Fatal("stale session wrote to modem")
	}
}

func TestSessionRechecksIndexReuseBeforeHangup(t *testing.T) {
	p := newSessionPort()
	p.response = "+CLCC: 1,0,0,0,0,\"10010\",129"
	s := newTestSession(t, p)
	if _, err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	id := s.Calls()[0].ID
	p.response = "+CLCC: 1,0,2,0,0,\"10010\",129"
	update, err := s.Hangup(context.Background(), id)
	if !errors.Is(err, ErrCallChanged) || len(update.Changes) != 2 || !update.Changes[0].Ended {
		t.Fatalf("reuse not exposed: %+v %v", update, err)
	}
	if !reflect.DeepEqual(p.commands, []string{"AT+CLCC", "AT+CLCC"}) {
		t.Fatal(p.commands)
	}
}

func TestSessionAnswerDoesNotManufactureConnection(t *testing.T) {
	p := newSessionPort()
	p.response = "+CLCC: 1,1,4,0,0,\"10010\",129"
	s := newTestSession(t, p)
	if _, err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	update, err := s.Answer(context.Background(), s.Calls()[0].ID)
	if err != nil || !update.Accepted || len(update.Changes) != 0 || s.Calls()[0].Call.State != Incoming {
		t.Fatalf("AT OK treated as connected: %+v %v", update, err)
	}
}

func TestSessionCloseFailureCannotReenableControls(t *testing.T) {
	p := newSessionPort()
	s := newTestSession(t, p)
	p.fenceErr = context.DeadlineExceeded
	if err := s.Close(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if _, err := s.Dial(context.Background(), "10010"); !errors.Is(err, ErrSessionClosed) {
		t.Fatal(err)
	}
	p.fenceErr = nil
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.unsubscribed != 1 || p.fences != 2 || len(p.commands) != 0 {
		t.Fatalf("cleanup: %+v", p)
	}
}

func TestSessionDialTimeoutDoesNotRedialOrLoseLaterCall(t *testing.T) {
	p := newSessionPort()
	s := newTestSession(t, p)
	p.commandErr = context.DeadlineExceeded
	update, err := s.Dial(context.Background(), "10010")
	if !errors.Is(err, context.DeadlineExceeded) || update.Accepted {
		t.Fatalf("%+v %v", update, err)
	}
	p.response = "+CLCC: 1,0,2,0,0,\"10010\",129"
	update, err = s.Refresh(context.Background())
	if err != nil || len(update.Changes) != 1 {
		t.Fatalf("%+v %v", update, err)
	}
	if !reflect.DeepEqual(p.commands, []string{"AT+CLCC", "ATD10010;", "AT+CLCC"}) {
		t.Fatal(p.commands)
	}
}
