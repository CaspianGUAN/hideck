package vowifihost

import "context"

type lifecycleSubmission struct {
	command      LifecycleCommand
	invalidation uint64
	preempt      bool
}

// Check ownership, allocate the generation and install cancellation together.
// A retired queued command must not advance a replacement's generation.
func (c *LifecycleController) bindCommandRun(ctx context.Context, lifecycle *deviceLifecycle, sub lifecycleSubmission) (context.Context, LifecycleCommand, func()) {
	runCtx, cancel := context.WithCancel(ctx)
	cmd := sub.command
	lifecycle.generationMu.Lock()
	stale := cmd.Generation != 0 && lifecycle.generation != 0 && cmd.Generation != lifecycle.generation
	if ctx.Err() != nil || lifecycle.invalidation != sub.invalidation || stale {
		lifecycle.generationMu.Unlock()
		cancel()
		return runCtx, cmd, func() {}
	}
	if sub.preempt {
		lifecycle.invalidation++
		if lifecycle.runCancel != nil {
			lifecycle.runCancel()
		}
	}
	if cmd.Generation == 0 && (cmd.Kind != LifecycleCommandSwitchEnd || cmd.RestoreRadio) {
		lifecycle.generation++
		cmd.Generation = lifecycle.generation
	}
	lifecycle.runCancelSeq++
	seq := lifecycle.runCancelSeq
	lifecycle.runCancel = cancel
	lifecycle.generationMu.Unlock()
	return runCtx, cmd, func() {
		lifecycle.generationMu.Lock()
		if lifecycle.runCancelSeq == seq {
			lifecycle.runCancel = nil
		}
		lifecycle.generationMu.Unlock()
	}
}

// Invalidate cancels the running command and makes already queued commands
// stale, without canceling commands submitted after this ownership boundary.
func (c *LifecycleController) Invalidate(deviceID string) {
	if c == nil {
		return
	}
	lifecycle := c.device(deviceID)
	lifecycle.generationMu.Lock()
	lifecycle.generation++
	lifecycle.invalidation++
	if lifecycle.runCancel != nil {
		lifecycle.runCancel()
	}
	lifecycle.generationMu.Unlock()
}

func (c *LifecycleController) invalidationVersion(lifecycle *deviceLifecycle) uint64 {
	lifecycle.generationMu.Lock()
	defer lifecycle.generationMu.Unlock()
	return lifecycle.invalidation
}
