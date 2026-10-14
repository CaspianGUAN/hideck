package device

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yibaiba/hideck/internal/cardpolicy"
	"github.com/yibaiba/hideck/internal/volte"
)

func TestNativeStartReplacementSupersedesStaleIdentity(t *testing.T) {
	for _, change := range []string{"worker", "sim"} {
		t.Run(change, func(t *testing.T) {
			p, port := testModemVoicePort(t)
			port.worker.Config.PhoneMode = PhoneModeVoLTE
			old := p.beginNativeVoLTESchedule(port.worker.ID)
			if change == "worker" {
				nextWorker := &Worker{ID: port.worker.ID, Config: port.worker.Config}
				nextWorker.state.Identity.ICCID = port.iccid
				nextWorker.state.Identity.IMSI = "460011234567890"
				p.mu.Lock()
				p.workers[nextWorker.ID] = nextWorker
				p.mu.Unlock()
			} else {
				port.worker.state.Identity.ICCID = "new-sim"
			}
			next := p.beginNativeVoLTESchedule(port.worker.ID)
			if next == nil || !errors.Is(old.ctx.Err(), context.Canceled) {
				t.Fatal("new identity did not replace and cancel the stale request")
			}
			defer p.endNativeVoLTESchedule(port.worker.ID, next)
			p.endNativeVoLTESchedule(port.worker.ID, old)
			if change == "worker" {
				p.cancelNativeVoLTEWorkerStart(port.worker)
			}
			if err := p.validateNativeVoLTEStart(port.worker.ID, next); err != nil {
				t.Fatal("old completion/removal interfered with replacement", err)
			}
		})
	}
}

func TestNativeStartLatestPolicyWaitsForOlderHandoff(t *testing.T) {
	p, port := testModemVoicePort(t)
	finish := p.beginNativeVoLTETransition(port.worker.ID)
	p.SetPolicyResolver(&stubPolicyResolver{pol: cardpolicy.Policy{
		ICCID: port.iccid, PhoneMode: PhoneModeVoLTE, VoWiFiEnabled: true,
	}})
	h := &nativeStartEffectHost{started: make(chan struct{})}
	p.volteCtl = volte.NewControllerWithBackup(h, t.TempDir())
	result := p.resolveAndApplyPolicy(port.worker, "newer_native_request")
	if !result.Applied {
		finish()
		t.Fatalf("policy was not applied: %+v", result)
	}
	// The older projection can still finish its native teardown after the
	// newer policy queued a start. It must only stop the old native session.
	p.stopNativeVoLTE(port.worker.ID, "older_transition_tail")
	if h.starts.Load() != 0 {
		t.Fatal("queued native request ran before handoff completed")
	}
	finish()
	select {
	case <-h.started:
	case <-time.After(time.Second):
		t.Fatal("latest native intent was lost after handoff")
	}
}

func TestNativeStartQueuedIntentRechecksLatestPolicy(t *testing.T) {
	for _, change := range []string{"disabled", "modem_voice", "airplane", "sim", "pool_stop"} {
		t.Run(change, func(t *testing.T) {
			p, port := testModemVoicePort(t)
			port.worker.Config.PhoneMode = PhoneModeVoLTE
			finish := p.beginNativeVoLTETransition(port.worker.ID)
			start := p.beginNativeVoLTESchedule(port.worker.ID)
			defer p.endNativeVoLTESchedule(port.worker.ID, start)
			h := &nativeStartEffectHost{}
			p.volteCtl = volte.NewControllerWithBackup(h, t.TempDir())
			done := make(chan error, 1)
			go func() { done <- p.enableNativeVoLTE(port.worker.ID, start) }()
			switch change {
			case "disabled":
				port.worker.Config.VoWiFiEnabled = false
			case "modem_voice":
				port.worker.Config.PhoneMode = PhoneModeModemVoice
			case "airplane":
				port.worker.Config.AirplaneEnabled = true
			case "sim":
				port.worker.state.Identity.ICCID = "new-sim"
			case "pool_stop":
				p.cancel()
			}
			finish()
			select {
			case err := <-done:
				if err == nil || h.starts.Load() != 0 {
					t.Fatal("queued request ignored the latest ownership/policy", err)
				}
			case <-time.After(time.Second):
				t.Fatal("queued request remained blocked")
			}
		})
	}
}

func TestNativeStartOverlappingTransitionsPreserveOnlyLatestIntent(t *testing.T) {
	p, port := testModemVoicePort(t)
	firstDone := p.beginNativeVoLTETransition(port.worker.ID)
	old := p.beginNativeVoLTESchedule(port.worker.ID)
	secondDone := p.beginNativeVoLTETransition(port.worker.ID)
	if old.ctx.Err() != context.Canceled {
		t.Fatal("new transition did not cancel the superseded intent")
	}
	latest := p.beginNativeVoLTESchedule(port.worker.ID)
	defer p.endNativeVoLTESchedule(port.worker.ID, latest)
	firstDone()
	select {
	case <-latest.ready:
		t.Fatal("one transition released a request while another was active")
	default:
	}
	p.endNativeVoLTESchedule(port.worker.ID, old)
	secondDone()
	if err := latest.waitTransition(); err != nil {
		t.Fatal("latest intent lost", err)
	}
}
