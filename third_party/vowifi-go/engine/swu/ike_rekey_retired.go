package swu

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"

	"github.com/iniwex5/vowifi-go/engine/ikev2"
	"go.uber.org/zap"
)

type ikeSAContext struct {
	spiI, spiR     [8]byte
	keys           *IKEKeys
	localInitiator bool
}

type retiredIKEDeleteReceipt struct {
	request, response []byte
}

func (s *Session) hasRetiredIKESA() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.retiredIKESA != nil
}

// dropStaleRetiredIKESA discards an old IKE SA that is still waiting for its
// Delete when the next local IKE rekey is due. Nothing else retires it after
// our own Delete failed, so keeping it would fail every later rekey.
func (s *Session) dropStaleRetiredIKESA() {
	s.mu.Lock()
	retired := s.retiredIKESA
	s.retiredIKESA = nil
	s.mu.Unlock()
	if retired == nil {
		return
	}
	wipeIKEKeys(retired.keys)
	s.Logger.Warn("dropped the old IKE SA still awaiting Delete before rekeying",
		zap.String("old_spi_i", hex.EncodeToString(retired.spiI[:])),
		zap.String("old_spi_r", hex.EncodeToString(retired.spiR[:])),
		zap.Bool("local_initiator", retired.localInitiator))
}

func (s *Session) ikeContextForHeader(header *ikev2.IKEHeader) (ikeSAContext, bool, error) {
	if header == nil {
		return ikeSAContext{}, false, errors.New("swu: missing IKE header")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ikeContextForHeaderLocked(header)
}

// The caller holds mu, including while using the returned key material.
func (s *Session) ikeContextForHeaderLocked(header *ikev2.IKEHeader) (ikeSAContext, bool, error) {
	active := ikeSAContext{
		spiI: s.spiI, spiR: s.spiR, keys: s.ikeKeys,
		localInitiator: s.localIKEInitiator,
	}
	retired := s.retiredIKESA
	if ikeContextMatches(active, header) {
		return active, false, nil
	}
	if retired != nil && ikeContextMatches(*retired, header) {
		return *retired, true, nil
	}
	return ikeSAContext{}, false, errors.New("swu: established IKE packet has unexpected SPIs")
}

func ikeContextMatches(context ikeSAContext, header *ikev2.IKEHeader) bool {
	return header.SPIi == ikeSPIUint64(context.spiI) && header.SPIr == ikeSPIUint64(context.spiR)
}

func validateIKEContextRole(context ikeSAContext, header *ikev2.IKEHeader) error {
	peerInitiator := header.Flags&ikeInitiatorFlag != 0
	if peerInitiator == context.localInitiator {
		return errors.New("swu: established IKE packet has invalid initiator flag")
	}
	return nil
}

func (s *Session) sendIKEContextResponse(
	request *ikev2.IKEPacket,
	payloads []ikev2.Payload,
	context ikeSAContext,
) ([]byte, error) {
	header := packetIKEHeader(request)
	response := &ikev2.IKEPacket{
		Header: newIKEHeader(
			context.spiI, context.spiR, header.ExchangeType,
			ikeFlagsFor(context.localInitiator, true), header.MessageID,
		),
		Payloads: payloads,
	}
	raw, err := s.encryptAndWrapWithKeys(response, header.MessageID, context.keys)
	if err != nil {
		return nil, err
	}
	if err := s.sendIKEPacketSet(s.transport(), [][]byte{raw}); err != nil {
		return nil, err
	}
	return raw, nil
}

func (s *Session) completeRetiredIKESADelete(
	context ikeSAContext,
	request *ikev2.IKEPacket,
	response []byte,
) error {
	requestRaw, err := request.Encode()
	if err != nil {
		return err
	}
	s.mu.Lock()
	retired := s.retiredIKESA
	if retired == nil || retired.spiI != context.spiI || retired.spiR != context.spiR {
		s.mu.Unlock()
		return errors.New("swu: retired IKE SA changed during Delete")
	}
	wipeIKEKeys(retired.keys)
	s.retiredIKESA = nil
	s.retiredIKEDelete = &retiredIKEDeleteReceipt{
		request: append([]byte(nil), requestRaw...), response: append([]byte(nil), response...),
	}
	s.mu.Unlock()
	return nil
}

func (s *Session) matchesRetiredIKEDelete(raw []byte) bool {
	s.mu.RLock()
	receipt := s.retiredIKEDelete
	matched := receipt != nil && bytes.Equal(receipt.request, raw)
	s.mu.RUnlock()
	return matched
}

func (s *Session) resendRetiredIKEDelete(raw []byte) (bool, error) {
	s.mu.RLock()
	receipt := s.retiredIKEDelete
	if receipt == nil || !bytes.Equal(receipt.request, raw) {
		s.mu.RUnlock()
		return false, nil
	}
	response := append([]byte(nil), receipt.response...)
	s.mu.RUnlock()
	return true, s.sendIKEPacketSet(s.transport(), [][]byte{response})
}

func ikeSPIUint64(spi [8]byte) uint64 {
	return binary.BigEndian.Uint64(spi[:])
}
