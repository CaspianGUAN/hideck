package swu

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/engine/ikev2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestControlLoopAnswersPeerDPDWhileLocalRequestIsPending(t *testing.T) {
	session, transport := newEstablishedControlSession(t)
	defer stopControlTestSession(session)
	result := make(chan error, 1)
	go func() { result <- session.DPDProbe() }()

	localRaw := receiveSentIKE(t, transport)
	localRequest, err := ikev2.DecodePacket(localRaw)
	if err != nil {
		t.Fatalf("decode local DPD: %v", err)
	}
	peerRequest := &ikev2.IKEPacket{
		Header: newIKEHeader(
			session.spiI, session.spiR, ikev2.INFORMATIONAL, 0, 41,
		),
	}
	peerRaw, err := session.encryptAndWrap(peerRequest)
	if err != nil {
		t.Fatalf("encrypt peer DPD: %v", err)
	}
	transport.ike <- peerRaw

	peerResponseRaw := receiveSentIKE(t, transport)
	peerResponse, err := ikev2.DecodePacket(peerResponseRaw)
	if err != nil || peerResponse.MessageID != 41 ||
		peerResponse.Flags != ikeInitiatorFlag|ikeResponseFlag {
		t.Fatalf("peer DPD response id=%d flags=%02x err=%v", peerResponse.MessageID, peerResponse.Flags, err)
	}

	localResponse := &ikev2.IKEPacket{
		Header: newIKEHeader(
			session.spiI, session.spiR, ikev2.INFORMATIONAL,
			ikeResponseFlag, localRequest.MessageID,
		),
	}
	localResponseRaw, err := session.encryptAndWrap(localResponse)
	if err != nil {
		t.Fatalf("encrypt local DPD response: %v", err)
	}
	transport.ike <- localResponseRaw
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("local DPD: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("local DPD transaction did not complete")
	}
}

// RFC 7296 2.21 and 2.21.4: messages that are malformed, outside our IKE SA
// or fail their integrity check are audited, never acted on, and do not count
// as peer liveness.
func TestControlLoopDropsInvalidIKEPackets(t *testing.T) {
	core, observed := observer.New(zapcore.WarnLevel)
	session, transport := newEstablishedControlSession(t, zap.New(core))
	defer stopControlTestSession(session)
	session.activityMu.Lock()
	before := session.lastInboundTime
	session.activityMu.Unlock()

	invalid, err := (&ikev2.IKEPacket{
		Header: &ikev2.IKEHeader{
			SPIi: 99, SPIr: 100, Version: 0x20,
			ExchangeType: ikev2.INFORMATIONAL, Flags: ikeResponseFlag, MessageID: 1,
		},
	}).Encode()
	if err != nil {
		t.Fatalf("encode invalid response: %v", err)
	}
	transport.ike <- invalid
	// A datagram whose IKE length field disagrees with its size, like ESP
	// bytes taken for IKE.
	garbled := append([]byte(nil), invalid...)
	garbled[24], garbled[25] = 0x08, 0xe3
	transport.ike <- garbled
	// A peer request on our SPIs whose integrity check fails.
	forged, err := session.encryptAndWrap(&ikev2.IKEPacket{
		Header: newIKEHeader(session.spiI, session.spiR, ikev2.INFORMATIONAL, 0, 40),
	})
	if err != nil {
		t.Fatalf("encrypt peer request: %v", err)
	}
	forged[len(forged)-1] ^= 0xff
	transport.ike <- forged

	deadline := time.Now().Add(time.Second)
	for observed.FilterMessage("dropped invalid IKE packet").Len() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-session.done:
		t.Fatalf("invalid IKE packets ended the session: %v", session.TerminalError())
	default:
	}
	if got := observed.FilterMessage("dropped invalid IKE packet").Len(); got != 3 {
		t.Fatalf("dropped-packet warnings = %d, want 3", got)
	}
	session.activityMu.Lock()
	after := session.lastInboundTime
	session.activityMu.Unlock()
	if !after.Equal(before) {
		t.Fatal("invalid IKE packets refreshed peer liveness")
	}
}

func TestControlLoopRetainsResponseUntilLegacyWaiterRegisters(t *testing.T) {
	session, transport := newEstablishedControlSession(t)
	defer stopControlTestSession(session)
	response, err := session.encryptAndWrap(&ikev2.IKEPacket{Header: newIKEHeader(
		session.spiI, session.spiR, ikev2.IKE_AUTH, ikeResponseFlag, 77,
	)})
	if err != nil {
		t.Fatalf("encode early response: %v", err)
	}
	transport.ike <- response
	deadline := time.Now().Add(time.Second)
	key := ikeWaitKey{exchangeType: ikev2.IKE_AUTH, msgID: 77}
	for {
		session.controlMu.RLock()
		_, pending := session.ikePending[key]
		session.controlMu.RUnlock()
		if pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("early response was not retained")
		}
		time.Sleep(time.Millisecond)
	}
	got, err := session.receiveIKEResponseWithTimeout(ikev2.IKE_AUTH, 77, time.Second)
	if err != nil || string(got) != string(response) {
		t.Fatalf("retained response len=%d err=%v", len(got), err)
	}
}

func TestEstablishedExchangePropagatesTaskManagerSendFailure(t *testing.T) {
	session, transport := newEstablishedControlSessionWithConfig(t, &Config{Retransmit: &RetransmitConfig{
		MaxRetries: 0, InitialDelay: time.Millisecond, Backoff: 1, PollInterval: time.Millisecond,
	}})
	defer stopControlTestSession(session)
	sendErr := errors.New("send failed")
	transport.sendIKEErr = sendErr

	err := session.DPDProbe()
	if !errors.Is(err, ErrWindowTimeout) || !strings.Contains(err.Error(), sendErr.Error()) {
		t.Fatalf("DPD send failure = %v", err)
	}
}

func receiveSentIKE(t *testing.T, transport *testIKETransport) []byte {
	t.Helper()
	select {
	case raw := <-transport.sentIKE:
		return raw
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for sent IKE packet")
		return nil
	}
}
