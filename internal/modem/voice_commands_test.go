package modem

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCanceledQueuedVoiceCommandNeverWrites(t *testing.T) {
	m := newRunningTestManager(t)
	port := &timeoutSerialPort{}
	m.port = port
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := m.ExecuteATContext(ctx, "ATD10010;", time.Second)
		done <- err
	}()
	req := <-m.cmdChan
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	m.handleCommand(req)
	if port.writes.Load() != 0 {
		t.Fatal("canceled request wrote to serial")
	}
}

func TestCanceledVoiceRequestOwnsLateReply(t *testing.T) {
	m := newRunningTestManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := m.ExecuteATContext(ctx, "ATD10010;", time.Second)
		done <- err
	}()
	first := <-m.cmdChan
	cancel()
	<-done
	response := make(chan string, 1)
	go func() {
		value, _ := m.ExecuteATContext(context.Background(), "AT+CLCC", time.Second)
		response <- value
	}()
	second := <-m.cmdChan
	first.respChan <- "old reply"
	second.respChan <- "+CLCC: 1,0,2,0,0"
	if got := <-response; got != "+CLCC: 1,0,2,0,0" {
		t.Fatalf("late response contaminated query: %q", got)
	}
}

func TestVoiceTerminalResponsesCompleteCommand(t *testing.T) {
	for _, result := range []string{"BUSY", "NO CARRIER", "NO ANSWER", "NO DIALTONE"} {
		t.Run(result, func(t *testing.T) {
			m := newRunningTestManager(t)
			port := &timeoutSerialPort{}
			m.port = port
			req := commandRequest{cmd: "ATD10010;", timeout: time.Second,
				respChan: make(chan string, 1), errChan: make(chan error, 1)}
			go func() { m.rxChan <- rxMsg{Data: result} }()
			m.handleCommand(req)
			if err := <-req.errChan; err == nil || !strings.Contains(err.Error(), result) {
				t.Fatalf("result %q: %v", result, err)
			}
			if port.writes.Load() != 1 {
				t.Fatal("terminal result must not time out and send ESC")
			}
		})
	}
	if isVoiceCommandFailure("AT+CMGS=10", "NO CARRIER") || isVoiceCommandFailure("ATD*99#", "NO CARRIER") {
		t.Fatal("non-voice transactions changed")
	}
}

func TestVoiceWakeupsCoalesceAndUnsubscribe(t *testing.T) {
	m := newRunningTestManager(t)
	first, cancel := m.SubscribeVoiceChanges()
	second, cancelSecond := m.SubscribeVoiceChanges()
	defer cancelSecond()
	m.notifyVoiceURC("RING")
	m.notifyVoiceURC("+CLIP: \"10010\",129")
	if len(first) != 1 || len(second) != 1 {
		t.Fatal("each subscriber must get one pending refresh")
	}
	<-first
	<-second
	cancel()
	cancel()
	if _, ok := <-first; ok {
		t.Fatal("canceled subscription remains open")
	}
	m.notifyVoiceURC("+CMTI: \"ME\",1")
	if len(second) != 0 {
		t.Fatal("SMS must not generate call state updates")
	}
	m.notifyVoiceURC("NO CARRIER")
	if len(second) != 1 {
		t.Fatal("other subscribers lost after unsubscribe")
	}
}

func TestVoiceSubscribeConcurrentDispatch(t *testing.T) {
	m := newRunningTestManager(t)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 100 {
				_, cancel := m.SubscribeVoiceChanges()
				m.notifyVoiceURC("RING")
				cancel()
			}
		})
	}
	workers.Wait()
}

func TestVoiceSubscriptionsCloseOnStop(t *testing.T) {
	m := newRunningTestManager(t)
	changes, unsubscribe := m.SubscribeVoiceChanges()
	m.Stop()
	if _, open := <-changes; open {
		t.Fatal("subscription remains open after manager stops")
	}
	unsubscribe()
	late, cancel := m.SubscribeVoiceChanges()
	defer cancel()
	if _, open := <-late; open {
		t.Fatal("stopped manager accepted subscription")
	}
}
