package modemvoice

import (
	"context"
	"errors"
	"testing"
	"time"
)

type watchedSessionPort struct {
	*sessionPort
	polls pollExecutor
}

func (p *watchedSessionPort) ExecuteATContext(ctx context.Context, command string, timeout time.Duration) (string, error) {
	return p.polls.ExecuteATContext(ctx, command, timeout)
}

func TestSessionCloseJoinsObserverBeforePortTransfer(t *testing.T) {
	p := &watchedSessionPort{sessionPort: newSessionPort(), polls: pollExecutor{requests: make(chan chan pollResult)}}
	s := newTestSession(t, p)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.Watch(context.Background(), WatchOptions{
			OnChanges: func([]Change) { close(entered); <-release }, OnError: func(error) {},
		})
	}()
	(<-p.polls.requests) <- pollResult{response: "+CLCC: 1,1,4,0,0"}
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if p.fences != 0 {
		t.Fatal("port transferred before observer stopped")
	}
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.fences != 1 || p.unsubscribed != 1 {
		t.Fatalf("cleanup: %+v", p.sessionPort)
	}
}

func TestSessionPortLossInvalidatesControlsWithoutInventingEnd(t *testing.T) {
	p := &watchedSessionPort{sessionPort: newSessionPort(), polls: pollExecutor{requests: make(chan chan pollResult)}}
	s := newTestSession(t, p)
	changes := make(chan []Change, 1)
	done := make(chan error, 1)
	go func() {
		done <- s.Watch(context.Background(), WatchOptions{Interval: time.Hour,
			OnChanges: func(c []Change) { changes <- c }, OnError: func(error) {},
		})
	}()
	(<-p.polls.requests) <- pollResult{response: "+CLCC: 1,1,0,0,0"}
	first := <-changes
	close(p.wakeups)
	if err := <-done; err == nil {
		t.Fatal("port loss was hidden")
	}
	if len(changes) != 0 || len(s.Calls()) != 1 {
		t.Fatal("port loss manufactured a completed call")
	}
	if _, err := s.Hangup(context.Background(), first[0].Call.ID); !errors.Is(err, ErrSessionClosed) {
		t.Fatal(err)
	}
}
