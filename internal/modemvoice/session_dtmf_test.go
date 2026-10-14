package modemvoice

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

const activeDTMFCall = `+CLCC: 1,0,0,0,0,"10010",129`

func TestSessionDTMFSendsSingleKeyWithFreshCall(t *testing.T) {
	for _, digit := range "0123456789*#" {
		t.Run(string(digit), func(t *testing.T) {
			p := newSessionPort()
			p.response = activeDTMFCall + "\n+CLCC: 2,0,0,1,0"
			s := newTestSession(t, p)
			if _, err := s.Refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			update, err := s.DTMF(context.Background(), s.Calls()[0].ID, string(digit))
			if err != nil || !update.Attempted || !update.Accepted {
				t.Fatalf("%+v %v", update, err)
			}
			want := []string{"AT+CLCC", "AT+CLCC", `AT+VTS="` + string(digit) + `",3`}
			if !reflect.DeepEqual(p.commands, want) {
				t.Fatalf("commands=%v", p.commands)
			}
		})
	}
}

func TestSessionDTMFRejectsInvalidDigitsWithoutIO(t *testing.T) {
	for _, digit := range []string{"", "12", "A", "１", "1\r\nATH", `";ATH`, " "} {
		p := newSessionPort()
		s := newTestSession(t, p)
		if _, err := s.DTMF(context.Background(), "id", digit); err == nil || len(p.commands) != 0 {
			t.Fatalf("invalid digit %q reached modem: %v %v", digit, p.commands, err)
		}
	}
}

func TestSessionDTMFRejectsChangedOrInactiveCalls(t *testing.T) {
	for name, response := range map[string]string{
		"ended": "", "held": `+CLCC: 1,0,1,0,0,"10010",129`,
		"reused":       `+CLCC: 1,0,2,0,0,"10010",129`,
		"other_peer":   `+CLCC: 1,0,0,0,0,"10086",129`,
		"multiparty":   `+CLCC: 1,0,0,0,1,"10010",129`,
		"waiting":      activeDTMFCall + "\n+CLCC: 2,1,5,0,0",
		"invalid_poll": "ERROR",
	} {
		t.Run(name, func(t *testing.T) {
			p := newSessionPort()
			p.response = activeDTMFCall
			s := newTestSession(t, p)
			if _, err := s.Refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			id := s.Calls()[0].ID
			p.response = response
			update, err := s.DTMF(context.Background(), id, "1")
			if err == nil || update.Attempted || update.Accepted || len(p.commands) != 2 {
				t.Fatalf("unsafe DTMF: %+v %v commands=%v", update, err, p.commands)
			}
			if name == "ended" && (len(update.Changes) != 1 || !update.Changes[0].Ended) {
				t.Fatalf("missing terminal update: %+v", update)
			}
		})
	}
}

func TestSessionDTMFErrorNeverRetries(t *testing.T) {
	for _, failure := range []error{context.DeadlineExceeded, errors.New("ERROR"), errors.New("+CME ERROR: 4")} {
		p := newSessionPort()
		p.response, p.commandErr = activeDTMFCall, failure
		s := newTestSession(t, p)
		if _, err := s.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		update, err := s.DTMF(context.Background(), s.Calls()[0].ID, "#")
		if !errors.Is(err, failure) || update.Accepted || !update.Attempted || len(p.commands) != 3 {
			t.Fatalf("unexpected retry/success: %+v %v commands=%v", update, err, p.commands)
		}
	}
}

func TestSessionDTMFRejectsCanceledOrStaleOwnership(t *testing.T) {
	p := newSessionPort()
	p.response = activeDTMFCall
	s := newTestSession(t, p)
	if _, err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	id := s.Calls()[0].ID
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.DTMF(ctx, id, "1"); err == nil {
		t.Fatal("canceled DTMF accepted")
	}
	if _, err := s.DTMF(context.Background(), "retired-id", "1"); !errors.Is(err, ErrCallChanged) {
		t.Fatal(err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DTMF(context.Background(), id, "1"); !errors.Is(err, ErrSessionClosed) {
		t.Fatal(err)
	}
	if len(p.commands) != 1 {
		t.Fatalf("stale DTMF reached modem: %v", p.commands)
	}
}
