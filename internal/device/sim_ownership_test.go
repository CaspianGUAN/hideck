package device

import (
	"context"
	"testing"
)

func TestPhysicalSIMRefreshRetainsRoundTripGeneration(t *testing.T) {
	be := &workerStartupIdentityBackendStub{liveICCID: "card-a", liveIMSI: "234150000000001"}
	w := &Worker{Backend: be}
	if err := w.RefreshIdentityLive(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	first := currentSIMOwnership(w)
	for _, id := range []string{"card-b", "card-a"} {
		be.liveICCID = id
		if err := w.RefreshIdentityLive(context.Background(), "test"); err != nil {
			t.Fatal(err)
		}
	}
	last := currentSIMOwnership(w)
	if first.iccid != last.iccid || first.generation+2 != last.generation {
		t.Fatalf("physical SIM round trip lost ownership: first=%+v last=%+v", first, last)
	}
	if err := w.RefreshIdentityLive(context.Background(), "stable"); err != nil {
		t.Fatal(err)
	}
	if currentSIMOwnership(w) != last {
		t.Fatal("unchanged SIM refresh invalidated the current owner")
	}
}

func TestSIMRoundTripRetiresNativeAndModemVoiceOwnership(t *testing.T) {
	p, port := testModemVoicePort(t)
	w := port.worker
	w.Config.PhoneMode = PhoneModeVoLTE
	old := p.beginNativeVoLTESchedule(w.ID)
	defer p.endNativeVoLTESchedule(w.ID, old)
	w.BeginSIMIdentityTransition("card-b", "switch")
	w.restoreSIMIdentityAfterTransitionFailure("card-b", "234150000000001", "ready")
	w.BeginSIMIdentityTransition(port.iccid, "switch_back")
	w.restoreSIMIdentityAfterTransitionFailure(port.iccid, "460011234567890", "ready")
	if err := p.validateNativeVoLTEStart(w.ID, old); err == nil {
		t.Fatal("old native task accepted the same ICCID after a round trip")
	}
	if err := port.checkIdentity(context.Background()); err == nil {
		t.Fatal("old modem call session accepted the same ICCID after a round trip")
	}
	next := p.beginNativeVoLTESchedule(w.ID)
	if next == nil {
		t.Fatal("new SIM generation was incorrectly deduplicated")
	}
	defer p.endNativeVoLTESchedule(w.ID, next)
	if err := p.validateNativeVoLTEStart(w.ID, next); err != nil {
		t.Fatal("new SIM generation cannot start", err)
	}
	w.Config.PhoneMode = PhoneModeModemVoice
	sim := currentSIMOwnership(w)
	freshPort := &modemVoicePort{pool: p, worker: w, iccid: sim.iccid, identityGeneration: sim.generation}
	if err := freshPort.check(context.Background()); err != nil {
		t.Fatal("new modem session rejected", err)
	}
}
