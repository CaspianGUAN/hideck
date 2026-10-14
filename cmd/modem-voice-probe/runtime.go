package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yibaiba/hideck/internal/modemvoice"
	"github.com/yibaiba/hideck/internal/modemvoice/qdc507"
)

type runtimeProbe struct {
	options options
	api     *probeAPI
	client  *modemvoice.Client
}

func prepareRuntime(ctx context.Context, p runtimeProbe) (func() error, error) {
	if p.options.runtimeDir == "" {
		return nil, nil
	}
	if err := p.check(ctx); err != nil {
		return nil, err
	}
	firmware, err := p.api.ExecuteATContext(ctx, "AT+CGMR", 5*time.Second)
	if err != nil {
		return nil, err
	}
	executor, err := qdc507.NewExec(qdc507.ExecOptions{Program: p.options.adbProgram, StateDirectory: p.options.adbState})
	if err != nil {
		return nil, err
	}
	adb, err := qdc507.NewADB(executor)
	if err != nil {
		return nil, err
	}
	m, err := qdc507.NewManager(qdc507.Options{
		USB: filepath.Base(p.options.usb), Firmware: firmwareVersion(firmware), Client: adb,
		Source: os.DirFS(p.options.runtimeDir), Check: p.check,
	})
	if err != nil {
		return nil, err
	}
	cleanup := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return m.Shutdown(ctx)
	}
	report, err := m.Prepare(ctx)
	if err != nil {
		return cleanup, err
	}
	fmt.Printf("RUNTIME version=%s kernel=%s; calibration diagnostics:\n%s\n", report.Version, report.Kernel, report.Calibration)
	_, err = m.Start(ctx)
	return cleanup, err
}

func (p runtimeProbe) check(ctx context.Context) error {
	if err := p.api.checkDevice(ctx, p.options); err != nil {
		return err
	}
	calls, err := p.client.Calls(ctx)
	if err != nil {
		return err
	}
	for _, call := range calls {
		if call.Mode != 1 && call.Mode != 2 {
			return fmt.Errorf("runtime preparation refused during an existing voice/unknown call")
		}
	}
	return nil
}

func firmwareVersion(response string) string {
	for _, line := range strings.FieldsFunc(response, func(r rune) bool { return r == '\r' || r == '\n' }) {
		line = strings.TrimSpace(line)
		if line != "" && line != "AT+CGMR" && line != "OK" {
			return line
		}
	}
	return ""
}
