package vowifihost

import (
	"context"
	"strings"

	"github.com/iniwex5/vowifi-go/runtimehost"
	"github.com/yibaiba/hideck/pkg/logger"
)

func (m *Manager) BeginStart(deviceID string) StartClaim {
	if m == nil {
		return StartClaim{}
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return StartClaim{}
	}
	stateMu := m.stateLock(deviceID)
	stateMu.Lock()
	defer stateMu.Unlock()
	return m.RuntimeStore().BeginStart(deviceID)
}

func (m *Manager) beginRuntimeStart(ctx context.Context, deviceID string) (StartClaim, error) {
	stateMu := m.stateLock(deviceID)
	stateMu.Lock()
	defer stateMu.Unlock()
	if err := ctx.Err(); err != nil {
		return StartClaim{}, err
	}
	claim := m.RuntimeStore().BeginStart(deviceID)
	if claim.Accepted {
		m.BeginWiFiCallingHealth(deviceID)
	}
	return claim, nil
}

func (m *Manager) FailStart(deviceID string, epoch uint64, state runtimehost.State, err error) {
	if m == nil {
		return
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return
	}
	stateMu := m.stateLock(deviceID)
	stateMu.Lock()
	defer stateMu.Unlock()
	if !m.ShouldRun(deviceID, epoch) {
		return
	}
	m.FailWiFiCallingHealthStart(deviceID, err)
	m.RuntimeStore().FailStart(deviceID, epoch, state, err)
	m.BroadcastState(deviceID)
}

func (m *Manager) CurrentEpoch(deviceID string) uint64 {
	if m == nil {
		return 0
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return 0
	}
	return m.RuntimeStore().CurrentEpoch(deviceID)
}

func (m *Manager) ShouldRun(deviceID string, epoch uint64) bool {
	return m.CurrentEpoch(deviceID) == epoch
}

func (m *Manager) ClaimStarted(deviceID string, epoch uint64, inst *runtimehost.Instance) bool {
	if m == nil || inst == nil {
		return false
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return false
	}
	stateMu := m.stateLock(deviceID)
	stateMu.Lock()
	defer stateMu.Unlock()
	current := m.CurrentEpoch(deviceID)
	if current != epoch {
		logger.Info("丢弃过期 VoWiFi 启动结果",
			"device", deviceID,
			"startup_epoch", epoch,
			"current_epoch", current)
		return false
	}
	return m.RuntimeStore().ClaimStarted(deviceID, epoch, inst)
}

func (m *Manager) IsCurrentInstance(deviceID string, inst *runtimehost.Instance) bool {
	if m == nil || inst == nil {
		return false
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return false
	}
	return m.RuntimeStore().Instance(deviceID) == inst
}
