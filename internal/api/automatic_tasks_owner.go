package api

import (
	"fmt"
	"strings"

	"github.com/yibaiba/hideck/internal/db"
	"github.com/yibaiba/hideck/internal/device"
)

type automaticTaskDeviceOwner struct {
	worker  *device.Worker
	imei    string
	usbPath string
}

type automaticTaskRestore struct {
	DeviceID string
	ICCID    string
	Snapshot db.CardPolicy
	Owner    automaticTaskDeviceOwner
}

func captureAutomaticTaskDeviceOwner(worker *device.Worker) automaticTaskDeviceOwner {
	if worker == nil {
		return automaticTaskDeviceOwner{}
	}
	status := worker.GetCachedDeviceStatus()
	imei := strings.TrimSpace(status.IMEI)
	if imei == "" {
		imei = strings.TrimSpace(worker.Config.ModemIMEI)
	}
	return automaticTaskDeviceOwner{
		worker: worker, imei: imei, usbPath: strings.TrimSpace(worker.Config.USBPath),
	}
}

func (owner automaticTaskDeviceOwner) resolve(
	pool *device.Pool,
	deviceID, expectedICCID string,
) (*device.Worker, error) {
	worker := pool.GetWorker(strings.TrimSpace(deviceID))
	if worker == nil {
		return nil, fmt.Errorf("device %s went offline", deviceID)
	}
	current := captureAutomaticTaskDeviceOwner(worker)
	if owner.imei != "" && current.imei != owner.imei {
		return nil, fmt.Errorf("device %s was replaced while the automatic task was running", deviceID)
	}
	if owner.imei == "" && owner.usbPath != "" && current.usbPath != owner.usbPath {
		return nil, fmt.Errorf("device %s moved to different hardware while the automatic task was running", deviceID)
	}
	if owner.imei == "" && owner.usbPath == "" && worker != owner.worker {
		return nil, fmt.Errorf("device %s was recreated without a stable hardware identity", deviceID)
	}
	if normalizeAutomaticTaskICCID(worker.CurrentICCID()) != normalizeAutomaticTaskICCID(expectedICCID) {
		return nil, fmt.Errorf("device %s no longer contains the task SIM", deviceID)
	}
	return worker, nil
}
