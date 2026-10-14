package outbound

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	mu   sync.Mutex
	rows map[Key][]Event
	err  error
}

func (s *memoryStore) Update(ctx context.Context, key Key, apply func([]Event) ([]Event, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	events, err := apply(append([]Event(nil), s.rows[key]...))
	if err == nil {
		s.rows[key] = events
	}
	return err
}

func newTestLimiter(t *testing.T, cfg Config, now *time.Time) (*Limiter, *memoryStore) {
	t.Helper()
	store := &memoryStore{rows: make(map[Key][]Event)}
	limiter, err := New(store, cfg, func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	return limiter, store
}

func requireLimited(t *testing.T, err error, wait time.Duration) {
	t.Helper()
	var limited *LimitedError
	if !errors.As(err, &limited) || limited.RetryAfter != wait {
		t.Fatalf("error=%v, want limit with wait=%v", err, wait)
	}
}

func TestDefaultIntervalPerSIMAndTrafficKind(t *testing.T) {
	now := time.Date(2026, 9, 24, 23, 59, 59, 0, time.UTC)
	l, _ := newTestLimiter(t, Config{}, &now)
	call := Request{ICCID: "sim-a", Kind: Call, Units: 1}
	if err := l.Consume(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	requireLimited(t, l.Consume(context.Background(), call), 10*time.Second)
	sms := Request{ICCID: "sim-a", Kind: SMS, Units: 1}
	if err := l.Consume(context.Background(), sms); err != nil {
		t.Fatal(err)
	}
	requireLimited(t, l.Consume(context.Background(), sms), 5*time.Second)
	call.ICCID = "sim-b"
	if err := l.Consume(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Second) // Midnight does not reset rolling limits.
	if err := l.Consume(context.Background(), sms); err != nil {
		t.Fatal(err)
	}
	call.ICCID = "sim-a"
	requireLimited(t, l.Consume(context.Background(), call), 5*time.Second)
}

func TestRollingHourAndDay(t *testing.T) {
	for _, kind := range []Kind{Call, SMS} {
		t.Run(string(kind), func(t *testing.T) {
			start := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
			now := start
			l, _ := newTestLimiter(t, Config{}, &now)
			hourly := 20
			if kind == SMS {
				hourly = 30
			}
			request := Request{ICCID: "sim", Kind: kind, Units: 1}
			for i := 0; i < hourly; i++ {
				if err := l.Consume(context.Background(), request); err != nil {
					t.Fatal(err)
				}
				now = now.Add(10 * time.Second)
			}
			requireLimited(t, l.Consume(context.Background(), request), start.Add(time.Hour).Sub(now))
			for i := hourly; i < 100; i++ {
				now = start.Add(time.Duration(i/hourly)*time.Hour + time.Duration(i%hourly)*10*time.Second)
				if err := l.Consume(context.Background(), request); err != nil {
					t.Fatalf("attempt %d: %v", i, err)
				}
			}
			now = start.Add(10 * time.Hour)
			requireLimited(t, l.Consume(context.Background(), request), 14*time.Hour)
			now = start.Add(24 * time.Hour)
			if err := l.Consume(context.Background(), request); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMultipartAndConfiguration(t *testing.T) {
	now := time.Now()
	disabled := true
	l, _ := newTestLimiter(t, Config{
		Policy: Policy{Calls: Limit{Disabled: &disabled}},
		Cards:  map[string]Policy{"\"123FF\"": {SMS: Limit{PerHour: 4, PerDay: 10}}},
	}, &now)
	request := Request{ICCID: "123", Kind: SMS, Units: 3}
	if err := l.Consume(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Second)
	requireLimited(t, l.Consume(context.Background(), request), time.Hour-5*time.Second)
	request.Units = 6
	var limited *LimitedError
	if err := l.Consume(context.Background(), request); !errors.As(err, &limited) || limited.Reason != "request_too_large" {
		t.Fatalf("err=%v", err)
	}
	// A card override for SMS must not accidentally enable globally disabled calls.
	for i := 0; i < 2; i++ {
		if err := l.Consume(context.Background(), Request{ICCID: "123", Kind: Call, Units: 1}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLimiterErrorsDoNotSpendBudget(t *testing.T) {
	now := time.Now()
	l, store := newTestLimiter(t, Config{}, &now)
	request := Request{ICCID: "sim", Kind: SMS, Units: 1}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.Consume(cancelled, request); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	store.err = errors.New("database unavailable")
	if err := l.Consume(context.Background(), request); !errors.Is(err, store.err) {
		t.Fatal(err)
	}
	store.err = nil
	if len(store.rows) != 0 {
		t.Fatal("failed request spent budget")
	}
	if err := l.Consume(context.Background(), Request{Kind: SMS, Units: 1}); err == nil {
		t.Fatal("missing identity accepted")
	}
	if err := l.Consume(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	now = now.Add(-time.Hour)
	requireLimited(t, l.Consume(context.Background(), request), time.Hour+5*time.Second)
}

func TestConcurrentAttemptsOnlyOneAccepted(t *testing.T) {
	now := time.Now()
	l, store := newTestLimiter(t, Config{}, &now)
	var group sync.WaitGroup
	for i := 0; i < 30; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_ = l.Consume(context.Background(), Request{ICCID: "sim", Kind: SMS, Units: 1})
		}()
	}
	group.Wait()
	if len(store.rows[Key{ICCID: "sim", Kind: SMS}]) != 1 {
		t.Fatal("concurrent attempts exceeded interval")
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for _, config := range []Config{
		{Policy: Policy{Calls: Limit{PerDay: -1}}},
		{Cards: map[string]Policy{"": {}}},
		{Cards: map[string]Policy{"123": {}, "123F": {}}},
	} {
		if _, err := New(&memoryStore{}, config, time.Now); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
}
