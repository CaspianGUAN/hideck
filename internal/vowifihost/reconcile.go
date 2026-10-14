package vowifihost

import (
	"context"
	"strings"
	"time"

	"github.com/yibaiba/hideck/pkg/logger"
)

const defaultDesiredRecoverReason = "desired_reconcile"

type DesiredRecoverRequest struct {
	DeviceID     string
	Reason       string
	OverrideEPDG string
	Generation   uint64
	Now          time.Time
	OnResult     func(deviceID, reason string, err error)
}

func (m *Manager) DesiredRecoverable(deviceID string) bool {
	if m == nil {
		return false
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return false
	}
	store := m.RuntimeStore()
	return !store.Active(deviceID) && !store.Starting(deviceID)
}

func (m *Manager) ScheduleDesiredRecover(ctx context.Context, req DesiredRecoverRequest) bool {
	if m == nil {
		return false
	}
	deviceID := strings.TrimSpace(req.DeviceID)
	if deviceID == "" {
		return false
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = defaultDesiredRecoverReason
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	stateMu := m.stateLock(deviceID)
	stateMu.Lock()
	defer stateMu.Unlock()
	if !m.DesiredRecoverable(deviceID) {
		return false
	}
	if !m.BeginDesiredRecover(deviceID, now) {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	generation := req.Generation
	if generation == 0 {
		generation = m.CurrentLifecycleGeneration(deviceID)
		if generation == 0 {
			generation = m.NextLifecycleGeneration(deviceID)
		}
	}

	logger.Warn("VoWiFi 目标态恢复开始", "event", "VOWIFI_DESIRED_RECOVER", "device", deviceID, "reason", reason)
	runRequest := req
	runRequest.DeviceID, runRequest.Reason, runRequest.Generation = deviceID, reason, generation
	go m.runDesiredRecover(ctx, runRequest)
	return true
}

func (m *Manager) runDesiredRecover(ctx context.Context, req DesiredRecoverRequest) {
	err := m.Recover(ctx, LifecycleRecoverRequest{
		DeviceID: req.DeviceID, Reason: req.Reason,
		OverrideEPDG: req.OverrideEPDG, Generation: req.Generation,
	})
	stateMu := m.stateLock(req.DeviceID)
	stateMu.Lock()
	defer stateMu.Unlock()
	if m.CurrentLifecycleGeneration(req.DeviceID) != req.Generation {
		return
	}
	if req.OnResult != nil {
		req.OnResult(req.DeviceID, req.Reason, err)
	}
}
