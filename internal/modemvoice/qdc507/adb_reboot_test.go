package qdc507

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestADBRebootOnlyAfterVerifiedUSBWriteAndMissingConnection(t *testing.T) {
	f := newADBEnableFixture(t)
	f.options.Probe = func(context.Context, string) error { return ErrADBNotFound }
	reboots := 0
	f.options.Reboot = func(context.Context) error { reboots++; return nil }
	err := ensureADB(context.Background(), f.options, time.Millisecond)
	if !errors.Is(err, ErrADBRestarting) || reboots != 1 {
		t.Fatal(err, reboots)
	}
	// After re-enumeration (or a new prepare call), an already-enabled USB bit
	// alone cannot trigger another reboot.
	err = ensureADB(context.Background(), f.options, time.Millisecond)
	if err == nil || errors.Is(err, ErrADBRestarting) || reboots != 1 {
		t.Fatal(err, reboots)
	}
}

func TestADBRebootDoesNotTreatProbeFailureOrCancellationAsMissing(t *testing.T) {
	for _, scenario := range []string{"permission", "offline", "deadline", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			f := newADBEnableFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.options.Probe = func(context.Context, string) error {
				if !f.enabled {
					return ErrADBNotFound
				}
				if scenario == "cancel" {
					cancel()
					return ErrADBNotFound
				}
				if scenario == "deadline" {
					return context.DeadlineExceeded
				}
				return errors.New(scenario)
			}
			f.options.Reboot = func(context.Context) error { t.Fatal("unexpected reboot"); return nil }
			if err := ensureADB(ctx, f.options, time.Millisecond); err == nil {
				t.Fatal("ignored failure")
			}
		})
	}
}

func TestADBRebootRechecksCallUSBAndLateConnection(t *testing.T) {
	for _, scenario := range []string{"call", "USB changed", "SIM changed", "late ADB"} {
		t.Run(scenario, func(t *testing.T) {
			f := newADBEnableFixture(t)
			f.options.Probe = func(context.Context, string) error { return ErrADBNotFound }
			f.options.Reboot = func(context.Context) error { t.Fatal("rebooted after state changed"); return nil }
			queries := 0
			original := f.options.AT
			f.options.AT = func(ctx context.Context, cmd string, timeout time.Duration) (string, error) {
				if cmd == `AT+QCFG="usbcfg"?` {
					queries++
				}
				if queries >= 4 {
					switch scenario {
					case "USB changed":
						return usbOff, nil
					case "SIM changed":
						return "", errors.New("SIM changed")
					case "call":
						if cmd == "AT+CLCC" {
							return "+CLCC: 1,1,4,0,0", nil
						}
					}
				}
				return original(ctx, cmd, timeout)
			}
			// The option snapshot holds the original Probe; share state explicitly.
			late := false
			if scenario == "late ADB" {
				f.options.Probe = func(context.Context, string) error {
					if late {
						return nil
					}
					return ErrADBNotFound
				}
				at := f.options.AT
				f.options.AT = func(ctx context.Context, cmd string, timeout time.Duration) (string, error) {
					value, err := at(ctx, cmd, timeout)
					if queries >= 4 {
						late = true
					}
					return value, err
				}
			}
			err := ensureADB(context.Background(), f.options, time.Millisecond)
			if (err == nil) != (scenario == "late ADB") {
				t.Fatal(scenario, err)
			}
		})
	}
}

func TestADBRebootFailureIsNotReportedAsRestarting(t *testing.T) {
	f := newADBEnableFixture(t)
	f.options.Probe = func(context.Context, string) error { return ErrADBNotFound }
	failure := errors.New("one-shot already consumed")
	f.options.Reboot = func(context.Context) error { return failure }
	if err := ensureADB(context.Background(), f.options, time.Millisecond); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}
