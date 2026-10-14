package modem

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ExecuteATContext uses the existing serial queue. Each request owns its reply
// channels, so cancellation cannot deliver a late response to a later command.
// Once written, the queue still drains the command's terminal response/timeout.
func (m *Manager) ExecuteATContext(ctx context.Context, cmd string, timeout time.Duration) (string, error) {
	if ctx == nil {
		return "", errors.New("AT context is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if m == nil || !m.CanExecuteAT() || !m.IsHealthy() {
		return "", errors.New("AT 管理器未启动或不可用")
	}
	if timeout <= 0 || strings.TrimSpace(cmd) == "" || strings.ContainsAny(cmd, "\r\n") {
		return "", errors.New("invalid AT request")
	}
	req := commandRequest{ctx: ctx, cmd: cmd, timeout: timeout, silent: true,
		respChan: make(chan string, 1), errChan: make(chan error, 1)}
	return m.enqueueContext(ctx, req)
}

// WaitATIdle fences requests already queued on the normal AT queue. Cancel
// the session and join its callers first, so none can enqueue behind the fence.
// Cancellation of ExecuteATContext alone does not mean an on-wire ATD finished.
func (m *Manager) WaitATIdle(ctx context.Context) error {
	if ctx == nil {
		return errors.New("AT context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if m == nil {
		return errors.New("AT 管理器未启动或不可用，无法确认队列已完成")
	}
	select {
	case <-m.stop:
		return m.waitStopped(ctx)
	default:
	}
	if !m.CanExecuteAT() || !m.IsHealthy() {
		return errors.New("AT 管理器未启动或不可用，无法确认队列已完成")
	}
	req := commandRequest{ctx: ctx, barrier: true,
		respChan: make(chan string, 1), errChan: make(chan error, 1)}
	_, err := m.enqueueContext(ctx, req)
	// Stop can win while the queue fence is pending. Only the completed join
	// proves that no old serial operation can overlap the replacement runtime.
	if err != nil {
		select {
		case <-m.stop:
			return m.waitStopped(ctx)
		default:
		}
	}
	return err
}

func (m *Manager) waitStopped(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.stopped:
		return ctx.Err()
	}
}

func (m *Manager) enqueueContext(ctx context.Context, req commandRequest) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-m.stop:
		return "", errors.New("manager stopped")
	case m.cmdChan <- req:
	}
	select {
	case response := <-req.respChan:
		return response, nil
	case err := <-req.errChan:
		return "", err
	case <-ctx.Done():
		return "", ctx.Err()
	case <-m.stop:
		return "", errors.New("manager stopped")
	}
}

func isVoiceCommandFailure(command, response string) bool {
	command = strings.ToUpper(strings.TrimSpace(command))
	voice := command == "ATA" || (strings.HasPrefix(command, "ATD") && strings.HasSuffix(command, ";"))
	if !voice {
		return false
	}
	switch strings.TrimSpace(response) {
	case "NO CARRIER", "BUSY", "NO ANSWER", "NO DIALTONE":
		return true
	default:
		return false
	}
}

// SubscribeVoiceChanges coalesces wakeups, not call state. Consumers fetch CLCC
// after each wakeup; this never replaces existing RING/CLIP/SMS callbacks.
func (m *Manager) SubscribeVoiceChanges() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	m.voiceURCMu.Lock()
	select {
	case <-m.stop:
		close(ch)
		m.voiceURCMu.Unlock()
		return ch, func() {}
	default:
	}
	if m.voiceURCSubs == nil {
		m.voiceURCSubs = make(map[chan struct{}]struct{})
	}
	m.voiceURCSubs[ch] = struct{}{}
	m.voiceURCMu.Unlock()
	return ch, func() {
		m.voiceURCMu.Lock()
		if _, exists := m.voiceURCSubs[ch]; exists {
			delete(m.voiceURCSubs, ch)
			close(ch)
		}
		m.voiceURCMu.Unlock()
	}
}

func (m *Manager) notifyVoiceURC(line string) {
	key := urcKey(line)
	if line == "MO CONNECTED" {
		key = line
	}
	switch key {
	case "RING", "+CLIP", "+CCWA", "NO CARRIER", "BUSY", "NO ANSWER", "CONNECT", "MO CONNECTED":
	default:
		return
	}
	m.voiceURCMu.Lock()
	defer m.voiceURCMu.Unlock()
	for ch := range m.voiceURCSubs {
		select {
		case ch <- struct{}{}:
		default: // An outstanding wakeup already requests a fresh CLCC snapshot.
		}
	}
}

func (m *Manager) closeVoiceChanges() {
	m.voiceURCMu.Lock()
	defer m.voiceURCMu.Unlock()
	for ch := range m.voiceURCSubs {
		delete(m.voiceURCSubs, ch)
		close(ch)
	}
}
