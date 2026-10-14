package modem

func (m *Manager) isRunning() bool {
	m.lifecycleStateMu.RLock()
	defer m.lifecycleStateMu.RUnlock()
	return m.running
}

func (m *Manager) setRunning(running bool) {
	m.lifecycleStateMu.Lock()
	m.running = running
	m.lifecycleStateMu.Unlock()
}

func (m *Manager) markUnhealthy() {
	m.lifecycleStateMu.Lock()
	m.healthy = false
	m.lifecycleStateMu.Unlock()
}
