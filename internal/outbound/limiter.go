package outbound

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

type Event struct {
	At    time.Time `json:"at"`
	Units int       `json:"units"`
}

type Key struct {
	ICCID string
	Kind  Kind
}

// Update must serialize read-modify-write across callers and persist atomically.
// An error from apply must leave the stored history unchanged.
type Store interface {
	Update(context.Context, Key, func([]Event) ([]Event, error)) error
}

type Request struct {
	ICCID string
	Kind  Kind
	Units int
}

type LimitedError struct {
	Kind       Kind
	Reason     string
	RetryAfter time.Duration
}

func (e *LimitedError) Error() string {
	label := "短信发送"
	if e.Kind == Call {
		label = "外呼"
	}
	if e.Reason == "request_too_large" {
		return label + "分段数量超过单卡额度，请缩短内容或调整限流配置"
	}
	return fmt.Sprintf("本 SIM 的%s已达到限流额度（%s），请在 %d 秒后重试", label, e.Reason, e.RetrySeconds())
}

func (e *LimitedError) RetrySeconds() int64 { return int64(math.Ceil(e.RetryAfter.Seconds())) }

type Limiter struct {
	store  Store
	config Config
	now    func() time.Time
}

func New(store Store, config Config, now func() time.Time) (*Limiter, error) {
	if store == nil {
		return nil, errors.New("outbound limits: persistent store is required")
	}
	policy, err := config.normalized()
	if err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &Limiter{store: store, config: policy, now: now}, nil
}

// Consume records an outbound attempt before dispatch. Failed and uncertain
// attempts remain counted; protocol retries within that attempt are not charged again.
func (l *Limiter) Consume(ctx context.Context, request Request) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.Kind != Call && request.Kind != SMS {
		return errors.New("outbound limits: invalid traffic kind")
	}
	if request.Units <= 0 {
		return errors.New("outbound limits: units must be positive")
	}
	id := CanonicalICCID(request.ICCID)
	limit := l.limitFor(id, request.Kind)
	if limit.Disabled != nil && *limit.Disabled {
		return nil
	}
	if id == "" {
		return errors.New("尚未识别 SIM 的 ICCID，无法记录外发额度，请等待设备就绪")
	}
	if request.Units > limit.PerHour || request.Units > limit.PerDay {
		return &LimitedError{Kind: request.Kind, Reason: "request_too_large"}
	}
	return l.store.Update(ctx, Key{ICCID: id, Kind: request.Kind}, func(events []Event) ([]Event, error) {
		return consumeHistory(events, historyRequest{Request: request, Limit: limit, Now: l.now()})
	})
}

func (l *Limiter) limitFor(id string, kind Kind) Limit {
	policy := l.config.Policy
	if card, ok := l.config.Cards[id]; ok {
		policy = card
	}
	if kind == Call {
		return policy.Calls
	}
	return policy.SMS
}

type historyRequest struct {
	Request
	Limit Limit
	Now   time.Time
}

type usageWindow struct {
	duration time.Duration
	limit    int
	reason   string
}

func consumeHistory(events []Event, request historyRequest) ([]Event, error) {
	kept := make([]Event, 0, len(events)+1)
	var latest time.Time
	for _, event := range events {
		if event.Units <= 0 {
			return nil, errors.New("outbound limits: corrupt usage history")
		}
		if event.At.After(latest) {
			latest = event.At
		}
		if event.At.After(request.Now.Add(-24 * time.Hour)) {
			kept = append(kept, event)
		}
	}
	deadline := latest.Add(time.Duration(request.Limit.IntervalSeconds) * time.Second)
	reason := "最小间隔"
	for _, window := range []usageWindow{
		{time.Hour, request.Limit.PerHour, "滚动一小时"}, {24 * time.Hour, request.Limit.PerDay, "滚动24小时"},
	} {
		until := windowDeadline(kept, request, window)
		if until.After(deadline) {
			deadline, reason = until, window.reason
		}
	}
	if deadline.After(request.Now) {
		return nil, &LimitedError{Kind: request.Kind, Reason: reason, RetryAfter: deadline.Sub(request.Now)}
	}
	return append(kept, Event{At: request.Now, Units: request.Units}), nil
}

func windowDeadline(events []Event, request historyRequest, window usageWindow) time.Time {
	used := 0
	for _, event := range events {
		if event.At.After(request.Now.Add(-window.duration)) {
			used += event.Units
		}
	}
	for _, event := range events {
		if used <= window.limit-request.Units {
			break
		}
		if !event.At.After(request.Now.Add(-window.duration)) {
			continue
		}
		used -= event.Units
		if used <= window.limit-request.Units {
			return event.At.Add(window.duration)
		}
	}
	return time.Time{}
}
