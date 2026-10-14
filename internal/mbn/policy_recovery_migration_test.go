package mbn

import (
	"context"
	"errors"
	"testing"
)

func TestRecoveryHistorySurvivesSchemaUpgrade(t *testing.T) {
	s, f := testService(t)
	ctx := context.Background()
	saveRecoveryTestPolicy(t, s, f.owner)
	op := Operation{ID: "legacy", DeviceID: "d", RequestID: "legacy", ExpectedOwner: f.owner, Status: "needs_attention"}
	if err := s.store.SaveOperation(ctx, &op); err != nil {
		t.Fatal(err)
	}
	database := s.store.(*DatabaseStore).db
	if err := database.Migrator().DropColumn(&Operation{}, "RecoveryOf"); err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&Operation{}); err != nil {
		t.Fatal(err)
	}
	restarted := restartRecoveryTestService(t, s)
	if pending, err := restarted.Reconcile(ctx, f.owner); pending || !errors.Is(err, ErrNeedsAttention) {
		t.Fatalf("legacy history lost: pending=%v err=%v", pending, err)
	}
	recovered, err := restarted.Start(ctx, "d", Request{RequestID: "manual", Kind: "apply", Profile: "B"})
	if err != nil {
		t.Fatal(err)
	}
	result := waitOperation(t, restarted, recovered.ID)
	if result.Status != "succeeded" || len(result.RecoveryOf) != 1 || result.RecoveryOf[0] != op.ID {
		t.Fatalf("recovery proof not persisted: %+v", result)
	}
}
