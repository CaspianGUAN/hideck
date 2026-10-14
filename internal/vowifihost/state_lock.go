package vowifihost

import (
	"strings"
	"sync"
)

// stateLock serializes epoch-scoped state and host effects with invalidation
// for one device. Keep its identity across removal/re-addition: retired callbacks
// must still synchronize with the replacement, not acquire an obsolete lock.
func (m *Manager) stateLock(deviceID string) *sync.Mutex {
	deviceID = strings.TrimSpace(deviceID)
	m.stateLocksMu.Lock()
	defer m.stateLocksMu.Unlock()
	if m.stateLocks == nil {
		m.stateLocks = make(map[string]*sync.Mutex)
	}
	lock := m.stateLocks[deviceID]
	if lock == nil {
		lock = &sync.Mutex{}
		m.stateLocks[deviceID] = lock
	}
	return lock
}
