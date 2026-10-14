package qdc507

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"time"
)

const remoteDirectory = "/tmp/hideck-qdc507-20260712.5"
const prepareTimeout = 90 * time.Second

type Client interface {
	Bind(context.Context, string) (Target, error)
	Shell(context.Context, Target, string) (ShellResult, error)
	Upload(context.Context, Target, Upload) error
}

type Options struct {
	USB      string
	Firmware string
	Client   Client
	Source   fs.FS
	// Check verifies the current device/SIM generation, mode and RF policy. It
	// must return an error after a switch, rather than authorize the new SIM.
	Check func(context.Context) error
}

type Report struct {
	Version     string `json:"version"`
	Kernel      string `json:"kernel"`
	Calibration string `json:"calibration"`
}

type Manager struct {
	options Options
	gate    chan struct{}
	target  Target
	active  *Lease
}

func NewManager(options Options) (*Manager, error) {
	if options.Client == nil || options.Source == nil || options.Check == nil {
		return nil, fmt.Errorf("qdc507: client, bundle and device policy check are required")
	}
	if !usbLocation.MatchString(options.USB) {
		return nil, fmt.Errorf("qdc507: invalid USB location")
	}
	if options.Firmware != "QDC507GLEFM21" {
		return nil, fmt.Errorf("qdc507: firmware %q has not been validated", options.Firmware)
	}
	return &Manager{options: options, gate: make(chan struct{}, 1)}, nil
}

func (m *Manager) enter(ctx context.Context) (func(), error) {
	if ctx == nil {
		return nil, fmt.Errorf("qdc507: context is required")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case m.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-m.gate
			return nil, err
		}
		return func() { <-m.gate }, nil
	}
}

func (m *Manager) Prepare(ctx context.Context) (Report, error) {
	release, err := m.enter(ctx)
	if err != nil {
		return Report{}, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, prepareTimeout)
	defer cancel()
	if m.active != nil {
		return Report{}, fmt.Errorf("qdc507: an audio route is still owned")
	}
	if err := m.options.Check(ctx); err != nil {
		return Report{}, err
	}
	files, err := ReadBundle(m.options.Source)
	if err != nil {
		return Report{}, err
	}
	target, err := m.options.Client.Bind(ctx, m.options.USB)
	if err != nil {
		return Report{}, err
	}
	m.target = target
	if _, err := m.command(ctx, compatibilityScript); err != nil {
		return Report{}, err
	}
	if err := m.install(ctx, files); err != nil {
		return Report{}, err
	}
	if err := m.options.Check(ctx); err != nil {
		return Report{}, err
	}
	output, err := m.command(ctx, calibrationScript)
	if err != nil {
		return Report{}, fmt.Errorf("qdc507: calibration: %w", err)
	}
	if err := m.options.Check(ctx); err != nil {
		return Report{}, err
	}
	return Report{Version: RuntimeVersion, Kernel: KernelRelease, Calibration: output}, nil
}

func (m *Manager) command(ctx context.Context, script string) (string, error) {
	return checked(m.options.Client.Shell(ctx, m.target, script))
}

func (m *Manager) install(ctx context.Context, files map[string][]byte) error {
	output, err := m.command(ctx, installationStateScript)
	if err != nil {
		return err
	}
	if strings.TrimSpace(output) == "installed" {
		return m.verifyInstalled(ctx)
	}
	for _, artifact := range Artifacts() {
		if err := m.options.Check(ctx); err != nil {
			return err
		}
		if err := m.options.Client.Upload(ctx, m.target, Upload{Name: artifact.Name, Data: files[artifact.Name]}); err != nil {
			return err
		}
	}
	if err := m.options.Check(ctx); err != nil {
		return err
	}
	if _, err := m.command(ctx, loadScript); err != nil {
		return err
	}
	return m.verifyInstalled(ctx)
}

func (m *Manager) verifyInstalled(ctx context.Context) error {
	var command strings.Builder
	for _, artifact := range Artifacts() {
		fmt.Fprintf(&command, "test \"$(sha256sum %s | cut -d ' ' -f 1)\" = %s || exit 74\n", quoteShell(remoteDirectory+"/"+artifact.Name), quoteShell(artifact.SHA256))
	}
	command.WriteString(soundReadyScript)
	_, err := m.command(ctx, command.String())
	return err
}
