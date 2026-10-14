package vowifihost

import "github.com/iniwex5/vowifi-go/runtimehost"

// RecordStartupStateForEpoch atomically rejects observations from a retired
// start, including its health/publication effects, not just the cached state.
func (m *Manager) RecordStartupStateForEpoch(deviceID string, epoch uint64, state runtimehost.State) bool {
	stateMu := m.stateLock(deviceID)
	stateMu.Lock()
	defer stateMu.Unlock()
	if !m.ShouldRun(deviceID, epoch) {
		return false
	}
	return m.RecordStartupState(deviceID, state)
}
