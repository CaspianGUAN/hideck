package device

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yibaiba/hideck/pkg/simidentity"
)

const automationStatePollInterval = 500 * time.Millisecond

type automationDeviceOwner struct {
	worker  *Worker
	imei    string
	usbPath string
}

// ApplyCurrentCardPolicy reapplies the persisted policy for the device's active ICCID.
func (p *Pool) ApplyCurrentCardPolicy(deviceID, reason string) error {
	if p == nil {
		return errors.New("device pool is unavailable")
	}
	worker := p.GetWorker(strings.TrimSpace(deviceID))
	if worker == nil {
		return fmt.Errorf("device %s is unavailable", deviceID)
	}
	result := p.resolveAndApplyPolicy(worker, strings.TrimSpace(reason))
	if !result.Applied {
		if result.Reason == "" {
			result.Reason = "policy resolver is unavailable"
		}
		return fmt.Errorf("apply card policy for device %s: %s", deviceID, result.Reason)
	}
	return nil
}

// SwitchESIMProfileAndWait waits for both the asynchronous recovery flow and
// the runtime ICCID projection, not only the initial EnableProfile response.
func (p *Pool) SwitchESIMProfileAndWait(ctx context.Context, deviceID, iccid, aid string) error {
	if p == nil {
		return errors.New("device pool is unavailable")
	}
	releaseOperation, err := p.AcquireDeviceOperation(deviceID)
	if err != nil {
		return err
	}
	defer releaseOperation()
	target := normalizeAutomationICCID(iccid)
	if target == "" {
		return errors.New("target ICCID is empty")
	}
	worker := p.GetWorker(strings.TrimSpace(deviceID))
	if worker == nil {
		return fmt.Errorf("device %s is unavailable", deviceID)
	}
	if normalizeAutomationICCID(worker.CurrentICCID()) == target && !p.IsESIMSwitching(deviceID) {
		return nil
	}
	if worker.EsimMgr == nil {
		return fmt.Errorf("device %s has no eSIM manager", deviceID)
	}
	owner := captureAutomationDeviceOwner(worker)
	transition := p.policyTransitionFor(deviceID)
	epochBeforeSwitch := policySwitchEpoch(transition)
	if _, err := worker.EsimMgr.SwitchProfileWithResult(ctx, target, strings.TrimSpace(aid)); err != nil {
		return fmt.Errorf("switch device %s to profile %s: %w", deviceID, target, err)
	}
	expectedEpoch := policySwitchEpoch(transition)
	if expectedEpoch > epochBeforeSwitch+1 {
		return fmt.Errorf("wait for device %s profile %s: device operation was replaced", deviceID, target)
	}

	ticker := time.NewTicker(automationStatePollInterval)
	defer ticker.Stop()
	for {
		if policySwitchEpoch(transition) != expectedEpoch {
			return fmt.Errorf("wait for device %s profile %s: device operation was replaced", deviceID, target)
		}
		current := p.GetWorker(deviceID)
		if current != nil && !owner.matches(current) {
			return fmt.Errorf("wait for device %s profile %s: physical device was replaced", deviceID, target)
		}
		if current != nil && !p.IsESIMSwitching(deviceID) && normalizeAutomationICCID(current.CurrentICCID()) == target {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for device %s profile %s: %w", deviceID, target, ctx.Err())
		case <-ticker.C:
		}
	}
}

func captureAutomationDeviceOwner(worker *Worker) automationDeviceOwner {
	if worker == nil {
		return automationDeviceOwner{}
	}
	worker.cacheMu.RLock()
	imei := strings.TrimSpace(worker.state.Identity.IMEI)
	worker.cacheMu.RUnlock()
	if imei == "" {
		imei = strings.TrimSpace(worker.Config.ModemIMEI)
	}
	return automationDeviceOwner{worker: worker, imei: imei, usbPath: strings.TrimSpace(worker.Config.USBPath)}
}

func (owner automationDeviceOwner) matches(worker *Worker) bool {
	current := captureAutomationDeviceOwner(worker)
	if owner.imei != "" {
		return current.imei == owner.imei
	}
	if owner.usbPath != "" {
		return current.usbPath == owner.usbPath
	}
	return worker == owner.worker
}

func policySwitchEpoch(transition *policyTransition) uint64 {
	transition.Lock()
	defer transition.Unlock()
	return transition.switchEpoch
}

func normalizeAutomationICCID(value string) string {
	return simidentity.CanonicalICCID(value)
}
