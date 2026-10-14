package device

import (
	"context"
	"errors"
	"testing"

	"github.com/yibaiba/hideck/internal/backend"
	"github.com/yibaiba/hideck/internal/cardpolicy"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/mbn"
)

type fixedMBNPolicyStore struct{ mbn.Store }

func (fixedMBNPolicyStore) Policies(context.Context) ([]mbn.Policy, error) {
	return []mbn.Policy{{Key: "sim:card", Mode: "fixed", Profile: "B", Model: "EC20F", Firmware: "expected"}}, nil
}

type unavailableMBNGateway struct{ onOpen func() }

func (unavailableMBNGateway) Identity(context.Context, string) (mbn.Owner, error) {
	return mbn.Owner{}, nil
}
func (g unavailableMBNGateway) Open(context.Context, string, bool) (mbn.Session, error) {
	if g.onOpen != nil {
		g.onOpen()
	}
	return nil, errors.New("MBN query unsupported on replacement modem")
}

func mbnPolicyTestDevice() (*Pool, *Worker, *workerStatusBackendStub) {
	p := &Pool{ctx: context.Background()}
	p.SetPolicyResolver(&stubPolicyResolver{pol: cardpolicy.Policy{ICCID: "card", PhoneMode: PhoneModeWiFi, AirplaneEnabled: true}})
	stub := &workerStatusBackendStub{opMode: backend.ModeOnline}
	w := &Worker{ID: "d", Pool: p, Backend: stub, Config: config.DeviceConfig{PhoneMode: PhoneModeVoLTE}, stop: make(chan struct{})}
	w.state.Identity.ICCID = "card"
	w.state.Identity.IMEI = "imei"
	w.state.Identity.IMSI = "234150000000001"
	w.state.Identity.Ready = true
	p.workers = map[string]*Worker{"d": w}
	p.SetMBNService(mbn.New(mbn.Options{Context: p.ctx, Store: fixedMBNPolicyStore{}, Gateway: unavailableMBNGateway{}}))
	return p, w, stub
}

func TestMBNFailureMustNotKeepPreviousSIMRadioPolicy(t *testing.T) {
	p, w, stub := mbnPolicyTestDevice()
	p.SetMBNService(mbn.New(mbn.Options{Context: p.ctx, Store: fixedMBNPolicyStore{}, Gateway: unavailableMBNGateway{onOpen: func() {
		if !w.Config.AirplaneEnabled || stub.opMode != backend.ModeRFOff {
			t.Error("MBN queried before applying the new SIM's RF restriction")
		}
	}}}))
	result := p.resolveAndApplyPolicy(w, "identity_ready")
	if !w.Config.AirplaneEnabled || !w.cellularRadioIsSuppressed() || stub.opMode != backend.ModeRFOff {
		t.Fatalf("new SIM RF-off policy skipped; previous mode=%s airplane=%v result=%+v", w.Config.PhoneMode, w.Config.AirplaneEnabled, result)
	}
	if result.Err == nil || result.Applied || result.Reason != "mbn_policy" {
		t.Fatalf("MBN failure was hidden: %+v", result)
	}
}

func TestMBNFailureDoesNotStartDependentCellularServices(t *testing.T) {
	p, w, stub := mbnPolicyTestDevice()
	w.Config = config.DeviceConfig{PhoneMode: PhoneModeWiFi, AirplaneEnabled: true}
	stub.opMode = backend.ModeRFOff
	p.SetPolicyResolver(&stubPolicyResolver{pol: cardpolicy.Policy{ICCID: "card", PhoneMode: PhoneModeVoLTE, VoWiFiEnabled: true, NetworkEnabled: true}})
	result := p.resolveAndApplyPolicy(w, "identity_ready")
	if result.Err == nil || result.Applied || w.Config.PhoneMode != PhoneModeVoLTE {
		t.Fatalf("policy or error lost: %+v config=%+v", result, w.Config)
	}
	assertNoRadioOnline(t, stub, "MBN failure")
}

func TestMBNInspectionRechecksPolicyAndSIM(t *testing.T) {
	for _, change := range []string{"policy", "sim"} {
		t.Run(change, func(t *testing.T) {
			p, w, stub := mbnPolicyTestDevice()
			p.SetMBNService(mbn.New(mbn.Options{Context: p.ctx, Store: fixedMBNPolicyStore{}, Gateway: unavailableMBNGateway{onOpen: func() {
				transition := p.policyTransitionFor(w.ID)
				if !transition.TryLock() {
					t.Error("inspection held transition lock; real gateway would deadlock")
					return
				}
				defer transition.Unlock()
				if change == "policy" {
					w.Config.PhoneMode = PhoneModeModemVoice
				} else {
					w.cacheMu.Lock()
					w.state.Identity.Generation++
					w.cacheMu.Unlock()
				}
			}}}))
			result := p.resolveAndApplyPolicy(w, "identity_ready")
			if result.Err == nil || result.Applied || result.Reason == "mbn_policy" {
				t.Fatalf("stale policy resumed: %+v", result)
			}
			assertNoRadioOnline(t, stub, "stale policy")
		})
	}
}
