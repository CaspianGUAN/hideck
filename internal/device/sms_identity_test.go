package device

import (
	"errors"
	"testing"
	"time"
)

type smsIdentityStoreStub struct {
	identities map[string]SMSIdentity
	lookupErr  error
	saveErr    error
	saved      []savedInboundSMS
}

type savedInboundSMS struct {
	identity SMSIdentity
	message  inboundSMSRecord
}

func (s *smsIdentityStoreStub) LookupDeviceIdentity(deviceID string) (SMSIdentity, bool, error) {
	if s.lookupErr != nil {
		return SMSIdentity{}, false, s.lookupErr
	}
	identity, ok := s.identities[deviceID]
	return identity, ok, nil
}

func (s *smsIdentityStoreStub) SaveReceived(identity SMSIdentity, message inboundSMSRecord) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = append(s.saved, savedInboundSMS{identity: identity, message: message})
	return nil
}

func smsTestPool(deviceID string, identity SMSIdentity) (*Pool, *smsIdentityStoreStub) {
	store := &smsIdentityStoreStub{identities: map[string]SMSIdentity{deviceID: identity}}
	return &Pool{workers: make(map[string]*Worker), smsIdentities: store}, store
}

func setWorkerSMSIdentity(worker *Worker, identity SMSIdentity) {
	worker.state.Identity.ICCID = identity.ICCID
	worker.state.Identity.IMSI = identity.IMSI
	worker.state.Identity.Ready = true
	worker.state.Identity.Phase = simIdentityPhaseReady
}

func TestResolveSMSIdentityUsesStoredBindingWithoutWorker(t *testing.T) {
	pool, _ := smsTestPool("offline-1", SMSIdentity{ICCID: "89440001", IMSI: "23410001"})

	identity, err := pool.ResolveSMSIdentity("offline-1")

	if err != nil {
		t.Fatalf("ResolveSMSIdentity() error=%v", err)
	}
	if identity.ICCID != "89440001" || identity.IMSI != "23410001" {
		t.Fatalf("identity=%+v", identity)
	}
}

func TestResolveSMSIdentityRejectsSIMSwapConflict(t *testing.T) {
	pool, store := smsTestPool("dev-1", SMSIdentity{ICCID: "old-card", IMSI: "old-imsi"})
	worker := &Worker{ID: "dev-1", Pool: pool}
	setWorkerSMSIdentity(worker, SMSIdentity{ICCID: "new-card", IMSI: "new-imsi"})
	pool.workers[worker.ID] = worker

	_, err := pool.ResolveSMSIdentity(worker.ID)

	if !errors.Is(err, ErrSMSIdentityConflict) {
		t.Fatalf("ResolveSMSIdentity() error=%v, want conflict", err)
	}
	if err := worker.processSMS("10086", "must stay", time.Now()); !errors.Is(err, ErrSMSIdentityConflict) {
		t.Fatalf("processSMS() error=%v, want conflict", err)
	}
	if len(store.saved) != 0 {
		t.Fatalf("saved=%v, want no cross-card insert", store.saved)
	}
}

func TestProcessSMSSameHistoricalIMSIKeepsDeviceICCID(t *testing.T) {
	store := &smsIdentityStoreStub{identities: map[string]SMSIdentity{
		"dev-a": {ICCID: "card-a", IMSI: "shared-imsi"},
		"dev-b": {ICCID: "card-b", IMSI: "shared-imsi"},
	}}
	pool := &Pool{workers: make(map[string]*Worker), smsIdentities: store}
	for _, deviceID := range []string{"dev-a", "dev-b"} {
		worker := &Worker{ID: deviceID, Pool: pool}
		setWorkerSMSIdentity(worker, store.identities[deviceID])
		pool.workers[deviceID] = worker
		if err := worker.processSMS("service", deviceID, time.Now()); err != nil {
			t.Fatalf("%s processSMS() error=%v", deviceID, err)
		}
	}
	if len(store.saved) != 2 || store.saved[0].identity.ICCID != "CARD-A" || store.saved[1].identity.ICCID != "CARD-B" {
		t.Fatalf("saved identities=%+v", store.saved)
	}
}

// Every modem plugged into the same slot shares the interface-name device ID
// as alias; the binding of the modem attached now must win over an older
// modem's binding found through the alias.
func TestResolveSMSIdentityPrefersAttachedModemBinding(t *testing.T) {
	pool, store := smsTestPool("dev-1", SMSIdentity{ICCID: "old-modem-card", IMSI: "old-imsi"})
	store.identities["imei-current"] = SMSIdentity{ICCID: "current-card", IMSI: "current-imsi"}
	worker := &Worker{ID: "dev-1", Pool: pool}
	setWorkerSMSIdentity(worker, SMSIdentity{ICCID: "current-card", IMSI: "current-imsi"})
	worker.state.Identity.IMEI = "imei-current"
	pool.workers[worker.ID] = worker

	identity, err := pool.ResolveSMSIdentity(worker.ID)

	if err != nil {
		t.Fatalf("ResolveSMSIdentity() error=%v, want the attached modem's binding", err)
	}
	if identity.ICCID != "CURRENT-CARD" {
		t.Fatalf("identity=%+v", identity)
	}
}

func TestResolveSMSIdentityStillRejectsSIMSwapOnAttachedModem(t *testing.T) {
	pool, store := smsTestPool("dev-1", SMSIdentity{ICCID: "unused"})
	store.identities["imei-current"] = SMSIdentity{ICCID: "previous-card", IMSI: "previous-imsi"}
	worker := &Worker{ID: "dev-1", Pool: pool}
	setWorkerSMSIdentity(worker, SMSIdentity{ICCID: "swapped-card", IMSI: "swapped-imsi"})
	worker.state.Identity.IMEI = "imei-current"
	pool.workers[worker.ID] = worker

	if _, err := pool.ResolveSMSIdentity(worker.ID); !errors.Is(err, ErrSMSIdentityConflict) {
		t.Fatalf("ResolveSMSIdentity() error=%v, want conflict for a real SIM swap", err)
	}
}
