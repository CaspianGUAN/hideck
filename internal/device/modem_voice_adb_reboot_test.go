package device

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestADBRebootReservationSurvivesNewCallAndSeparatesModems(t *testing.T) {
	state := t.TempDir()
	const imei = "860000000000001"
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if reserveADBReboot(state, imei) == nil {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatal("duplicate reboot reservations", accepted.Load())
	}
	if err := reserveADBReboot(state, imei); err == nil {
		t.Fatal("reservation lost")
	}
	if err := reserveADBReboot(state, "860000000000002"); err != nil {
		t.Fatal("another modem blocked", err)
	}
	files, err := filepath.Glob(filepath.Join(state, "usb-reboot-attempts", "*.once"))
	if err != nil || len(files) != 2 {
		t.Fatal(files, err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil || strings.Contains(string(data), imei) || strings.Contains(file, imei) {
			t.Fatal("identity leaked", err)
		}
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal(info, err)
		}
	}
}

func TestModemADBRebootSendsOnceAndPreservesUncertainty(t *testing.T) {
	for _, result := range []string{"OK", "ERROR", "EOF"} {
		t.Run(result, func(t *testing.T) {
			p, port := testModemVoicePort(t)
			state := t.TempDir()
			resets, scheduled := 0, 0
			confirmed := false
			p.openATSession = func(string) (atSerialSession, error) {
				return &controlledATSession{execute: func(cmd string, _ time.Duration) (string, error) {
					switch cmd {
					case "AT+CGSN":
						return "860000000000001\r\nOK", nil
					case "AT+CFUN=1,1":
						resets++
						if result == "EOF" {
							return "", errors.New("EOF")
						}
						return result, nil
					default:
						t.Fatal("unexpected AT", cmd)
						return "", nil
					}
				}}, nil
			}
			schedule := func(ok bool) { scheduled++; confirmed = ok }
			err := port.rebootADB(context.Background(), state, schedule)
			if (err == nil) != (result == "OK") || resets != 1 {
				t.Fatal(err, resets)
			}
			if confirmed != (result == "OK") || (scheduled == 0) != (result == "ERROR") {
				t.Fatal(scheduled, confirmed)
			}
			if err := port.rebootADB(context.Background(), state, schedule); err == nil || resets != 1 {
				t.Fatal("reboot retried", err, resets)
			}
		})
	}
}

func TestModemADBRebootRejectsIdentityAndModeChanges(t *testing.T) {
	for _, scenario := range []string{"SIM", "mode", "IMEI", "removed"} {
		t.Run(scenario, func(t *testing.T) {
			p, port := testModemVoicePort(t)
			if scenario == "IMEI" {
				port.worker.Config.ModemIMEI = "860000000000011"
			}
			p.openATSession = func(string) (atSerialSession, error) {
				return &controlledATSession{execute: func(cmd string, _ time.Duration) (string, error) {
					if cmd != "AT+CGSN" {
						t.Fatal("reset after change", cmd)
					}
					switch scenario {
					case "SIM":
						port.worker.state.Identity.ICCID = "new-card"
					case "mode":
						port.worker.Config.PhoneMode = PhoneModeWiFi
					case "removed":
						delete(p.workers, port.worker.ID)
					}
					return "860000000000001\r\nOK", nil
				}}, nil
			}
			if err := port.rebootADB(context.Background(), t.TempDir(), func(bool) { t.Fatal("scheduled stale reboot") }); err == nil {
				t.Fatal("state change ignored")
			}
		})
	}
}

func TestADBRebootRecoveryDoesNotEvictReplacementWorker(t *testing.T) {
	p, port := testModemVoicePort(t)
	opts := defaultModemRebootRecoveryOptions(port.worker.ID, modemADBRebootReason)
	opts.initialWorker = port.worker
	opts.restoreVoWiFi = false
	replacement := &Worker{ID: port.worker.ID}
	p.workers[port.worker.ID] = replacement
	p.rescanAndReconnectForTest = func() error { t.Fatal("scanned after replacement"); return nil }
	if err := p.removeInitialRecoveryWorker(opts); err == nil {
		t.Fatal("ownership change not detected")
	}
	p.runModemRebootRecovery(opts)
	if p.GetWorker(port.worker.ID) != replacement {
		t.Fatal("replacement evicted")
	}
}
