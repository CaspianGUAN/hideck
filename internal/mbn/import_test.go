package mbn

import (
	"context"
	"testing"
)

func TestImportVerifiesNewProfileButDoesNotActivate(t *testing.T) {
	s, f := testService(t)
	f.uploadEnabled = true
	ctx := context.Background()
	a, err := s.Upload(ctx, Asset{Name: "profile.mbn", Model: f.owner.Model}, []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.Start(ctx, "d", Request{RequestID: "import", Kind: "import", AssetID: a.ID, AcceptUnknownCompatibility: true})
	if err != nil {
		t.Fatal(err)
	}
	result := waitOperation(t, s, op.ID)
	if result.Status != "succeeded" || !hasProfile(result.Current, "C") || f.active != "A" || f.reboots != 0 {
		t.Fatalf("%+v", result)
	}
	if err := s.DeleteAsset(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if !f.imported {
		t.Fatal("deleting a library file must not delete the modem profile")
	}
}

func TestPolicyCanImportMissingProfileThenVerifyVersion(t *testing.T) {
	s, f := testService(t)
	f.uploadEnabled = true
	ctx := context.Background()
	a, err := s.Upload(ctx, Asset{Name: "profile.mbn", Model: f.owner.Model}, []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	err = s.SavePolicy(ctx, Policy{Key: "sim:" + f.owner.ICCID, Mode: "fixed", Model: f.owner.Model, Firmware: f.owner.Firmware, Profile: "C", Version: "v1", AssetID: a.ID, AllowImport: true})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.Reconcile(ctx, f.owner)
	if err != nil || !pending {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	s.Wait()
	jobs, err := s.Operations(ctx, "d")
	if err != nil || len(jobs) != 1 || jobs[0].Status != "succeeded" || f.active != "C" {
		t.Fatalf("%+v %v", jobs, err)
	}
}

func TestActiveDeleteSwitchesReplacementBeforeDeleting(t *testing.T) {
	s, f := testService(t)
	op, err := s.Start(context.Background(), "d", Request{RequestID: "delete", Kind: "delete", Profile: "A", Replacement: "B", ConfirmName: "A", AcceptNoBackup: true})
	if err != nil {
		t.Fatal(err)
	}
	result := waitOperation(t, s, op.ID)
	if result.Status != "succeeded" || f.active != "B" || hasProfile(result.Current, "A") || f.reboots != 2 {
		t.Fatalf("%+v", result)
	}
}
