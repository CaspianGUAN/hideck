package swu

import (
	"bytes"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/engine/ikev2"
)

func TestValidIKEResponseHeader(t *testing.T) {
	request := &ikev2.IKEPacket{
		InitiatorSPI: [8]byte{1}, ResponderSPI: [8]byte{2},
		ExchangeType: ikev2.ExchangeIKEAuth, Flags: 0x08, MessageID: 3,
	}
	response := &ikev2.IKEPacket{
		InitiatorSPI: [8]byte{1}, ResponderSPI: [8]byte{2},
		ExchangeType: ikev2.ExchangeIKEAuth, Flags: 0x20, MessageID: 3,
	}
	if !validIKEResponseHeader(response, request) {
		t.Fatal("matching responder header was rejected")
	}

	invalid := *response
	invalid.ExchangeType = ikev2.ExchangeInformational
	if validIKEResponseHeader(&invalid, request) {
		t.Fatal("response with a different exchange type was accepted")
	}
	invalid = *response
	invalid.Flags = 0x28
	if validIKEResponseHeader(&invalid, request) {
		t.Fatal("response carrying the initiator flag was accepted")
	}
	invalid = *response
	invalid.ResponderSPI = [8]byte{9}
	if validIKEResponseHeader(&invalid, request) {
		t.Fatal("response with a different responder SPI was accepted")
	}
}

func TestInvalidIKEResponseKeepsTransactionPending(t *testing.T) {
	session, transport := newEstablishedControlSessionWithConfig(t, &Config{Retransmit: &RetransmitConfig{
		MaxRetries: 1, InitialDelay: 200 * time.Millisecond, Backoff: 1,
	}})
	defer stopControlTestSession(session)
	result := make(chan error, 1)
	go func() { result <- session.DPDProbe() }()
	request := receiveSentIKE(t, transport)
	header, err := ikev2.DecodeHeader(request)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := session.encryptAndWrap(&ikev2.IKEPacket{Header: newIKEHeader(
		session.spiI, session.spiR, ikev2.INFORMATIONAL, ikeResponseFlag, header.MessageID,
	)})
	if err != nil {
		t.Fatal(err)
	}
	invalid := append([]byte(nil), valid...)
	invalid[len(invalid)-1] ^= 0xff
	transport.ike <- invalid
	select {
	case err := <-result:
		t.Fatalf("invalid response completed transaction: %v", err)
	case retry := <-transport.sentIKE:
		if !bytes.Equal(request, retry) {
			t.Fatal("retransmission changed the pending request")
		}
	case <-time.After(time.Second):
		t.Fatal("invalid response stopped retransmission")
	}
	transport.ike <- valid
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("valid response: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("valid response did not complete pending transaction")
	}
}

func TestInvalidIKEResponseIsNotCachedOrCountedAsLiveness(t *testing.T) {
	session, _ := newEstablishedControlSession(t)
	defer stopControlTestSession(session)
	raw, err := session.encryptAndWrap(&ikev2.IKEPacket{Header: newIKEHeader(
		session.spiI, session.spiR, ikev2.IKE_AUTH, ikeResponseFlag, 77,
	)})
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xff
	session.activityMu.Lock()
	before := session.lastInboundTime
	session.activityMu.Unlock()
	if err := session.dispatchIKEPacket(raw, session.taskMgr, make(chan []byte, 1)); err != nil {
		t.Fatalf("invalid response was fatal: %v", err)
	}
	session.controlMu.RLock()
	_, cached := session.ikePending[ikeWaitKey{exchangeType: ikev2.IKE_AUTH, msgID: 77}]
	session.controlMu.RUnlock()
	if cached {
		t.Fatal("unauthenticated response cached for a future waiter")
	}
	session.activityMu.Lock()
	after := session.lastInboundTime
	session.activityMu.Unlock()
	if !after.Equal(before) {
		t.Fatal("unauthenticated response refreshed peer liveness")
	}
}
