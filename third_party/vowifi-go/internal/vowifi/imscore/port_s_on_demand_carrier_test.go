package imscore

import (
	"testing"
	"time"
)

// AIS drops an idle port-s connection after about 3.5 minutes; the binding
// stays valid, so a closure must not trigger a REGISTER (an iPhone waits).
func TestAISPortSClosureAwaitsPeerReconnect(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	service.cfg.CarrierPresetID = "ais_th_52003"

	service.handleProtectedServerPushClosed()
	time.Sleep(50 * time.Millisecond)

	if service.reRegisterPending.Load() || service.portSRecoveryPending.Load() {
		t.Fatal("AIS port-s closure triggered a REGISTER")
	}
	if !service.portSReconnectWaiting.Load() {
		t.Fatal("AIS port-s closure is not waiting for the peer to reconnect")
	}
	if service.protectedSMSPushRequiredLocked() {
		t.Fatal("AIS SMS readiness still requires a live port-s connection")
	}
}

func TestOtherCarrierPortSClosureStillRecovers(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	service.cfg.CarrierPresetID = "tmobile_us_310260"

	service.handleProtectedServerPushClosed()
	waitForPortSCondition(t, func() bool { return service.reRegisterPending.Load() })
}
