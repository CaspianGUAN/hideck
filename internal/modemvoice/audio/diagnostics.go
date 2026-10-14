package audio

import (
	"strings"
	"sync"
)

// ALSA diagnostics contain device/format errors. Bound retained output so a
// malfunctioning tool cannot accumulate unbounded stderr during a long call.
const diagnosticBytes = 4096

type diagnosticBuffer struct {
	mu   sync.Mutex
	tail []byte
}

func (b *diagnosticBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(data)
	if n >= diagnosticBytes {
		b.tail = append(b.tail[:0], data[n-diagnosticBytes:]...)
		return n, nil
	}
	b.tail = append(b.tail, data...)
	if len(b.tail) > diagnosticBytes {
		b.tail = append([]byte(nil), b.tail[len(b.tail)-diagnosticBytes:]...)
	}
	return n, nil
}

func (b *diagnosticBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.tail))
}
