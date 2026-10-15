package swu

import (
	"encoding/binary"
	"errors"

	"github.com/iniwex5/vowifi-go/engine/ikev2"
	"go.uber.org/zap"
)

var errPeerLostIKESA = errors.New("swu: peer answered with INVALID_IKE_SPI; the ePDG no longer has this IKE SA")

// peerLostIKESA recognizes the unprotected INVALID_IKE_SPI response an ePDG
// sends to a request on an IKE SA it has already discarded (RFC 7296
// 2.21.4). Such a message is only a hint, so it counts only when it carries
// our SPIi and answers a request we are still retransmitting: an off-path
// sender cannot guess both, and the request would otherwise just run out its
// retries (AIS, 2026-10-14: DPD spent three more minutes before giving up).
func (s *Session) peerLostIKESA(header *ikev2.IKEHeader, raw []byte, taskManager *TaskManager) bool {
	if header == nil || taskManager == nil || header.SPIr != 0 ||
		header.Flags&ikeResponseFlag == 0 || header.NextPayload != ikev2.PayloadNotify ||
		len(raw) < ikev2.IKE_HEADER_LEN+8 {
		return false
	}
	if binary.BigEndian.Uint16(raw[ikev2.IKE_HEADER_LEN+6:]) != ikev2.INVALID_IKE_SPI {
		return false
	}
	s.mu.RLock()
	ours := header.SPIi == ikeSPIUint64(s.spiI)
	s.mu.RUnlock()
	if !ours {
		return false
	}
	taskManager.mu.Lock()
	_, pending := taskManager.pending[header.MessageID]
	taskManager.mu.Unlock()
	if !pending {
		return false
	}
	if s.Logger != nil {
		s.Logger.Warn("ePDG no longer knows the IKE SA; reconnecting",
			zap.Uint32("msg_id", header.MessageID), zap.Uint8("exchange", uint8(header.ExchangeType)))
	}
	return true
}
