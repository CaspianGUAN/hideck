package device

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yibaiba/hideck/internal/mbn"
)

func TestMBNOwnershipExcludesOnlyItsDevice(t *testing.T) {
	p := &Pool{}
	a := p.policyTransitionFor("a")
	a.operations.Lock()
	s := &mbnDeviceSession{pool: p, owner: mbn.Owner{DeviceID: "a"}, writable: true}
	a.mbnSession.Store(s)
	if _, err := p.AcquireDeviceOperation("a"); !errors.Is(err, mbn.ErrConflict) {
		t.Fatal(err)
	}
	release, err := p.AcquireDeviceOperation("b")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	release, err = p.AcquireDeviceOperation("a")
	if err != nil {
		t.Fatal(err)
	}
	release()
}
func TestMBNMutationCannotOvertakeExistingDeviceOperation(t *testing.T) {
	p := &Pool{}
	release, err := p.AcquireDeviceOperation("a")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = (MBNGateway{Pool: p}).Open(ctx, "a", true)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestMBNWaitsForPolicyRequestToReleaseDevice(t *testing.T) {
	p := &Pool{}
	release, err := p.AcquireDeviceOperation("a")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		err := waitMBNExclusive(ctx, p.policyTransitionFor("a"))
		if err == nil {
			p.policyTransitionFor("a").operations.Unlock()
		}
		done <- err
	}()
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestMBNRejectsReplacedWorkerAndChangedSIMGeneration(t *testing.T) {
	w := &Worker{ID: "a", stop: make(chan struct{})}
	w.state.Identity = deviceIdentityState{IMEI: "imei", ICCID: "card", Generation: 2, Ready: true}
	p := &Pool{workers: map[string]*Worker{"a": w}}
	s := &mbnDeviceSession{pool: p, worker: w, writable: true, owner: mbn.Owner{DeviceID: "a", IMEI: "imei", ICCID: "card"}, identityGeneration: 2}
	task := p.policyTransitionFor("a")
	task.mbnSession.Store(s)
	if err := s.validateLocked(); err != nil {
		t.Fatal(err)
	}
	w.state.Identity.Generation++
	if err := s.validateLocked(); !errors.Is(err, mbn.ErrOwnerChanged) {
		t.Fatal(err)
	}
	w.state.Identity.Generation = 2
	p.workers["a"] = &Worker{ID: "a"}
	if err := s.validateLocked(); !errors.Is(err, mbn.ErrOwnerChanged) {
		t.Fatal(err)
	}
}
