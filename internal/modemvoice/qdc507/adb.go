package qdc507

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Calibration can spend 10s waiting for its FIFO, 12s writing commands and
// another 10s waiting for DSP calibration. Keep the transport above that bound.
const commandTimeout = 45 * time.Second

type Invocation struct {
	Args []string
	File *Transfer
}
type Transfer struct {
	Remote string
	Data   []byte
}
type Executor interface {
	Execute(context.Context, Invocation) (string, error)
}

type ADB struct{ exec Executor }
type Target struct{ usb, boot string }

func NewADB(executor Executor) (*ADB, error) {
	if executor == nil {
		return nil, fmt.Errorf("qdc507: ADB executor is required")
	}
	return &ADB{exec: executor}, nil
}

func (a *ADB) run(ctx context.Context, request Invocation) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("qdc507: context is required")
	}
	bounded, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	output, err := a.exec.Execute(bounded, request)
	return output, errors.Join(err, bounded.Err())
}

func (a *ADB) find(ctx context.Context, usb string) (transport, error) {
	output, err := a.run(ctx, Invocation{Args: []string{"devices", "-l"}})
	if err != nil {
		return transport{}, fmt.Errorf("qdc507: enumerate ADB: %w", err)
	}
	return selectTransport(output, usb)
}

func (a *ADB) Probe(ctx context.Context, usb string) error {
	_, err := a.find(ctx, usb)
	return err
}

func (a *ADB) Bind(ctx context.Context, usb string) (Target, error) {
	t, err := a.find(ctx, usb)
	if err != nil {
		return Target{}, err
	}
	result, err := a.shell(ctx, t, "cat /proc/sys/kernel/random/boot_id")
	if err != nil {
		return Target{}, err
	}
	if result.Status != 0 || !bootIdentity.MatchString(result.Output) {
		return Target{}, fmt.Errorf("qdc507: cannot identify module boot")
	}
	return Target{usb: usb, boot: result.Output}, nil
}

func (a *ADB) shell(ctx context.Context, t transport, command string) (ShellResult, error) {
	wrapped := "( " + command + "\n); rc=$?; printf '\\n" + shellStatus + "%s\\n' \"$rc\""
	output, err := a.run(ctx, Invocation{Args: []string{"-t", t.id, "shell", wrapped}})
	if err != nil {
		return ShellResult{}, fmt.Errorf("qdc507: ADB shell transport: %w", err)
	}
	return parseShell(output)
}

func (a *ADB) Shell(ctx context.Context, target Target, command string) (ShellResult, error) {
	if !bootIdentity.MatchString(target.boot) {
		return ShellResult{}, fmt.Errorf("qdc507: target is not bound to a module boot")
	}
	t, err := a.find(ctx, target.usb)
	if err != nil {
		return ShellResult{}, err
	}
	guard := "test \"$(cat /proc/sys/kernel/random/boot_id)\" = " + quoteShell(target.boot) + " || exit 75\n"
	return a.shell(ctx, t, guard+command)
}

// Upload sends a verified snapshot, never a mutable cache path. Shell commands
// after transfer recheck the boot identity before accepting or using the file.
func (a *ADB) Upload(ctx context.Context, target Target, file Upload) error {
	artifact, ok := pinnedArtifact(file.Name)
	if !ok {
		return fmt.Errorf("qdc507: unknown artifact")
	}
	if err := verifyBytes(file.Data, artifact); err != nil {
		return err
	}
	path := remoteDirectory + "/" + file.Name
	if _, err := checked(a.Shell(ctx, target, "test -d "+quoteShell(remoteDirectory))); err != nil {
		return err
	}
	t, err := a.find(ctx, target.usb)
	if err != nil {
		return err
	}
	if _, err := a.run(ctx, Invocation{Args: []string{"-t", t.id, "push"}, File: &Transfer{Remote: path + ".part", Data: file.Data}}); err != nil {
		return fmt.Errorf("qdc507: push %s: %w", file.Name, err)
	}
	command := "test \"$(sha256sum " + quoteShell(path+".part") + " | cut -d ' ' -f 1)\" = " + quoteShell(artifact.SHA256) + " && mv " + quoteShell(path+".part") + " " + quoteShell(path)
	result, err := a.Shell(ctx, target, command)
	if err != nil {
		return err
	}
	if result.Status != 0 {
		return fmt.Errorf("qdc507: upload %s remote status %d", file.Name, result.Status)
	}
	return nil
}

func checked(result ShellResult, err error) (string, error) {
	if err != nil {
		return result.Output, err
	}
	if result.Status != 0 {
		return result.Output, fmt.Errorf("qdc507: remote command failed with status %d: %s", result.Status, strings.TrimSpace(result.Output))
	}
	return result.Output, nil
}
