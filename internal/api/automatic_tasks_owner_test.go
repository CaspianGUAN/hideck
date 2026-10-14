package api

import (
	"testing"

	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/device"
)

func TestAutomaticTaskOwnerAllowsSameModemReenumeration(t *testing.T) {
	original := &device.Worker{ID: "wwan0", Config: config.DeviceConfig{ModemIMEI: "860000000000001"}}
	owner := captureAutomaticTaskDeviceOwner(original)
	replacement := &device.Worker{ID: original.ID, Config: config.DeviceConfig{ModemIMEI: original.Config.ModemIMEI}}
	pool := device.NewPool(&config.Config{})
	pool.AttachWorkerForTest(replacement)

	resolved, err := owner.resolve(pool, original.ID, "")
	if err != nil || resolved != replacement {
		t.Fatalf("resolve() worker=%p err=%v", resolved, err)
	}
}

func TestAutomaticTaskOwnerRejectsDifferentModem(t *testing.T) {
	original := &device.Worker{ID: "wwan0", Config: config.DeviceConfig{ModemIMEI: "860000000000001"}}
	owner := captureAutomaticTaskDeviceOwner(original)
	replacement := &device.Worker{ID: original.ID, Config: config.DeviceConfig{ModemIMEI: "860000000000002"}}
	pool := device.NewPool(&config.Config{})
	pool.AttachWorkerForTest(replacement)

	if _, err := owner.resolve(pool, original.ID, ""); err == nil {
		t.Fatal("different physical modem inherited the automatic task")
	}
}
