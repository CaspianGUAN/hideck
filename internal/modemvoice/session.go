package modemvoice

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrSessionClosed = errors.New("modem voice: session closed")
	ErrCallChanged   = errors.New("modem voice: call no longer belongs to this session")
)

// Port must retain ownership of its serial queue until Close has completed.
// A new SIM/worker generation always gets a new Session, even on the same port.
type Port interface {
	Executor
	SubscribeVoiceChanges() (<-chan struct{}, func())
	WaitATIdle(context.Context) error
}

// Update contains all CLCC changes observed by an operation, including those
// discovered before a rejected command. Consumers must publish them on errors
// too. Accepted only means the AT command returned OK, never remote answer.
type Update struct {
	Changes   []Change
	Accepted  bool
	Attempted bool // False when validation/CLCC rejected before issuing control.
}

type Session struct {
	ctx         context.Context
	cancel      context.CancelFunc
	port        Port
	client      *Client
	tracker     *Tracker
	gate        chan struct{}
	wakeups     <-chan struct{}
	wake        chan struct{}
	unsubscribe func()
	unsubOnce   sync.Once
	watchMu     sync.Mutex
	watchDone   chan struct{}
}

func NewSession(parent context.Context, port Port) (*Session, error) {
	if parent == nil || port == nil {
		return nil, errors.New("modem voice: session requires context and port")
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	client, err := NewClient(port)
	if err != nil {
		return nil, err
	}
	wakeups, unsubscribe := port.SubscribeVoiceChanges()
	if wakeups == nil || unsubscribe == nil {
		if unsubscribe != nil {
			unsubscribe()
		}
		return nil, errors.New("modem voice: invalid port subscription")
	}
	ctx, cancel := context.WithCancel(parent)
	return &Session{ctx: ctx, cancel: cancel, port: port, client: client, tracker: NewTracker(),
		gate: make(chan struct{}, 1), wakeups: wakeups, wake: make(chan struct{}, 1), unsubscribe: unsubscribe}, nil
}

func (s *Session) enter(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, errors.New("modem voice: context is required")
	}
	if s.ctx.Err() != nil {
		return nil, nil, ErrSessionClosed
	}
	opCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	release := func() { stop(); cancel() }
	select {
	case <-opCtx.Done():
		release()
		return nil, nil, opCtx.Err()
	case s.gate <- struct{}{}:
	}
	if s.ctx.Err() != nil || opCtx.Err() != nil {
		<-s.gate
		err := opCtx.Err()
		if s.ctx.Err() != nil {
			err = ErrSessionClosed
		}
		release()
		return nil, nil, err
	}
	return opCtx, func() { <-s.gate; release() }, nil
}

// Close cancels controls, unsubscribes and waits for their actual AT queue
// completion. A deadline/error is not successful cleanup: retry Close before
// transferring port ownership. This does not pretend physical calls ended.
func (s *Session) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("modem voice: close context is required")
	}
	s.cancel()
	s.unsubOnce.Do(s.unsubscribe)
	s.watchMu.Lock()
	done := s.watchDone
	s.watchMu.Unlock()
	if done != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.gate <- struct{}{}:
	}
	defer func() { <-s.gate }()
	return s.port.WaitATIdle(ctx)
}

// Calls is the last successful snapshot, not a promise that the device is live.
func (s *Session) Calls() []TrackedCall { return s.tracker.Calls() }

func (s *Session) Refresh(ctx context.Context) (Update, error) {
	opCtx, release, err := s.enter(ctx)
	if err != nil {
		return Update{}, err
	}
	defer release()
	return s.refresh(opCtx)
}

func (s *Session) refresh(ctx context.Context) (Update, error) {
	calls, err := s.client.Calls(ctx)
	if err != nil {
		return Update{}, err
	}
	if s.ctx.Err() != nil {
		return Update{}, ErrSessionClosed
	}
	return Update{Changes: s.tracker.applyCalls(calls)}, nil
}
