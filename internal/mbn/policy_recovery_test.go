package mbn

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPolicyRestartDoesNotReplayUncertainWrite(t *testing.T) {
	s, f := testService(t)
	ctx := context.Background()
	saveRecoveryTestPolicy(t, s, f.owner)
	f.rebootErr = errors.New("reboot outcome unknown")
	if pending, err := s.Reconcile(ctx, f.owner); !pending || err != nil {
		t.Fatalf("first pending=%v err=%v", pending, err)
	}
	s.Wait()
	jobs, err := s.Operations(ctx, "d")
	if err != nil || len(jobs) != 1 || jobs[0].Status != "needs_attention" {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
	f.owner.Generation++
	f.owner.IdentityGeneration++
	f.owner.DeviceID = "re-added"
	restarted := restartRecoveryTestService(t, s)
	// Changing the saved policy must not implicitly confirm an uncertain write.
	saveRecoveryTestPolicy(t, restarted, f.owner)
	for i := 0; i < 3; i++ {
		if pending, err := restarted.Reconcile(ctx, f.owner); pending || !errors.Is(err, ErrNeedsAttention) {
			t.Fatalf("uncertain attempt replayed: pending=%v err=%v", pending, err)
		}
	}
	restarted.Wait()
	jobs, err = s.Operations(ctx, "")
	if err != nil || len(jobs) != 1 || f.reboots != 1 {
		t.Fatalf("jobs=%d reboots=%d err=%v", len(jobs), f.reboots, err)
	}
}

func TestRecoveredRunningOperationBlocksAutomaticPolicy(t *testing.T) {
	s, f := testService(t)
	saveRecoveryTestPolicy(t, s, f.owner)
	op := Operation{ID: "interrupted", DeviceID: "d", RequestID: "interrupted", ExpectedOwner: f.owner, Status: "running", Stage: "applying"}
	if err := s.store.SaveOperation(context.Background(), &op); err != nil {
		t.Fatal(err)
	}
	restarted := restartRecoveryTestService(t, s)
	if pending, err := restarted.Reconcile(context.Background(), f.owner); pending || !errors.Is(err, ErrNeedsAttention) {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	jobs, err := s.Operations(context.Background(), "")
	if err != nil || len(jobs) != 1 || jobs[0].Status != "needs_attention" || f.prepared != 0 || f.reboots != 0 {
		t.Fatalf("jobs=%+v prepared=%d reboots=%d err=%v", jobs, f.prepared, f.reboots, err)
	}
}

func TestRunningOperationBlocksPolicyAfterLogicalDeviceRename(t *testing.T) {
	s, f := testService(t)
	saveRecoveryTestPolicy(t, s, f.owner)
	op := Operation{ID: "running", DeviceID: "d", RequestID: "running", ExpectedOwner: f.owner, Status: "running"}
	if err := s.store.SaveOperation(context.Background(), &op); err != nil {
		t.Fatal(err)
	}
	f.owner.DeviceID = "re-added"
	f.owner.Generation++
	if pending, err := s.Reconcile(context.Background(), f.owner); pending || !errors.Is(err, ErrConflict) {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	if f.prepared != 0 || f.reboots != 0 {
		t.Fatalf("prepared=%d reboots=%d", f.prepared, f.reboots)
	}
}

func TestPolicyUncertaintyDoesNotTransferToOtherSIMOrModem(t *testing.T) {
	for _, identity := range []string{"sim", "modem"} {
		t.Run(identity, func(t *testing.T) {
			s, f := testService(t)
			oldOwner := f.owner
			if identity == "sim" {
				f.owner.ICCID = "other-card"
			} else {
				f.owner.IMEI = "other-modem"
			}
			saveRecoveryTestPolicy(t, s, f.owner)
			op := Operation{ID: "old", DeviceID: "d", RequestID: "old", ExpectedOwner: oldOwner, Status: "needs_attention"}
			if err := s.store.SaveOperation(context.Background(), &op); err != nil {
				t.Fatal(err)
			}
			if pending, err := s.Reconcile(context.Background(), f.owner); !pending || err != nil {
				t.Fatalf("unrelated identity blocked: pending=%v err=%v", pending, err)
			}
			s.Wait()
			if f.active != "B" || f.reboots != 1 {
				t.Fatalf("active=%s reboots=%d", f.active, f.reboots)
			}
		})
	}
}

func TestManualRecoveryAllowsPolicyAfterAnotherRestart(t *testing.T) {
	for _, kind := range []string{"apply", "restore"} {
		t.Run(kind, func(t *testing.T) {
			s, f := testService(t)
			ctx := context.Background()
			saveRecoveryTestPolicy(t, s, f.owner)
			f.rebootErr = errors.New("reboot not confirmed")
			if pending, err := s.Reconcile(ctx, f.owner); !pending || err != nil {
				t.Fatalf("pending=%v err=%v", pending, err)
			}
			s.Wait()
			jobs, err := s.Operations(ctx, "d")
			if err != nil || len(jobs) != 1 {
				t.Fatalf("jobs=%+v err=%v", jobs, err)
			}
			restarted := restartRecoveryTestService(t, s)
			f.rebootErr = nil
			request := Request{RequestID: "policy-manual-recovery", Kind: kind, Profile: "B", RestoreOperation: jobs[0].ID}
			op, err := restarted.Start(ctx, "d", request)
			if err != nil {
				t.Fatal(err)
			}
			if result := waitOperation(t, restarted, op.ID); result.Status != "succeeded" {
				t.Fatalf("manual recovery failed: %+v", result)
			} else if len(result.RecoveryOf) != 1 || result.RecoveryOf[0] != jobs[0].ID {
				t.Fatalf("recovery not tied to uncertain operation: %+v", result)
			}
			f.active, f.selected = "A", "A"
			again := restartRecoveryTestService(t, restarted)
			if pending, err := again.Reconcile(ctx, f.owner); !pending || err != nil {
				t.Fatalf("resolved history still blocked: pending=%v err=%v", pending, err)
			}
			again.Wait()
			if f.active != "B" {
				t.Fatalf("policy not applied: %s", f.active)
			}
			prior, err := s.Operation(ctx, jobs[0].ID)
			if err != nil || prior.Status != "needs_attention" {
				t.Fatalf("historical record altered: %+v err=%v", prior, err)
			}
		})
	}
}

func TestPolicyRecoveryRequiresExplicitVerifiedConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kind     string
		status   string
		stage    string
		unlinked bool
		noProof  bool
		otherSIM bool
		resolved bool
	}{
		{name: "unlinked success", kind: "apply", status: "succeeded", unlinked: true},
		{name: "file import", kind: "import", status: "succeeded"},
		{name: "failed apply", kind: "apply", status: "failed"},
		{name: "no verified snapshot", kind: "apply", status: "succeeded", noProof: true},
		{name: "different snapshot owner", kind: "apply", status: "succeeded", otherSIM: true},
		{name: "restored failed trial", kind: "trial", status: "failed", stage: "restored", resolved: true},
		{name: "restored cancelled trial", kind: "trial", status: "cancelled", stage: "restored", resolved: true},
		{name: "failed resume", kind: "apply", status: "needs_attention", stage: "resuming"},
		{name: "successful trial", kind: "trial", status: "succeeded", resolved: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := testService(t)
			ctx := context.Background()
			saveRecoveryTestPolicy(t, s, f.owner)
			uncertain := Operation{ID: "uncertain", DeviceID: "d", RequestID: "uncertain", ExpectedOwner: f.owner, Status: "needs_attention"}
			proof := Operation{ID: "proof", DeviceID: "d", RequestID: "proof", ExpectedOwner: f.owner, RecoveryOf: []string{uncertain.ID}, Request: Request{Kind: tc.kind}, Status: tc.status, Stage: tc.stage}
			proof.Current = Snapshot{Owner: f.owner, Profiles: []Profile{{Name: "A", Selected: true, Activated: true}}}
			if tc.unlinked {
				proof.RecoveryOf = nil
			}
			if tc.noProof {
				proof.Current = Snapshot{}
			}
			if tc.otherSIM {
				proof.Current.Owner.ICCID = "other"
			}
			for _, op := range []*Operation{&uncertain, &proof} {
				if err := s.store.SaveOperation(ctx, op); err != nil {
					t.Fatal(err)
				}
			}
			pending, err := s.Reconcile(ctx, f.owner)
			s.Wait()
			if tc.resolved {
				if !pending || err != nil || f.active != "B" {
					t.Fatalf("verified recovery rejected: pending=%v err=%v active=%s", pending, err, f.active)
				}
			} else if pending || !errors.Is(err, ErrNeedsAttention) || f.prepared != 0 || f.reboots != 0 {
				t.Fatalf("unresolved recovery accepted: pending=%v err=%v prepared=%d reboots=%d", pending, err, f.prepared, f.reboots)
			}
		})
	}
}

type recoveryErrorStore struct {
	Store
	err error
}

func (s recoveryErrorStore) Operations(context.Context, string) ([]Operation, error) {
	return nil, s.err
}

func TestPolicyDoesNotWriteWhenRecoveryHistoryCannotBeRead(t *testing.T) {
	s, f := testService(t)
	saveRecoveryTestPolicy(t, s, f.owner)
	failure := errors.New("operation history unavailable")
	s.store = recoveryErrorStore{Store: s.store, err: failure}
	if pending, err := s.Reconcile(context.Background(), f.owner); pending || !errors.Is(err, failure) {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	if f.prepared != 0 || f.reboots != 0 {
		t.Fatalf("prepared=%d reboots=%d", f.prepared, f.reboots)
	}
}

func TestRecoveryProofDoesNotResolveSubsequentUncertainty(t *testing.T) {
	s, f := testService(t)
	ctx := context.Background()
	saveRecoveryTestPolicy(t, s, f.owner)
	for _, op := range []Operation{
		{ID: "old", DeviceID: "d", RequestID: "old", ExpectedOwner: f.owner, Status: "needs_attention"},
		{ID: "verified", DeviceID: "d", RequestID: "verified", ExpectedOwner: f.owner, RecoveryOf: []string{"old"}, Request: Request{Kind: "apply"}, Status: "succeeded", Current: Snapshot{Owner: f.owner, AutoSel: true}},
		{ID: "new", DeviceID: "d", RequestID: "new", ExpectedOwner: f.owner, Status: "needs_attention"},
	} {
		if err := s.store.SaveOperation(ctx, &op); err != nil {
			t.Fatal(err)
		}
	}
	if pending, err := s.Reconcile(ctx, f.owner); pending || !errors.Is(err, ErrNeedsAttention) {
		t.Fatalf("new uncertainty ignored: pending=%v err=%v", pending, err)
	}
	if f.prepared != 0 || f.reboots != 0 {
		t.Fatalf("prepared=%d reboots=%d", f.prepared, f.reboots)
	}
}

func TestRecoveryProofDoesNotDependOnClockOrder(t *testing.T) {
	s, f := testService(t)
	ctx := context.Background()
	saveRecoveryTestPolicy(t, s, f.owner)
	for _, op := range []Operation{
		{ID: "old", DeviceID: "d", RequestID: "old", ExpectedOwner: f.owner, Status: "needs_attention"},
		{ID: "verified", DeviceID: "d", RequestID: "verified", ExpectedOwner: f.owner, RecoveryOf: []string{"old"}, Request: Request{Kind: "apply"}, Status: "succeeded", Current: Snapshot{Owner: f.owner, AutoSel: true}, CreatedAt: time.Now().Add(-time.Hour)},
	} {
		if err := s.store.SaveOperation(ctx, &op); err != nil {
			t.Fatal(err)
		}
	}
	if pending, err := s.Reconcile(ctx, f.owner); !pending || err != nil {
		t.Fatalf("clock order rejected explicit recovery: pending=%v err=%v", pending, err)
	}
	s.Wait()
	if f.active != "B" {
		t.Fatalf("active=%s", f.active)
	}
}

func TestRecoveryStartRetainsRequestIdempotency(t *testing.T) {
	s, _ := testService(t)
	ctx := context.Background()
	request := Request{RequestID: "manual", Kind: "apply", Profile: "B", Candidates: []string{}}
	op, err := s.Start(ctx, "d", request)
	if err != nil {
		t.Fatal(err)
	}
	waitOperation(t, s, op.ID)
	if replay, err := s.Start(ctx, "d", request); err != nil || replay.ID != op.ID {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
}

func saveRecoveryTestPolicy(t *testing.T, s *Service, owner Owner) {
	t.Helper()
	if err := s.SavePolicy(context.Background(), Policy{Key: "sim:" + owner.ICCID, Mode: "fixed", Model: owner.Model, Firmware: owner.Firmware, Profile: "B"}); err != nil {
		t.Fatal(err)
	}
}

func restartRecoveryTestService(t *testing.T, s *Service) *Service {
	t.Helper()
	restarted := New(Options{Context: s.ctx, Store: s.store, Files: s.files, Gateway: s.gateway})
	t.Cleanup(restarted.Wait)
	if err := restarted.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	return restarted
}
