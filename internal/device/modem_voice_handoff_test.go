package device

import (
	"context"
	"errors"
	"testing"

	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
	"github.com/yibaiba/hideck/internal/cardpolicy"
	"github.com/yibaiba/hideck/internal/volte"
)

type failingHandoffHost struct {
	volte.Host
	failure error
}

// Stop provisioning before touching hardware; the real controller retains
// the failed native session, as it does after a runtime initialization error.
func (h *failingHandoffHost) StopSoftwareIMS(string) error { return h.failure }
func (h *failingHandoffHost) AudioDevice(string) string    { return "" }
func (h *failingHandoffHost) VOICEGetAllCallInfo(context.Context, string) (*qmi.VoiceAllCallInfo, error) {
	return nil, h.failure
}

func TestNativeHandoffFailureDoesNotProjectModemVoicePolicy(t *testing.T) {
	p, port := testModemVoicePort(t)
	port.worker.Config.PhoneMode = PhoneModeVoLTE
	failure := errors.New("native call query failed")
	p.volteCtl = volte.NewControllerWithBackup(&failingHandoffHost{failure: failure}, t.TempDir())
	if err := p.volteCtl.Enable(p.Context(), port.worker.ID); !errors.Is(err, failure) {
		t.Fatal("failed native session not installed", err)
	}
	p.SetPolicyResolver(&stubPolicyResolver{pol: cardpolicy.Policy{
		ICCID: port.iccid, PhoneMode: PhoneModeModemVoice, VoWiFiEnabled: true,
	}})
	result := p.resolveAndApplyPolicy(port.worker, "test_handoff")
	if result.Applied || result.Reason != "native_volte_stop_failed" || !errors.Is(result.Err, failure) {
		t.Fatalf("handoff failure lost: %+v", result)
	}
	if port.worker.Config.PhoneMode != PhoneModeVoLTE || !port.worker.Config.VoWiFiEnabled {
		t.Fatal("failed native cleanup changed effective phone policy")
	}
	if p.volteCtl.Status(port.worker.ID).Phase == volte.PhaseIdle {
		t.Fatal("failed native cleanup discarded session")
	}
}
