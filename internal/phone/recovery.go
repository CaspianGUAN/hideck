package phone

import (
	"context"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/yibaiba/hideck/pkg/logger"
)

const disconnectHangupTimeout = 10 * time.Second

type pendingMediaDrop struct {
	timer *time.Timer
}

func (s *Service) handleMediaState(mediaID string, state webrtc.PeerConnectionState) {
	logger.Info("浏览器媒体连接状态", "media_id", mediaID, "state", state.String())
	if state == webrtc.PeerConnectionStateConnected {
		s.cancelDisconnectTimer(mediaID)
		return
	}
	if state == webrtc.PeerConnectionStateDisconnected ||
		state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
		s.scheduleDisconnectHangup(mediaID)
	}
}

func (s *Service) cancelDisconnectTimer(mediaID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearPendingMediaDropLocked(mediaID)
	call := s.calls[s.mediaCalls[mediaID]]
	if call != nil && call.disconnectTimer != nil {
		call.disconnectTimer.Stop()
		call.disconnectTimer = nil
	}
}

func (s *Service) scheduleDisconnectHangup(mediaID string) {
	s.mu.Lock()
	mediaExists := s.media.Get(mediaID) != nil
	callID := s.mediaCalls[mediaID]
	call := s.calls[callID]
	if call == nil || call.terminal || call.mediaID != mediaID || call.disconnectTimer != nil {
		if callID == "" && mediaExists && s.pendingMediaDrops[mediaID] == nil {
			pending := &pendingMediaDrop{}
			s.pendingMediaDrops[mediaID] = pending
			if s.mediaReservations[mediaID] == nil {
				s.armUnboundMediaDropLocked(mediaID, pending)
			}
		}
		s.mu.Unlock()
		return
	}
	call.disconnectTimer = time.AfterFunc(s.recoveryGrace, func() { s.expireDisconnectedMedia(callID, mediaID) })
	s.mu.Unlock()
	s.publish("media_disconnected", call)
}

func (s *Service) resumePendingMediaDrop(mediaID string, pending bool) {
	if pending {
		s.scheduleDisconnectHangup(mediaID)
	}
}

func (s *Service) expireDisconnectedMedia(callID, mediaID string) {
	s.mu.RLock()
	call := s.calls[callID]
	valid := call != nil && !call.terminal && call.mediaID == mediaID
	deviceID, resolvedCallID := "", ""
	if valid {
		deviceID, resolvedCallID = call.view.DeviceID, call.view.CallID
	}
	s.mu.RUnlock()
	if !valid {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, disconnectHangupTimeout)
	defer cancel()
	logger.Warn("浏览器媒体连接未恢复，自动挂断", "device_id", deviceID, "call_id", resolvedCallID)
	if err := s.gateway.HangupCall(ctx, deviceID, resolvedCallID); err != nil {
		logger.Error("媒体恢复超时挂断失败", "device_id", deviceID, "call_id", resolvedCallID, "err", err)
	}
}

func (s *Service) expireUnboundMedia(mediaID string, pending *pendingMediaDrop) {
	s.mu.Lock()
	if s.pendingMediaDrops[mediaID] != pending || s.mediaCalls[mediaID] != "" || s.mediaReservations[mediaID] != nil {
		s.mu.Unlock()
		return
	}
	delete(s.pendingMediaDrops, mediaID)
	media := s.media.detach(mediaID)
	s.mu.Unlock()
	if media == nil {
		return
	}
	logger.Warn("未绑定电话的浏览器媒体连接未恢复，释放服务端会话", "media_id", mediaID)
	_ = media.Close()
}

func (s *Service) clearPendingMediaDropLocked(mediaID string) bool {
	drop, pending := s.pendingMediaDrops[mediaID]
	if !pending {
		return false
	}
	delete(s.pendingMediaDrops, mediaID)
	if drop.timer != nil {
		drop.timer.Stop()
	}
	return true
}

func (s *Service) pausePendingMediaDropLocked(mediaID string) {
	pending := s.pendingMediaDrops[mediaID]
	if pending == nil || pending.timer == nil {
		return
	}
	pending.timer.Stop()
	pending.timer = nil
}

func (s *Service) resumeUnboundMediaDropLocked(mediaID string) {
	pending := s.pendingMediaDrops[mediaID]
	if pending == nil || pending.timer != nil || s.media.Get(mediaID) == nil {
		return
	}
	s.armUnboundMediaDropLocked(mediaID, pending)
}

func (s *Service) armUnboundMediaDropLocked(mediaID string, pending *pendingMediaDrop) {
	pending.timer = time.AfterFunc(s.recoveryGrace, func() { s.expireUnboundMedia(mediaID, pending) })
}

func (s *Service) stopPendingMediaDropTimers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for mediaID, pending := range s.pendingMediaDrops {
		if pending.timer != nil {
			pending.timer.Stop()
		}
		delete(s.pendingMediaDrops, mediaID)
	}
}
