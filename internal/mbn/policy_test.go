package mbn

import (
	"context"
	"errors"
	"testing"
)

func TestInvalidTargetDoesNotStopServices(t *testing.T) {
	for _, request := range []Request{
		{RequestID: "apply", Kind: "apply", Profile: "missing"},
		{RequestID: "trial", Kind: "trial", Candidates: []string{"B", "missing"}},
		{RequestID: "delete", Kind: "delete", Profile: "A", ConfirmName: "A", AcceptNoBackup: true},
	} {
		t.Run(request.Kind, func(t *testing.T) {
			s, f := testService(t)
			op, err := s.Start(context.Background(), "d", request)
			if err != nil {
				t.Fatal(err)
			}
			result := waitOperation(t, s, op.ID)
			if result.Status != "failed" || f.prepared != 0 || f.reboots != 0 {
				t.Fatalf("result=%+v prepare=%d reboots=%d", result, f.prepared, f.reboots)
			}
		})
	}
}

func TestReconcileRejectsChangedSIMBeforeStartingJob(t *testing.T) {
	s, f := testService(t)
	ctx := context.Background()
	old := f.owner
	if err := s.SavePolicy(ctx, Policy{Key: "sim:" + old.ICCID, Mode: "fixed", Model: old.Model, Firmware: old.Firmware, Profile: "B"}); err != nil {
		t.Fatal(err)
	}
	f.owner.ICCID = "other-card"
	if _, err := s.Reconcile(ctx, old); !errors.Is(err, ErrOwnerChanged) {
		t.Fatal(err)
	}
	jobs, err := s.Operations(ctx, "d")
	if err != nil || len(jobs) != 0 || f.prepared != 0 {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
}

func TestSessionChoiceDoesNotSurviveSIMGenerationChange(t *testing.T) {
	s, f := testService(t)
	s.keepSessionChoice(Snapshot{Owner: f.owner, AutoSel: true})
	policy, err := s.EffectivePolicy(context.Background(), f.owner)
	if err != nil || policy.Mode != "autosel" {
		t.Fatalf("%+v %v", policy, err)
	}
	f.owner.IdentityGeneration++
	policy, err = s.EffectivePolicy(context.Background(), f.owner)
	if err != nil || policy.Mode != "inherit" {
		t.Fatalf("%+v %v", policy, err)
	}
}

func TestSuccessfulTrialKeepsProfileWithoutCreatingPolicy(t *testing.T) {
	s, f := testService(t)
	f.registration = Registration{Registered: true, PLMN: "46001"}
	op, err := s.Start(context.Background(), "d", Request{RequestID: "trial", Kind: "trial", Candidates: []string{"B", "A"}, RegistrationSeconds: 1, StabilitySeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	result := waitOperation(t, s, op.ID)
	if result.Status != "succeeded" || len(result.Results) != 1 || f.active != "B" {
		t.Fatalf("%+v", result)
	}
	policies, err := s.Policies(context.Background())
	if err != nil || len(policies) != 0 {
		t.Fatalf("%+v %v", policies, err)
	}
}

func TestExhaustedTrialRestoresOriginal(t *testing.T) {
	s, f := testService(t)
	op, err := s.Start(context.Background(), "d", Request{RequestID: "trial", Kind: "trial", Candidates: []string{"B"}, RegistrationSeconds: 1, StabilitySeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	result := waitOperation(t, s, op.ID)
	if result.Status != "failed" || result.Stage != "restored" || len(result.Results) != 1 || f.active != "A" {
		t.Fatalf("%+v", result)
	}
}
