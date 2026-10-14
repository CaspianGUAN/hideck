package mbn

import (
	"context"
	"fmt"
)

// s.mu is held until enqueueing, so unresolved history cannot change between
// authorizing an automatic write and recording its operation.
func (s *Service) recoveryForStart(ctx context.Context, owner Owner, input operationStart) ([]string, error) {
	if !input.policy && !recoveryKind(input.request.Kind) {
		return nil, nil
	}
	pending, err := s.pendingPolicyRecovery(ctx, owner)
	if err != nil {
		return nil, err
	}
	if input.policy && len(pending) != 0 {
		return nil, fmt.Errorf("%w：任务 %s 尚未处理，请读取当前配置后手动应用或恢复；不会自动重发写命令", ErrNeedsAttention, pending[0])
	}
	return pending, nil
}

// Use physical identity, not process-local generations or logical device IDs:
// an uncertain write remains uncertain after a restart or re-enumeration.
func samePolicyRecoveryOwner(a, b Owner) bool {
	return a.IMEI != "" && a.ICCID != "" && a.IMEI == b.IMEI && a.ICCID == b.ICCID
}

// Called with s.mu held before an automatic policy mutation is queued.
func (s *Service) pendingPolicyRecovery(ctx context.Context, owner Owner) ([]string, error) {
	operations, err := s.store.Operations(ctx, "")
	if err != nil {
		return nil, err
	}
	resolved := resolvedPolicyRecovery(operations, owner)
	var pending []string
	for _, op := range operations {
		if !samePolicyRecoveryOwner(op.ExpectedOwner, owner) {
			continue
		}
		if op.Running() {
			return nil, fmt.Errorf("%w：同一模组与 SIM 的任务 %s 尚未结束", ErrConflict, op.ID)
		}
		if op.Status == "needs_attention" && !resolved[op.ID] {
			pending = append(pending, op.ID)
		}
	}
	return pending, nil
}

func resolvedPolicyRecovery(operations []Operation, owner Owner) map[string]bool {
	resolved := make(map[string]bool)
	for _, op := range operations {
		if !samePolicyRecoveryOwner(op.ExpectedOwner, owner) || !verifiedRecovery(op, owner) {
			continue
		}
		for _, id := range op.RecoveryOf {
			resolved[id] = true
		}
	}
	return resolved
}

func verifiedRecovery(op Operation, owner Owner) bool {
	if op.Status != "succeeded" && !((op.Status == "failed" || op.Status == "cancelled") && op.Stage == "restored") {
		return false
	}
	return recoveryKind(op.Request.Kind) && samePolicyRecoveryOwner(op.Current.Owner, owner) &&
		(op.Current.AutoSel || activeProfile(op.Current, op.Current.Active()))
}

func recoveryKind(kind string) bool {
	return kind == "apply" || kind == "restore" || kind == "trial"
}
