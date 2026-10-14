package device

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yibaiba/hideck/internal/cardpolicy"
	"github.com/yibaiba/hideck/internal/vowifihost"
)

type delayedOwnerPolicy struct {
	entered, release chan struct{}
}

func (r *delayedOwnerPolicy) Resolve(id string) (cardpolicy.Policy, error) {
	close(r.entered)
	<-r.release
	return cardpolicy.Policy{ICCID: id, PhoneMode: PhoneModeWiFi, AirplaneEnabled: true}, nil
}

func TestRetiredPolicyCannotAffectReplacementDeviceOrSIM(t *testing.T) {
	for _, change := range []string{"worker", "sim", "sim_round_trip", "esim_prepare", "esim_failed_same_sim"} {
		t.Run(change, func(t *testing.T) {
			p, port := testModemVoicePort(t)
			w := port.worker
			resolver := &delayedOwnerPolicy{entered: make(chan struct{}), release: make(chan struct{})}
			p.SetPolicyResolver(resolver)
			var once sync.Once
			release := func() { once.Do(func() { close(resolver.release) }) }
			defer release()
			done := make(chan policyApplyResult, 1)
			go func() { done <- p.resolveAndApplyPolicy(w, "identity_ready") }()
			waitPolicyOperation(t, resolver.entered)
			replacePolicyOwnerForTest(t, p, change)
			host := p.voWiFiHost()
			claim := host.BeginStart(w.ID)
			if !claim.Accepted {
				t.Fatal("replacement startup rejected")
			}
			stopped := false
			host.SetLifecycleRunForTest(func(context.Context, vowifihost.LifecycleCommand) error {
				stopped = true
				return errors.New("unexpected stale policy effect")
			})
			release()
			result := <-done
			if result.Applied || !errors.Is(result.Err, errPolicyOwnerChanged) {
				t.Fatalf("old policy not rejected explicitly: %+v", result)
			}
			if stopped || !host.ShouldRun(w.ID, claim.Epoch) || w.Config.PhoneMode != PhoneModeModemVoice {
				t.Fatal("old policy changed replacement runtime or configuration")
			}
		})
	}
}

func replacePolicyOwnerForTest(t *testing.T, p *Pool, change string) {
	t.Helper()
	w := p.GetWorker("d")
	switch change {
	case "worker":
		if err := p.AbandonDevice(w.ID); err != nil {
			t.Fatal(err)
		}
		replacement := &Worker{ID: w.ID, Config: w.Config}
		replacement.state.Identity = w.state.Identity // Same ICCID, different Worker.
		if err := p.registerWorkerStarting(replacement); err != nil {
			t.Fatal(err)
		}
	case "sim", "sim_round_trip":
		w.BeginSIMIdentityTransition("new-sim", "test_swap")
		w.restoreSIMIdentityAfterTransitionFailure("new-sim", "234150000000001", "test_ready")
		if change == "sim_round_trip" {
			w.BeginSIMIdentityTransition("test-sim", "test_swap_back")
			w.restoreSIMIdentityAfterTransitionFailure("test-sim", "460011234567890", "test_ready")
		}
	case "esim_prepare", "esim_failed_same_sim":
		p.beginESIMSwitch(w.ID, "new-sim")
		if change == "esim_failed_same_sim" {
			p.clearESIMSwitch(w.ID)
		}
	}
}

func TestPolicyEffectsSerializeWithRemovalAndESIMSwitch(t *testing.T) {
	for _, operation := range []string{"remove", "esim_switch"} {
		t.Run(operation, func(t *testing.T) {
			p, port := testModemVoicePort(t)
			w := port.worker
			p.SetPolicyResolver(&stubPolicyResolver{pol: cardpolicy.Policy{PhoneMode: PhoneModeWiFi, AirplaneEnabled: true}})
			p.voWiFiHost().BeginStart(w.ID)
			entered, unblock := make(chan struct{}), make(chan struct{})
			var effectOnce, unblockOnce sync.Once
			release := func() { unblockOnce.Do(func() { close(unblock) }) }
			defer release()
			p.voWiFiHost().SetLifecycleRunForTest(func(context.Context, vowifihost.LifecycleCommand) error {
				effectOnce.Do(func() { close(entered); <-unblock })
				return nil
			})
			applied := make(chan policyApplyResult, 1)
			go func() { applied <- p.resolveAndApplyPolicy(w, "test") }()
			waitPolicyOperation(t, entered)
			completed := make(chan error, 1)
			go func() {
				if operation == "remove" {
					completed <- p.RemoveWorker(w.ID)
				} else {
					p.beginESIMSwitch(w.ID, "next-sim")
					completed <- nil
				}
			}()
			otherDone := make(chan struct{})
			go func() {
				p.beginESIMSwitch("other", "other-sim")
				close(otherDone)
			}()
			waitPolicyOperation(t, otherDone)
			select {
			case err := <-completed:
				t.Fatalf("ownership changed during old policy effects: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			release()
			if result := <-applied; !result.Applied {
				t.Fatalf("current owner policy failed: %+v", result)
			}
			if err := <-completed; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPolicyRechecksSIMAfterSlowRuntimeStop(t *testing.T) {
	p, port := testModemVoicePort(t)
	w := port.worker
	p.SetPolicyResolver(&stubPolicyResolver{pol: cardpolicy.Policy{PhoneMode: PhoneModeWiFi, AirplaneEnabled: true}})
	p.voWiFiHost().BeginStart(w.ID)
	p.voWiFiHost().SetLifecycleRunForTest(func(context.Context, vowifihost.LifecycleCommand) error {
		w.BeginSIMIdentityTransition("new-sim", "physical_swap")
		w.restoreSIMIdentityAfterTransitionFailure("new-sim", "234150000000001", "test_ready")
		return nil
	})
	result := p.resolveAndApplyPolicy(w, "test")
	if !errors.Is(result.Err, errPolicyOwnerChanged) || w.Config.PhoneMode != PhoneModeModemVoice {
		t.Fatalf("old policy was applied after SIM changed during teardown: %+v", result)
	}
}

func waitPolicyOperation(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("policy transition blocked")
	}
}

func TestPolicyAcceptsConfirmedESIMTarget(t *testing.T) {
	p, port := testModemVoicePort(t)
	w := port.worker
	snapshot := p.beginESIMSwitch(w.ID, "new-sim")
	generation := w.BeginSIMIdentityTransition("new-sim", "esim_switch_begin")
	p.updateESIMSwitchIdentityGeneration(w.ID, snapshot.SwitchToken, generation)
	w.restoreSIMIdentityAfterTransitionFailure("new-sim", "234150000000001", "test_ready")
	p.SetPolicyResolver(&stubPolicyResolver{pol: cardpolicy.Policy{
		ICCID: "new-sim", PhoneMode: PhoneModeWiFi, AirplaneEnabled: true, APN: "new-card-apn",
	}})
	result := p.resolveAndApplyPolicy(w, "esim_switched")
	if !result.Applied || result.Err != nil || w.Config.APN != "new-card-apn" {
		t.Fatalf("confirmed target policy rejected: %+v", result)
	}
}
