package qdc507

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

const usbOff = `+QCFG: "usbcfg",0x2C7C,0x125,1,1,1,1,1,0,1`
const usbOn = `+QCFG: "usbcfg",0x2C7C,0x125,1,1,1,1,1,1,1`
const usbSet = `AT+QCFG="usbcfg",0x2C7C,0x125,1,1,1,1,1,1,1`

type adbEnableFixture struct {
	options  ADBEnableOptions
	commands []string
	backup   string
	enabled  bool
}

func newADBEnableFixture(t *testing.T) *adbEnableFixture {
	t.Helper()
	f := &adbEnableFixture{}
	f.options = ADBEnableOptions{USB: "3-2.2", Firmware: "QDC507GLEFM21",
		Check:  func(ctx context.Context) error { return ctx.Err() },
		Backup: func(original string) error { f.backup = original; return nil },
		Probe: func(_ context.Context, usb string) error {
			if usb != "3-2.2" {
				t.Fatal("wrong USB", usb)
			}
			if f.enabled {
				return nil
			}
			return ErrADBNotFound
		},
		AT: func(_ context.Context, command string, _ time.Duration) (string, error) {
			f.commands = append(f.commands, command)
			switch command {
			case `AT+QCFG="usbcfg"?`:
				if f.enabled {
					return usbOn, nil
				}
				return usbOff, nil
			case "AT+CLCC":
				return "OK", nil
			case "AT+QADBKEY?":
				return "+QADBKEY: 12345678\r\nOK", nil
			case `AT+QADBKEY="0jXKXQwSwMxYoeg"`:
				if f.backup == "" {
					t.Fatal("authorization before backup")
				}
				return "", nil // Managed AT strips OK.
			case usbSet:
				f.enabled = true
				return "OK", nil
			default:
				t.Fatalf("unexpected command %q", command)
				return "", nil
			}
		},
	}
	return f
}

func TestEnsureADBEnablesOnlyADBAndVerifiesReadback(t *testing.T) {
	f := newADBEnableFixture(t)
	if err := EnsureADB(context.Background(), f.options); err != nil {
		t.Fatal(err)
	}
	want := []string{`AT+QCFG="usbcfg"?`, "AT+CLCC", "AT+QADBKEY?",
		`AT+QADBKEY="0jXKXQwSwMxYoeg"`, `AT+QCFG="usbcfg"?`, "AT+CLCC", usbSet, `AT+QCFG="usbcfg"?`}
	if !reflect.DeepEqual(f.commands, want) {
		t.Fatalf("commands: %v", f.commands)
	}
	if f.backup != `AT+QCFG="usbcfg",0x2C7C,0x125,1,1,1,1,1,0,1` {
		t.Fatal(f.backup)
	}
	f.commands = nil
	if err := EnsureADB(context.Background(), f.options); err != nil || len(f.commands) != 0 {
		t.Fatal("already-enabled ADB must not touch AT", err, f.commands)
	}
}

func TestEnsureADBAllowsStandingDataBearersButNotOtherCalls(t *testing.T) {
	for _, mode := range []string{"0", "1", "2", "9"} {
		t.Run(mode, func(t *testing.T) {
			f := newADBEnableFixture(t)
			original := f.options.AT
			f.options.AT = func(ctx context.Context, cmd string, timeout time.Duration) (string, error) {
				if cmd == "AT+CLCC" {
					return "+CLCC: 1,1,0," + mode + ",0,\"\",128\r\n+CLCC: 2,1,0,1,0,\"\",128\r\nOK", nil
				}
				return original(ctx, cmd, timeout)
			}
			err := EnsureADB(context.Background(), f.options)
			if (err == nil) != (mode == "1") || f.enabled != (mode == "1") {
				t.Fatal(mode, err)
			}
		})
	}
}

func TestEnsureADBRejectsUnsafeOrDisabledPreparation(t *testing.T) {
	for _, name := range []string{"disabled", "firmware", "usb", "identity", "offline", "backup", "call"} {
		t.Run(name, func(t *testing.T) {
			f := newADBEnableFixture(t)
			switch name {
			case "disabled":
				f.options.Disabled = true
			case "firmware":
				f.options.Firmware = "EC25"
			case "usb":
				f.options.USB = "../3-2.1"
			case "identity":
				f.options.Check = func(context.Context) error { return errors.New("SIM changed") }
			case "offline":
				f.options.Probe = func(context.Context, string) error { return errors.New("offline") }
			case "backup":
				f.options.Backup = func(string) error { return errors.New("disk full") }
			case "call":
				original := f.options.AT
				f.options.AT = func(ctx context.Context, cmd string, timeout time.Duration) (string, error) {
					if cmd == "AT+CLCC" {
						return "+CLCC: 1,0,0,0,0", nil
					}
					return original(ctx, cmd, timeout)
				}
			}
			if err := EnsureADB(context.Background(), f.options); err == nil {
				t.Fatal("accepted unsafe operation")
			}
			for _, command := range f.commands {
				if strings.HasPrefix(command, "AT+QADBKEY") || command == usbSet {
					t.Fatal("unauthorized write", command)
				}
			}
		})
	}
}

func TestEnsureADBErrorsDoNotLeakKeysOrContinue(t *testing.T) {
	for _, responseOnly := range []bool{false, true} {
		f := newADBEnableFixture(t)
		original := f.options.AT
		f.options.AT = func(ctx context.Context, cmd string, timeout time.Duration) (string, error) {
			if strings.HasPrefix(cmd, "AT+QADBKEY=") {
				if responseOnly {
					return cmd + "\r\nERROR\r\n", nil
				}
				return "", errors.New(cmd + " transport error")
			}
			return original(ctx, cmd, timeout)
		}
		err := EnsureADB(context.Background(), f.options)
		if err == nil || strings.Contains(err.Error(), "0jXKXQwSwMxYoeg") || f.enabled {
			t.Fatal("authorization failure was ignored or leaked", err)
		}
	}
}

func TestEnsureADBRejectsFalseOKAndChangedConfiguration(t *testing.T) {
	for _, scenario := range []string{"false OK", "changed config", "SIM replaced", "setter ERROR"} {
		t.Run(scenario, func(t *testing.T) {
			f := newADBEnableFixture(t)
			original := f.options.AT
			queries := 0
			f.options.AT = func(ctx context.Context, cmd string, timeout time.Duration) (string, error) {
				if cmd == `AT+QCFG="usbcfg"?` {
					queries++
					if queries == 2 && scenario == "changed config" {
						return strings.Replace(usbOff, "0x125", "0x126", 1), nil
					}
					if queries == 2 && scenario == "SIM replaced" {
						return "", errors.New("SIM replaced")
					}
				}
				if cmd == usbSet && scenario == "false OK" {
					return "OK", nil
				}
				if cmd == usbSet && scenario == "setter ERROR" {
					return "ERROR", nil
				}
				return original(ctx, cmd, timeout)
			}
			if err := EnsureADB(context.Background(), f.options); err == nil {
				t.Fatal("ignored", scenario)
			}
			if f.enabled {
				t.Fatal("unexpected USB change")
			}
		})
	}
}

func TestEnsureADBEnabledBitDoesNotCauseRepeatedWrites(t *testing.T) {
	f := newADBEnableFixture(t)
	f.enabled = true
	f.options.Probe = func(context.Context, string) error { return ErrADBNotFound }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := EnsureADB(ctx, f.options)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "连接未出现") {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.commands, []string{`AT+QCFG="usbcfg"?`}) {
		t.Fatal("rewrote enabled USB", f.commands)
	}
}

func TestEnsureADBStopsWhenModeChangesAfterAuthorization(t *testing.T) {
	f := newADBEnableFixture(t)
	stale := false
	f.options.Check = func(context.Context) error {
		if stale {
			return errors.New("mode changed")
		}
		return nil
	}
	original := f.options.AT
	f.options.AT = func(ctx context.Context, cmd string, timeout time.Duration) (string, error) {
		response, err := original(ctx, cmd, timeout)
		if strings.HasPrefix(cmd, "AT+QADBKEY=") {
			stale = true
		}
		return response, err
	}
	if err := EnsureADB(context.Background(), f.options); err == nil || f.enabled {
		t.Fatal(err)
	}
}
