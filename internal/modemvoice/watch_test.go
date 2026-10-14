package modemvoice

import (
	"context"
	"errors"
	"testing"
	"time"
)

type pollResult struct {
	response string
	err      error
}

type pollExecutor struct {
	requests chan chan pollResult
}

func (p pollExecutor) ExecuteATContext(ctx context.Context, _ string, _ time.Duration) (string, error) {
	reply := make(chan pollResult, 1)
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case p.requests <- reply:
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-reply:
		return result.response, result.err
	}
}

func TestWatchErrorPreservesCallAndReportsRealEnd(t *testing.T) {
	executor := pollExecutor{requests: make(chan chan pollResult)}
	client, _ := NewClient(executor)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wakeups := make(chan struct{}, 1)
	changes := make(chan []Change, 8)
	errorsSeen := make(chan error, 8)
	done := make(chan error, 1)
	go func() {
		done <- client.Watch(ctx, WatchOptions{Wakeups: wakeups, Interval: time.Hour,
			OnChanges: func(v []Change) { changes <- v }, OnError: func(err error) { errorsSeen <- err }})
	}()
	(<-executor.requests) <- pollResult{response: "+CLCC: 1,1,4,0,0,\"10010\",129"}
	first := <-changes
	wakeups <- struct{}{}
	(<-executor.requests) <- pollResult{err: context.DeadlineExceeded}
	if err := <-errorsSeen; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatal("failed CLCC ended the call")
	}
	wakeups <- struct{}{}
	(<-executor.requests) <- pollResult{response: "+CLCC: 1,1,0,0,0,\"10010\",129"}
	answered := <-changes
	if answered[0].Call.ID != first[0].Call.ID || answered[0].Call.Call.State != Active {
		t.Fatalf("answer: %+v", answered)
	}
	wakeups <- struct{}{}
	(<-executor.requests) <- pollResult{}
	ended := <-changes
	if len(ended) != 1 || !ended[0].Ended || ended[0].Call.ID != first[0].Call.ID {
		t.Fatalf("end: %+v", ended)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestWatchCancellationStopsInFlightPoll(t *testing.T) {
	executor := pollExecutor{requests: make(chan chan pollResult)}
	client, _ := NewClient(executor)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- client.Watch(ctx, WatchOptions{OnChanges: func([]Change) {}, OnError: func(error) {}})
	}()
	<-executor.requests
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
