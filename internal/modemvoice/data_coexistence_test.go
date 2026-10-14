package modemvoice

import (
	"context"
	"reflect"
	"testing"
)

const activeDataResponse = "+CLCC: 2,1,0,1,0,\"\",128"
const activeVoiceResponse = "+CLCC: 1,0,0,0,0,\"10010\",129"

func TestClientPreservesDataSessionDuringVoiceControl(t *testing.T) {
	p := &executorStub{response: activeDataResponse}
	c, _ := NewClient(p)
	if err := c.Dial(context.Background(), "10010"); err != nil {
		t.Fatal(err)
	}
	p.response += "\n" + activeVoiceResponse
	if err := c.Hangup(context.Background(), Call{Index: 1, State: Active, Number: "10010"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"AT+CLCC", "ATD10010;", "AT+CLCC", "AT+CHUP"}
	if !reflect.DeepEqual(p.commands, want) {
		t.Fatalf("commands = %v; want %v", p.commands, want)
	}
}

func TestSessionPreservesDataAndItsIdentity(t *testing.T) {
	p := newSessionPort()
	p.response = activeDataResponse
	s := newTestSession(t, p)
	if _, err := s.Dial(context.Background(), "10010"); err != nil {
		t.Fatal(err)
	}
	dataID := s.Calls()[0].ID
	p.response += "\n" + activeVoiceResponse
	if _, err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	voice := s.voiceControlCalls()
	if len(voice) != 1 {
		t.Fatalf("voice snapshot: %+v", voice)
	}
	if _, err := s.Hangup(context.Background(), voice[0].ID); err != nil {
		t.Fatal(err)
	}
	if p.commands[len(p.commands)-1] != "AT+CHUP" {
		t.Fatal(p.commands)
	}
	p.response = activeDataResponse
	if _, err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls := s.Calls(); len(calls) != 1 || calls[0].ID != dataID {
		t.Fatalf("data identity changed: %+v", calls)
	}
}

func TestSessionDoesNotTreatUnknownModeAsData(t *testing.T) {
	p := newSessionPort()
	p.response = "+CLCC: 2,1,0,9,0"
	s := newTestSession(t, p)
	if _, err := s.Dial(context.Background(), "10010"); err == nil {
		t.Fatal("unknown call mode allowed dialing")
	}
	if !reflect.DeepEqual(p.commands, []string{"AT+CLCC"}) {
		t.Fatal(p.commands)
	}
}

func TestPendingDataCallCannotBeAnsweredAsVoice(t *testing.T) {
	p := &executorStub{response: "+CLCC: 1,1,4,0,0,\"10010\",129\n+CLCC: 2,1,4,1,0"}
	c, _ := NewClient(p)
	err := c.Answer(context.Background(), Call{Index: 1, Inbound: true, State: Incoming, Number: "10010"})
	if err == nil || !reflect.DeepEqual(p.commands, []string{"AT+CLCC"}) {
		t.Fatalf("ambiguous answer issued: %v, %v", p.commands, err)
	}
}
