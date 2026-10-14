package device

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/modemvoice/qdc507"
)

type adbTestExecutor func(context.Context, qdc507.Invocation) (string, error)

func (f adbTestExecutor) Execute(ctx context.Context, request qdc507.Invocation) (string, error) {
	return f(ctx, request)
}

func TestModemADBPreparationRespectsDisableAndIdentity(t *testing.T) {
	for _, scenario := range []string{"disabled", "existing", "different USB", "SIM changed", "mode changed", "worker removed"} {
		t.Run(scenario, func(t *testing.T) {
			p, port := testModemVoicePort(t)
			port.worker.Config.USBPath = "/sys/devices/usb3/3-2.2"
			disabled := false
			p.cfg = &config.Config{ModemVoice: config.ModemVoiceConfig{AutoEnableADB: &disabled}}
			p.openATSession = func(string) (atSerialSession, error) {
				t.Fatal("unexpected AT access")
				return nil, errors.New("unexpected")
			}
			listing := ""
			switch scenario {
			case "existing":
				listing = "serial device usb:3-2.2 transport_id:1"
			case "different USB":
				listing = "serial device usb:3-2.1 transport_id:1"
			case "SIM changed":
				port.worker.state.Identity.ICCID = "other-card"
			case "mode changed":
				port.worker.Config.PhoneMode = PhoneModeWiFi
			case "worker removed":
				delete(p.workers, port.worker.ID)
			}
			client, err := qdc507.NewADB(adbTestExecutor(func(context.Context, qdc507.Invocation) (string, error) { return listing, nil }))
			if err != nil {
				t.Fatal(err)
			}
			err = port.prepareADB(context.Background(), client, t.TempDir())
			if (err == nil) != (scenario == "existing") {
				t.Fatalf("%s: %v", scenario, err)
			}
		})
	}
}

func TestModemUSBBackupIsPrivateAndContainsRestoreCommand(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "usb-backups")
	command := `AT+QCFG="usbcfg",0x2C7C,0x125,1,1,1,1,1,0,1`
	path, err := saveModemUSBBackup(dir, "3-2.2", command)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]interface{}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record["restore_command"] != command || record["usb"] != "3-2.2" || strings.Contains(string(data), "QADBKEY") {
		t.Fatal(string(data))
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	next, err := saveModemUSBBackup(dir, "3-2.2", command)
	if err != nil || next == path {
		t.Fatal("overwrote backup", err)
	}
	if _, err := saveModemUSBBackup(dir, "../other", command); err == nil {
		t.Fatal("accepted invalid USB path")
	}
}
