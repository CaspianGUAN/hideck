package modem

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type notifyingVoicePort struct {
	*timeoutSerialPort
	wrote chan struct{}
}

func (p *notifyingVoicePort) Write(data []byte) (int, error) {
	n, err := p.timeoutSerialPort.Write(data)
	p.wrote <- struct{}{}
	return n, err
}

func TestVoiceQueueFenceWaitsForCanceledOnWireCommand(t *testing.T) {
	m := newRunningTestManager(t)
	port := &notifyingVoicePort{timeoutSerialPort: &timeoutSerialPort{}, wrote: make(chan struct{}, 2)}
	m.port = port
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dial := make(chan error, 1)
	go func() { _, err := m.ExecuteATContext(ctx, "ATD10010;", time.Second); dial <- err }()
	queueDone := make(chan struct{})
	go func() {
		defer close(queueDone)
		m.handleCommand(<-m.cmdChan)
		m.handleCommand(<-m.cmdChan)
	}()
	<-port.wrote
	cancel()
	if err := <-dial; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	idle := make(chan error, 1)
	go func() { idle <- m.WaitATIdle(context.Background()) }()
	select {
	case err := <-idle:
		t.Fatalf("fence completed before dial response: %v", err)
	default:
	}
	m.rxChan <- rxMsg{Data: "OK"}
	if err := <-idle; err != nil {
		t.Fatal(err)
	}
	<-queueDone
	if port.writes.Load() != 1 {
		t.Fatal("queue fence wrote an AT command")
	}
}

func TestCanceledVoiceQueueFenceDoesNotEnqueue(t *testing.T) {
	m := newRunningTestManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.WaitATIdle(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(m.cmdChan) != 0 {
		t.Fatal("canceled fence enqueued")
	}
}

func TestVoiceQueueFenceConcurrentManagerStop(t *testing.T) {
	m := newRunningTestManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var workers sync.WaitGroup
	workers.Go(func() {
		for range 1000 {
			_ = m.WaitATIdle(ctx)
		}
	})
	workers.Go(m.Stop)
	workers.Wait()
}
