package audio

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// ExecRunner is the production infrastructure adapter. Custom route output is
// not copied into application logs: it may contain vendor credentials. Failures
// expose the exit status; helpers should write device diagnostics to their log.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, command Command) error {
	if ctx == nil {
		return errors.New("audio: command context is required")
	}
	if err := validateCommand(command); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, command.Program, command.Args...)
	cmd.WaitDelay = processPipeWait
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("audio: route command failed: %w", errors.Join(ctx.Err(), err))
	}
	return ctx.Err()
}
