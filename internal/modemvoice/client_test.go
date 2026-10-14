package modemvoice

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type executorStub struct {
	commands []string
	response string
	err      error
}

func (s *executorStub) ExecuteATContext(_ context.Context, command string, _ time.Duration) (string, error) {
	s.commands = append(s.commands, command)
	if command == "AT+CLCC" {
		return s.response, s.err
	}
	return "", s.err
}

func TestDialPreservesNumberAndDoesNotClaimConnection(t *testing.T) {
	for _, number := range []string{"10010", "+447700900123", "07911123456"} {
		executor := &executorStub{}
		client, _ := NewClient(executor)
		if err := client.Dial(context.Background(), number); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(executor.commands, []string{"AT+CLCC", "ATD" + number + ";"}) {
			t.Fatalf("%q: %v", number, executor.commands)
		}
	}
}

func TestDialRejectsInjectionBeforeSerialAccess(t *testing.T) {
	for _, number := range []string{"", "+", "123;ATH", "123\r\nAT+CFUN=1", "tel:123", "12 34", "１２３", "12+34"} {
		executor := &executorStub{}
		client, _ := NewClient(executor)
		if err := client.Dial(context.Background(), number); err == nil || len(executor.commands) != 0 {
			t.Fatalf("invalid number reached serial: %q %v", number, executor.commands)
		}
	}
}

func TestFailedOrBusyPollNeverDials(t *testing.T) {
	for _, executor := range []*executorStub{
		{err: context.DeadlineExceeded}, {response: "ERROR"},
		{response: "+CLCC: 1,0,0,0,0"}, {response: "+CLCC: 1,0,0,9,0"},
	} {
		client, _ := NewClient(executor)
		if err := client.Dial(context.Background(), "10010"); err == nil || len(executor.commands) != 1 {
			t.Fatalf("dial after failed/busy poll: %v", executor.commands)
		}
	}
}

func TestWholeModemControlRequiresSingleExpectedCall(t *testing.T) {
	expected := Call{Index: 1, Inbound: true, State: Incoming, Number: "10010"}
	for _, response := range []string{
		"", "+CLCC: 1,1,4,0,0,\"10086\",129", "+CLCC: 1,1,4,0,1,\"10010\",129",
		"+CLCC: 1,1,4,0,0,\"10010\",129\n+CLCC: 2,1,5,0,0",
	} {
		executor := &executorStub{response: response}
		client, _ := NewClient(executor)
		if err := client.Hangup(context.Background(), expected); err == nil || len(executor.commands) != 1 {
			t.Fatalf("unsafe voice hangup: %q %v", response, executor.commands)
		}
	}
	executor := &executorStub{response: "+CLCC: 1,1,4,0,0,\"10010\",129"}
	client, _ := NewClient(executor)
	if err := client.Answer(context.Background(), expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(executor.commands, []string{"AT+CLCC", "ATA"}) {
		t.Fatal(executor.commands)
	}
}

func TestCanceledControlDoesNotQueue(t *testing.T) {
	executor := &executorStub{}
	client, _ := NewClient(executor)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.Dial(ctx, "10010"); !errors.Is(err, context.Canceled) || len(executor.commands) != 0 {
		t.Fatalf("canceled dial: %v %v", executor.commands, err)
	}
}
