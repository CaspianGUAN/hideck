package swu

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/engine/ikev2"
)

// invalidIKESPIResponse is the 36-byte reply AIS's ePDG sent to our DPD
// after it had dropped the IKE SA (2026-10-14).
func invalidIKESPIResponse(spiI [8]byte, msgID uint32) []byte {
	raw := make([]byte, ikev2.IKE_HEADER_LEN+8)
	copy(raw, spiI[:])
	raw[16] = byte(ikev2.PayloadNotify)
	raw[17] = 0x20
	raw[18] = byte(ikev2.INFORMATIONAL)
	raw[19] = ikeResponseFlag
	binary.BigEndian.PutUint32(raw[20:], msgID)
	binary.BigEndian.PutUint32(raw[24:], uint32(len(raw)))
	binary.BigEndian.PutUint16(raw[30:], 8)
	binary.BigEndian.PutUint16(raw[34:], ikev2.INVALID_IKE_SPI)
	return raw
}

func TestInvalidIKESPIAnsweringPendingDPDFailsSession(t *testing.T) {
	session, transport := newEstablishedControlSessionWithConfig(t, &Config{Retransmit: &RetransmitConfig{
		MaxRetries: 5, InitialDelay: 10 * time.Second, Backoff: 1,
	}})
	defer stopControlTestSession(session)
	result := make(chan error, 1)
	go func() { result <- session.DPDProbe() }()
	header, err := ikev2.DecodeHeader(receiveSentIKE(t, transport))
	if err != nil {
		t.Fatal(err)
	}
	if err := session.dispatchIKEPacket(
		invalidIKESPIResponse(session.spiI, header.MessageID), session.taskMgr, make(chan []byte, 1),
	); !errors.Is(err, errPeerLostIKESA) {
		t.Fatalf("dispatch error = %v, want errPeerLostIKESA", err)
	}
}

func TestInvalidIKESPIWithoutPendingRequestIsDropped(t *testing.T) {
	session, _ := newEstablishedControlSession(t)
	defer stopControlTestSession(session)
	if err := session.dispatchIKEPacket(
		invalidIKESPIResponse(session.spiI, 99), session.taskMgr, make(chan []byte, 1),
	); err != nil {
		t.Fatalf("unsolicited INVALID_IKE_SPI was fatal: %v", err)
	}
	other := session.spiI
	other[0] ^= 0xff
	if session.peerLostIKESA(mustDecodeHeader(t, invalidIKESPIResponse(other, 1)),
		invalidIKESPIResponse(other, 1), session.taskMgr) {
		t.Fatal("INVALID_IKE_SPI for another SPIi was accepted")
	}
}

func mustDecodeHeader(t *testing.T, raw []byte) *ikev2.IKEHeader {
	t.Helper()
	header, err := ikev2.DecodeHeader(raw)
	if err != nil {
		t.Fatal(err)
	}
	return header
}
