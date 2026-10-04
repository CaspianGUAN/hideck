package runtimecore

import (
	"context"
	"errors"
	"time"

	"github.com/iniwex5/vowifi-go/engine/swu"
)

func RunLoop(
	ctx context.Context,
	reconnectDelay func(int) int64,
	before func(),
	onRetry func(int, int64),
	run func(context.Context) error,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if run == nil {
		return errors.New("runtimecore: nil run function")
	}
	attempt := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if before != nil {
			before()
		}
		err := run(ctx)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		// A rejected candidate may cancel its own Delete exchange. The parent
		// context above owns stopping the runtime; cleanup cannot hide Notify 36.
		if errors.Is(err, context.Canceled) && !isIKEAddressFailure(err) {
			return err
		}
		if isTerminalRuntimeError(err) {
			return err
		}
		delay, nextAttempt := retryDecision(err, attempt, reconnectDelay)
		attempt = nextAttempt
		if delay <= 0 {
			continue
		}
		if onRetry != nil {
			onRetry(attempt, delay)
		}
		if err := waitRetry(ctx, time.Duration(delay)); err != nil {
			return err
		}
	}
}

func isTerminalRuntimeError(err error) bool {
	return errors.Is(err, ErrTooManyRedirects)
}

func retryDecision(err error, attempt int, delayFn func(int) int64) (int64, int) {
	if err == nil || err == swu.ErrFreshRuntimeRequired {
		return 0, 0
	}
	redirect, ok := err.(*ErrRedirect)
	if ok {
		return redirect.Delay, 0
	}
	if delay, addressFailure := ikeAddressRetryDelay(err); addressFailure {
		if scheduled, ok := retryDelayUntil(err, time.Now()); ok {
			delay = max(delay, scheduled)
		}
		if delayFn != nil {
			delay = max(delay, delayFn(attempt))
		}
		return delay, attempt + 1
	}
	if delay, scheduled := retryDelayUntil(err, time.Now()); scheduled {
		return delay, 0
	}
	if delayFn == nil {
		return 0, attempt + 1
	}
	delay := delayFn(attempt)
	if errors.Is(err, swu.ErrEAPAuthenticationFailed) {
		delay = max(delay, eapFailureBackoff(attempt))
	}
	return delay, attempt + 1
}

// eapFailureBackoff spaces out retries after the AAA refused the subscriber:
// a fixed 30 s loop runs hundreds of failed authentications an hour, which
// carriers throttle.
func eapFailureBackoff(attempt int) int64 {
	steps := []time.Duration{
		30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute,
	}
	if attempt < len(steps) {
		return int64(steps[attempt])
	}
	return int64(30 * time.Minute)
}

type retryAtError interface {
	RetryAt() time.Time
}

func retryDelayUntil(err error, now time.Time) (int64, bool) {
	var scheduled retryAtError
	if !errors.As(err, &scheduled) {
		return 0, false
	}
	retryAt := scheduled.RetryAt()
	if retryAt.IsZero() {
		return 0, false
	}
	delay := retryAt.Sub(now)
	if delay < 0 {
		delay = 0
	}
	return int64(delay), true
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
