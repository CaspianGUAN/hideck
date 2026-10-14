package volte

import (
	"context"
	"errors"
	"testing"

	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
)

func TestMuxLimitsEveryOutboundModeButNotInbound(t *testing.T) {
	for _, mode := range []string{"wifi", "volte", "modem"} {
		t.Run(mode, func(t *testing.T) {
			backend := &stubBackend{name: mode}
			denied := errors.New("rate limited")
			checks := 0
			mux := &Mux{IMS: backend, Native: backend, Modem: backend,
				IsNative:   func(string) bool { return mode == "volte" },
				IsModem:    func(string) bool { return mode == "modem" },
				BeforeDial: func(context.Context, string, string) error { checks++; return denied },
			}
			if _, err := mux.BeginCall(context.Background(), voicehost.BeginCallRequest{DeviceID: "dev"}); !errors.Is(err, denied) {
				t.Fatal(err)
			}
			if backend.last != "" {
				t.Fatal("backend dialed despite limit")
			}
			if _, err := mux.AnswerIncomingCall(context.Background(), voicehost.AnswerRequest{DeviceID: "dev"}); err != nil {
				t.Fatal(err)
			}
			if checks != 1 {
				t.Fatalf("inbound consumed quota: checks=%d", checks)
			}
		})
	}
}
