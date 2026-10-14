package mbn

import (
	"context"
	"errors"
	"testing"
)

func TestPolicyReappliesAfterReturningToSIM(t *testing.T) {
	s, f := testService(t)
	ctx := context.Background()
	policy := Policy{Key: "sim:" + f.owner.ICCID, Mode: "fixed", Model: f.owner.Model, Firmware: f.owner.Firmware, Profile: "B"}
	if err := s.SavePolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if pending, err := s.Reconcile(ctx, f.owner); !pending || err != nil {
		t.Fatalf("first pending=%v err=%v", pending, err)
	}
	s.Wait()
	if f.active != "B" {
		t.Fatalf("first did not apply: %s", f.active)
	}
	// Another SIM used A, then the original SIM returns with a new identity generation.
	f.owner.IdentityGeneration += 2
	f.active, f.selected = "A", "A"
	pending, err := s.Reconcile(ctx, f.owner)
	if err != nil || !pending {
		t.Fatalf("returning SIM cannot reapply: pending=%v err=%v", pending, err)
	}
	s.Wait()
	if f.active != "B" {
		t.Fatalf("returned SIM retained wrong profile %s", f.active)
	}
}

func TestPolicyFailureDoesNotRepeatInSameSession(t *testing.T) {
	s, f := testService(t)
	ctx := context.Background()
	f.rebootErr = errors.New("reboot not confirmed")
	if err := s.SavePolicy(ctx, Policy{Key: "sim:" + f.owner.ICCID, Mode: "fixed", Model: f.owner.Model, Firmware: f.owner.Firmware, Profile: "B"}); err != nil {
		t.Fatal(err)
	}
	if pending, err := s.Reconcile(ctx, f.owner); err != nil || !pending {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	s.Wait()
	for i := 0; i < 3; i++ {
		if pending, err := s.Reconcile(ctx, f.owner); pending || err == nil {
			t.Fatalf("failed attempt not reported: pending=%v err=%v", pending, err)
		}
	}
	jobs, err := s.Operations(ctx, "d")
	if err != nil || len(jobs) != 1 || f.reboots != 1 {
		t.Fatalf("unexpected retries: jobs=%d reboots=%d err=%v", len(jobs), f.reboots, err)
	}
}

func TestPolicyReappliesAfterServiceRestart(t *testing.T) {
	s, f := testService(t)
	ctx := context.Background()
	if err := s.SavePolicy(ctx, Policy{Key: "sim:" + f.owner.ICCID, Mode: "fixed", Model: f.owner.Model, Firmware: f.owner.Firmware, Profile: "B"}); err != nil {
		t.Fatal(err)
	}
	if pending, err := s.Reconcile(ctx, f.owner); !pending || err != nil {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	s.Wait()
	f.active, f.selected = "A", "A" // Another card was used while HiDeck was stopped.
	restarted := New(Options{Context: s.ctx, Store: s.store, Files: s.files, Gateway: s.gateway})
	t.Cleanup(restarted.Wait)
	if pending, err := restarted.Reconcile(ctx, f.owner); !pending || err != nil {
		t.Fatalf("old process request reused: pending=%v err=%v", pending, err)
	}
	restarted.Wait()
	if f.active != "B" || f.reboots != 2 {
		t.Fatalf("profile=%s reboots=%d", f.active, f.reboots)
	}
}

type deadlineRegistrationSession struct {
	*testSession
	polls              int
	lastCandidateReady bool
}

func (f *deadlineRegistrationSession) Registration(ctx context.Context) (Registration, error) {
	if f.lastCandidateReady && f.active == "A" {
		return Registration{Registered: true}, nil
	}
	f.polls++
	if f.polls >= 2 {
		<-ctx.Done()
		return Registration{}, ctx.Err()
	}
	return Registration{}, nil
}

func TestObservationTimeoutCanReachHealthyCandidate(t *testing.T) {
	s, f := testService(t)
	s.gateway = registrationTestGateway{&deadlineRegistrationSession{testSession: f, lastCandidateReady: true}}
	op, err := s.Start(context.Background(), "d", Request{RequestID: "trial", Kind: "trial", Candidates: []string{"B", "A"}, RegistrationSeconds: 1, StabilitySeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	result := waitOperation(t, s, op.ID)
	if result.Status != "succeeded" || len(result.Results) != 2 || result.Results[0].Passed || !result.Results[1].Passed || f.active != "A" {
		t.Fatalf("healthy candidate not reached after observation timeout: %+v", result)
	}
}

type registrationTestGateway struct{ session Session }

func (g registrationTestGateway) Open(context.Context, string, bool) (Session, error) {
	return g.session, nil
}
func (g registrationTestGateway) Identity(context.Context, string) (Owner, error) {
	return g.session.Owner(), nil
}

func TestObservationTimeoutStillTriesNextCandidate(t *testing.T) {
	s, f := testService(t)
	s.gateway = registrationTestGateway{&deadlineRegistrationSession{testSession: f}}
	op, err := s.Start(context.Background(), "d", Request{RequestID: "trial", Kind: "trial", Candidates: []string{"B", "A"}, RegistrationSeconds: 1, StabilitySeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	result := waitOperation(t, s, op.ID)
	if len(result.Results) != 2 {
		t.Fatalf("observation deadline prematurely aborted remaining candidates: %+v", result)
	}
	if result.Status != "failed" || result.Stage != "restored" || f.active != "A" {
		t.Fatalf("exhausted candidates were not restored: %+v", result)
	}
}

type failedRegistrationSession struct {
	*testSession
	err error
}

func (f failedRegistrationSession) Registration(context.Context) (Registration, error) {
	return Registration{}, f.err
}

func TestTrialDoesNotAdvanceAfterRegistrationFailure(t *testing.T) {
	for _, failure := range []error{ErrOwnerChanged, context.Canceled, context.DeadlineExceeded, errors.New("transport disconnected")} {
		t.Run(failure.Error(), func(t *testing.T) {
			s, f := testService(t)
			s.gateway = registrationTestGateway{failedRegistrationSession{testSession: f, err: failure}}
			op, err := s.Start(context.Background(), "d", Request{RequestID: "failure", Kind: "trial", Candidates: []string{"B", "A"}})
			if err != nil {
				t.Fatal(err)
			}
			result := waitOperation(t, s, op.ID)
			if len(result.Results) != 1 || result.Status == "succeeded" {
				t.Fatalf("fatal failure advanced the trial: %+v", result)
			}
		})
	}
}
