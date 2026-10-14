package device

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/modem"
)

type controlledATSession struct {
	execute  func(string, time.Duration) (string, error)
	closed   atomic.Bool
	closeErr error
}

func (s *controlledATSession) Execute(cmd string, timeout time.Duration) (string, error) {
	return s.execute(cmd, timeout)
}
func (s *controlledATSession) Close() error { s.closed.Store(true); return s.closeErr }

func atTestPool(t *testing.T) (*Pool, *Worker) {
	t.Helper()
	p := NewPool(&config.Config{})
	t.Cleanup(p.cancel)
	w := &Worker{ID: "d", Config: config.DeviceConfig{ID: "d", DeviceBackend: "qmi", ATPort: "/dev/test-at"}}
	p.workers[w.ID] = w
	return p, w
}

func TestDeviceATUsesTransientSessionAndExposesCloseFailure(t *testing.T) {
	p, _ := atTestPool(t)
	closeErr := errors.New("close failed")
	session := &controlledATSession{closeErr: closeErr, execute: func(cmd string, timeout time.Duration) (string, error) {
		if cmd != "AT+CSQ" || timeout != 7*time.Second {
			t.Fatalf("cmd=%s timeout=%v", cmd, timeout)
		}
		return "OK\r\n", nil
	}}
	p.openATSession = func(port string) (atSerialSession, error) {
		if port != "/dev/test-at" {
			t.Fatal(port)
		}
		return session, nil
	}
	got, err := p.ExecuteAT("d", "AT+CSQ", 7*time.Second)
	if got != "OK\r\n" || !errors.Is(err, closeErr) || !session.closed.Load() {
		t.Fatalf("response=%q err=%v closed=%v", got, err, session.closed.Load())
	}
}

func TestDeviceATCanceledWrittenCommandKeepsPortUntilDrained(t *testing.T) {
	p, _ := atTestPool(t)
	written, finish := make(chan struct{}), make(chan struct{})
	var opens atomic.Int32
	session := &controlledATSession{execute: func(string, time.Duration) (string, error) {
		close(written)
		<-finish
		return "OK", nil
	}}
	p.openATSession = func(string) (atSerialSession, error) { opens.Add(1); return session, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := p.ExecuteATContext(ctx, ATRequest{"d", "AT+CSQ", time.Second}); done <- err }()
	<-written
	cancel()
	queued, cancelQueued := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelQueued()
	_, err := p.ExecuteATContext(queued, ATRequest{"d", "AT", time.Second})
	if !errors.Is(err, context.DeadlineExceeded) || opens.Load() != 1 {
		t.Fatalf("second reader opened before draining first: opens=%d err=%v", opens.Load(), err)
	}
	select {
	case err := <-done:
		t.Fatalf("canceled call returned before its command completed: %v", err)
	default:
	}
	close(finish)
	if err := <-done; !errors.Is(err, context.Canceled) || !session.closed.Load() {
		t.Fatalf("err=%v closed=%v", err, session.closed.Load())
	}
}

type observedATContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *observedATContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestDeviceATQueuedCommandCannotOperateReplacementWorker(t *testing.T) {
	for _, change := range []string{"worker", "SIM", "stop"} {
		t.Run(change, func(t *testing.T) { checkQueuedATIdentity(t, change) })
	}
}

func checkQueuedATIdentity(t *testing.T, change string) {
	t.Helper()
	p, old := atTestPool(t)
	release, err := p.acquireDeviceAT(context.Background(), old.ID)
	if err != nil {
		t.Fatal(err)
	}
	var opens atomic.Int32
	p.openATSession = func(string) (atSerialSession, error) { opens.Add(1); return nil, errors.New("unexpected open") }
	ctx := &observedATContext{Context: context.Background(), waiting: make(chan struct{})}
	done := make(chan error, 1)
	go func() { _, err := p.ExecuteATContext(ctx, ATRequest{old.ID, "ATD10010;", time.Second}); done <- err }()
	<-ctx.waiting
	switch change {
	case "worker":
		p.mu.Lock()
		p.workers[old.ID] = &Worker{ID: old.ID, Config: old.Config}
		p.mu.Unlock()
	case "SIM":
		old.cacheMu.Lock()
		old.state.Identity.ICCID = "another-card"
		old.cacheMu.Unlock()
	case "stop":
		old.stop = make(chan struct{})
		close(old.stop)
	}
	release()
	if err := <-done; !errors.Is(err, ErrATUnavailable) || opens.Load() != 0 {
		t.Fatalf("err=%v opens=%d", err, opens.Load())
	}
}

func TestDeviceATDoesNotBypassUnavailableRuntime(t *testing.T) {
	p, w := atTestPool(t)
	var err error
	w.Modem, err = modem.NewSMSAuxiliary(w.Config)
	if err != nil {
		t.Fatal(err)
	}
	p.openATSession = func(string) (atSerialSession, error) {
		t.Fatal("opened a second reader over configured AT ownership")
		return nil, nil
	}
	if _, err := p.ExecuteAT("d", "AT", time.Second); !errors.Is(err, ErrATUnavailable) {
		t.Fatal(err)
	}
}

func TestDeviceATInvalidOrMissingPortDoesNotOpenSerial(t *testing.T) {
	p, w := atTestPool(t)
	p.openATSession = func(string) (atSerialSession, error) {
		t.Fatal("invalid command or missing port reached serial factory")
		return nil, nil
	}
	if _, err := p.ExecuteAT("d", "AT\rATD10010;", time.Second); !errors.Is(err, ErrATUnavailable) {
		t.Fatal(err)
	}
	w.Config.ATPort = ""
	if _, err := p.ExecuteAT("d", "AT", time.Second); !errors.Is(err, ErrATUnavailable) {
		t.Fatal(err)
	}
}
