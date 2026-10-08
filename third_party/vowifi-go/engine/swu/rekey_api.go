package swu

import (
	"errors"
	"fmt"
	"time"

	enginecrypto "github.com/iniwex5/vowifi-go/engine/crypto"
	"github.com/iniwex5/vowifi-go/engine/ikev2"
	"github.com/iniwex5/vowifi-go/engine/logger"
	"go.uber.org/zap"
)

const rekeyCooldown = 30 * time.Second

func (s *Session) RekeyIKESA() error {
	s.rekeyMu.Lock()
	defer s.rekeyMu.Unlock()
	if s.ikeRekeyInCooldown() {
		return nil
	}
	if s.hasRetiredIKESA() {
		return errors.New("swu: previous IKE SA is awaiting Delete")
	}
	return s.performIKESARekey(s.ctx)
}

func (s *Session) RekeyChildSA() error {
	s.rekeyMu.Lock()
	defer s.rekeyMu.Unlock()
	if s.childRekeyInCooldown() {
		return nil
	}
	return s.performChildSARekey(s.ctx)
}

func (s *Session) HandleRekeyIKESARequest(msgID uint32, payloads []ikev2.Payload) error {
	packet := &ikev2.IKEPacket{Header: newIKEHeader(
		s.spiI, s.spiR, ikev2.CREATE_CHILD_SA,
		s.localIKEFlags(false)^ikeInitiatorFlag, msgID,
	)}
	return s.handlePeerIKESARekey(packet, payloads)
}

func (s *Session) handleRekeyIKESAResp(
	data, initiatorNonce []byte,
	newDH *enginecrypto.DiffieHellman,
	newSPIi uint64,
	oldSKd []byte,
	oldSPIi, oldSPIr uint64,
) error {
	packet, err := ikev2.DecodePacket(data)
	if err != nil {
		return fmt.Errorf("swu: decode IKE SA rekey response: %w", err)
	}
	payloads, err := s.decryptAndParse(packet)
	if err != nil {
		return fmt.Errorf("swu: decrypt IKE SA rekey response: %w", err)
	}
	if err := ikeAuthenticationError(payloads); err != nil {
		return err
	}
	s.mu.RLock()
	oldInitiator := s.localIKEInitiator
	s.mu.RUnlock()
	return s.completeInitiatedIKESARekey(initiatedIKERekey{
		ctx: s.ctx, payloads: payloads, nonce: initiatorNonce, dh: newDH,
		initiatorSPI: ikeSPIBytes(newSPIi), oldSKd: oldSKd,
		oldSPIi: ikeSPIBytes(oldSPIi), oldSPIr: ikeSPIBytes(oldSPIr),
		oldInitiator: oldInitiator,
	})
}

func (s *Session) handleCreateChildSAResp(
	data, initiatorNonce []byte,
	newSPI uint32,
	newChildDH *enginecrypto.DiffieHellman,
) error {
	packet, err := ikev2.DecodePacket(data)
	if err != nil {
		return fmt.Errorf("swu: decode CHILD_SA rekey response: %w", err)
	}
	payloads, err := s.decryptAndParse(packet)
	if err != nil {
		return fmt.Errorf("swu: decrypt CHILD_SA rekey response: %w", err)
	}
	if err := childSARejectionError(payloads); err != nil {
		return err
	}
	s.childSAMu.RLock()
	oldLocalSPI, oldRemoteSPI := s.espLocalSPI, s.espRemoteSPI
	s.childSAMu.RUnlock()
	return s.completeInitiatedChildSARekeyWithOld(initiatedChildRekey{
		payloads: payloads, nonce: initiatorNonce, localSPI: newSPI,
		oldLocalSPI: oldLocalSPI, oldRemoteSPI: oldRemoteSPI, newDH: newChildDH,
	})
}

func childSARejectionError(payloads []ikev2.Payload) error {
	for _, payload := range payloads {
		notify, ok := payload.(*ikev2.EncryptedPayloadNotify)
		if ok && notify.NotifyType < 16384 {
			return &createChildSARejectError{NotifyType: notify.NotifyType}
		}
	}
	return nil
}

func (s *Session) signalRekeyReset(channel chan struct{}) {
	if channel == nil {
		return
	}
	select {
	case channel <- struct{}{}:
	default:
	}
}

func (s *Session) ikeRekeyInCooldown() bool {
	s.mu.RLock()
	last := s.lastIKERekeyTime
	s.mu.RUnlock()
	return !last.IsZero() && time.Since(last) < rekeyCooldown
}

func (s *Session) childRekeyInCooldown() bool {
	s.mu.RLock()
	last := s.lastRekeyTime
	s.mu.RUnlock()
	return !last.IsZero() && time.Since(last) < rekeyCooldown
}

func (s *Session) markIKERekeyComplete() {
	s.mu.Lock()
	s.lastIKERekeyTime = time.Now()
	channel := s.rekeyResetCh
	s.mu.Unlock()
	s.signalRekeyReset(channel)
}

func (s *Session) markChildRekeyComplete() {
	s.mu.Lock()
	s.lastRekeyTime = time.Now()
	channel := s.childRekeyResetCh
	s.mu.Unlock()
	s.signalRekeyReset(channel)
}

func (s *Session) handleIncomingCreateChildSAPacket(packet *ikev2.IKEPacket) error {
	payloads, err := s.decryptAndParse(packet)
	if err != nil {
		return err
	}
	return s.handleIncomingCreateChildSAParsed(packetIKEHeader(packet).MessageID, payloads)
}

func (s *Session) handleIncomingCreateChildSAParsed(msgID uint32, payloads []ikev2.Payload) error {
	if !s.rekeyMu.TryLock() {
		return s.sendRekeyCollisionResponse(msgID)
	}
	defer s.rekeyMu.Unlock()
	protocolID, err := createChildSAProtocol(payloads)
	if err != nil {
		return err
	}
	if err := s.narrowPeerRekeySA(payloads, protocolID); err != nil {
		return s.rejectPeerRekeyProposal(msgID, err)
	}
	packet := &ikev2.IKEPacket{Header: newIKEHeader(
		s.spiI, s.spiR, ikev2.CREATE_CHILD_SA, s.localIKEFlags(false)^ikeInitiatorFlag, msgID,
	)}
	if protocolID == ikev2.ProtoIKE {
		if s.hasRetiredIKESA() {
			return s.sendRekeyCollisionResponse(msgID)
		}
		return s.HandleRekeyIKESARequest(msgID, payloads)
	}
	return s.handlePeerChildSARekeyPayloads(packet, payloads)
}

func (s *Session) sendRekeyCollisionResponse(msgID uint32) error {
	return s.sendEncryptedResponseWithMsgID([]ikev2.Payload{
		&ikev2.EncryptedPayloadNotify{NotifyType: ikev2.TEMPORARY_FAILURE},
	}, ikev2.CREATE_CHILD_SA, msgID)
}

func (s *Session) rejectPeerRekeyProposal(msgID uint32, reason error) error {
	logger.Warn("rejecting peer CREATE_CHILD_SA without tearing down the SA", zap.Error(reason))
	if err := s.sendEncryptedResponseWithMsgID([]ikev2.Payload{
		&ikev2.EncryptedPayloadNotify{NotifyType: ikev2.NO_PROPOSAL_CHOSEN},
	}, ikev2.CREATE_CHILD_SA, msgID); err != nil {
		return err
	}
	return nil
}

func createChildSAProtocol(payloads []ikev2.Payload) (ikev2.ProtocolID, error) {
	sa := createChildSAPayload(payloads)
	if sa == nil {
		return 0, errors.New("swu: CREATE_CHILD_SA request missing a single SA proposal")
	}
	var protocol ikev2.ProtocolID
	for _, proposal := range sa.Proposals {
		if proposal == nil {
			continue
		}
		if proposal.ProtocolID != ikev2.ProtoIKE && proposal.ProtocolID != ikev2.ProtoESP {
			return 0, fmt.Errorf("swu: unsupported CREATE_CHILD_SA protocol %d", proposal.ProtocolID)
		}
		if protocol == 0 {
			protocol = proposal.ProtocolID
			continue
		}
		if proposal.ProtocolID != protocol {
			return 0, errors.New("swu: CREATE_CHILD_SA request mixes IKE and ESP proposals")
		}
	}
	if protocol == 0 {
		return 0, errors.New("swu: CREATE_CHILD_SA request missing a single SA proposal")
	}
	return protocol, nil
}

func createChildSAPayload(payloads []ikev2.Payload) *ikev2.EncryptedPayloadSA {
	for _, payload := range payloads {
		sa, ok := payload.(*ikev2.EncryptedPayloadSA)
		if ok && sa != nil && len(sa.Proposals) > 0 {
			return sa
		}
	}
	return nil
}

func (s *Session) handleIncomingInformationalPacket(packet *ikev2.IKEPacket) error {
	s.rekeyMu.Lock()
	defer s.rekeyMu.Unlock()
	return s.handlePeerInformational(packet)
}
