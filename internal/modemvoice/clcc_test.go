package modemvoice

import (
	"fmt"
	"testing"
)

func TestCLCCStatesAndInternationalNumber(t *testing.T) {
	for state := Active; state <= Waiting; state++ {
		response := fmt.Sprintf("AT+CLCC\r\n+CLCC: 1,1,%d,0,0,\"447700900123\",145,\"Name, with comma\"\r\nOK", state)
		calls, err := ParseCLCC(response)
		if err != nil || len(calls) != 1 {
			t.Fatalf("state %d: %v %v", state, calls, err)
		}
		if calls[0].State != state || !calls[0].Inbound || calls[0].Number != "+447700900123" {
			t.Fatalf("state %d: %+v", state, calls[0])
		}
	}
}

func TestCLCCRejectsPartialOrFailedSnapshot(t *testing.T) {
	for _, response := range []string{
		"ERROR", "+CME ERROR: 3", "NO CARRIER", "+CLCC: 1,1,0",
		"+CLCC: 1,1,9,0,0", "+CLCC: 0,1,0,0,0", "+CLCC: 1,2,0,0,0",
		"+CLCC: 1,1,0,3,0", "+CLCC: 1,1,0,0,2", "+CLCC: 1,1,0,0,0,\"123\"",
		"+CLCC: 1,1,0,0,0,\"123\",999", "+CLCC: 1,1,0,0,0\n+CLCC: 1,1,0,0,0",
		"+CLCC: 1,1,0,0,0\nERROR", "+CLCC: 1,1,0,0,0,\"unterminated,145",
	} {
		if calls, err := ParseCLCC(response); err == nil || calls != nil {
			t.Fatalf("accepted invalid response %q: %+v %v", response, calls, err)
		}
	}
}

func TestCLCCEmptyAndNonVoiceCalls(t *testing.T) {
	for _, response := range []string{"", "OK\r\n", "AT+CLCC\r\nOK"} {
		calls, err := ParseCLCC(response)
		if err != nil || len(calls) != 0 {
			t.Fatalf("%q: %+v %v", response, calls, err)
		}
	}
	calls, err := ParseCLCC("+CLCC: 1,0,0,1,0\n+CLCC: 2,1,0,2,0")
	if err != nil || len(calls) != 2 || calls[0].Mode != 1 || calls[1].Mode != 2 {
		t.Fatalf("data/fax calls must remain visible: %+v %v", calls, err)
	}
}

func TestTrackerKeepsStateOnErrorAndReplacesReusedIndex(t *testing.T) {
	tracker := NewTracker()
	first, err := tracker.Apply("+CLCC: 1,1,4,0,0,\"10010\",129")
	if err != nil || len(first) != 1 {
		t.Fatalf("first: %+v %v", first, err)
	}
	id := first[0].Call.ID
	if changes, err := tracker.Apply("+CLCC: 1,1,4,0,0,\"10010\",129"); err != nil || len(changes) != 0 {
		t.Fatalf("duplicate: %+v %v", changes, err)
	}
	if _, err := tracker.Apply("ERROR"); err == nil || len(tracker.Calls()) != 1 {
		t.Fatal("failed poll must preserve active call")
	}
	changes, err := tracker.Apply("+CLCC: 1,1,0,0,0")
	if err != nil || len(changes) != 1 || changes[0].Call.ID != id || changes[0].Call.Call.Number != "10010" {
		t.Fatalf("answer without number: %+v %v", changes, err)
	}
	changes, err = tracker.Apply("+CLCC: 1,1,4,0,0,\"10086\",129")
	if err != nil || len(changes) != 2 || !changes[0].Ended || changes[1].Call.ID == id {
		t.Fatalf("reused index: %+v %v", changes, err)
	}
	changes, err = tracker.Apply("OK")
	if err != nil || len(changes) != 1 || !changes[0].Ended || len(tracker.Calls()) != 0 {
		t.Fatalf("end: %+v %v", changes, err)
	}
}

func TestTrackerSeparatesSameNumberReusingActiveIndex(t *testing.T) {
	tracker := NewTracker()
	first, err := tracker.Apply("+CLCC: 1,0,0,0,0,\"10010\",129")
	if err != nil {
		t.Fatal(err)
	}
	changes, err := tracker.Apply("+CLCC: 1,0,2,0,0,\"10010\",129")
	if err != nil || len(changes) != 2 || !changes[0].Ended || changes[1].Call.ID == first[0].Call.ID {
		t.Fatalf("new setup reused old call identity: %+v %v", changes, err)
	}
}
