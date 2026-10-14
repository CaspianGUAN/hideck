package modem

import (
	"strings"
	"testing"
	"time"
)

func TestADBAuthLoggingRedactsQueryAndSubmission(t *testing.T) {
	for _, command := range []string{"AT+QADBKEY?", `AT+QADBKEY="test-key"`, " at+qadbkey? "} {
		if strings.Contains(logATCommand(command), "test-key") {
			t.Fatal("key exposed")
		}
		if strings.Contains(logATResponse(command, "test-key\nERROR"), "test-key") {
			t.Fatal("response exposed")
		}
		if strings.Contains(logATResponse(command, "12345678\nOK"), "12345678") {
			t.Fatal("challenge exposed")
		}
	}
	if logATCommand("AT+CLCC") != "AT+CLCC" || logATResponse("AT+CLCC", "ERROR") != "ERROR" {
		t.Fatal("unrelated diagnostics changed")
	}
}

func TestADBAuthRejectedResponseRedactsEcho(t *testing.T) {
	m := newRunningTestManager(t)
	m.port = &timeoutSerialPort{}
	req := commandRequest{cmd: `AT+QADBKEY="test-key"`, timeout: time.Second, silent: true,
		respChan: make(chan string, 1), errChan: make(chan error, 1)}
	go func() {
		m.rxChan <- rxMsg{Data: req.cmd}
		m.rxChan <- rxMsg{Data: "ERROR"}
	}()
	m.handleCommand(req)
	err := <-req.errChan
	if err == nil || strings.Contains(err.Error(), "test-key") || !strings.Contains(err.Error(), "redacted") {
		t.Fatalf("authorization error was not redacted: %v", err)
	}
}

func TestLateADBAuthResponseIsRedactedOutsideOriginalTransaction(t *testing.T) {
	m := newRunningTestManager(t)
	for _, line := range []string{`AT+QADBKEY="test-key"`, "+QADBKEY: 12345678", "12345678"} {
		if got := logATResponse("AT+CSQ", line+" | ERROR"); !strings.Contains(got, "redacted") {
			t.Fatal(got)
		}
		formatted := m.formatURC(line)
		if formatted.Key != "QADBKEY" || len(formatted.Fields) != 0 || !strings.Contains(formatted.Msg, "redacted") {
			t.Fatal("late authorization appeared in URC log", formatted)
		}
	}
}
