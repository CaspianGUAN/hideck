package swu

import (
	"bytes"
	"errors"
	"testing"

	"github.com/iniwex5/vowifi-go/engine/ikev2"
	enginesim "github.com/iniwex5/vowifi-go/engine/sim"
	"github.com/iniwex5/vowifi-go/engine/swu/eapaka"
)

func identityRequest() *ikev2.EncryptedPayloadNotify {
	return &ikev2.EncryptedPayloadNotify{NotifyType: ikev2.DEVICE_IDENTITY_3GPP, NotifyData: []byte{0, 1, 1}}
}

func newIdentityTestSession(t *testing.T) (*Session, *testIKETransport) {
	t.Helper()
	s := NewSession(&Config{IMSI: "530051234567890", DeviceIdentityIMEI: "358983361433761", WithholdDeviceIdentity: true})
	t.Cleanup(s.cancel)
	transport := newTestIKETransport()
	s.socket, s.ikeKeys = transport, testIKEKeys()
	return s, transport
}

func receiveIdentityReply(t *testing.T, s *Session, transport *testIKETransport) *ikev2.EncryptedPayloadNotify {
	t.Helper()
	packet, err := ikev2.DecodePacket(receiveFragmentPacket(t, transport.sentIKE))
	if err != nil {
		t.Fatal(err)
	}
	payloads, err := s.decryptAndParse(packet)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range payloads {
		if notify, ok := payload.(*ikev2.EncryptedPayloadNotify); ok && notify.NotifyType == ikev2.DEVICE_IDENTITY_3GPP {
			return notify
		}
	}
	return nil
}

func assertStandardIdentityReply(t *testing.T, reply *ikev2.EncryptedPayloadNotify) {
	t.Helper()
	// Length=9, type=IMEI, then TBCD 358983361433761; no legacy notify.
	want := []byte{0, 9, 1, 0x53, 0x98, 0x38, 0x63, 0x41, 0x33, 0x67, 0xf1}
	if reply == nil || !bytes.Equal(reply.NotifyData, want) {
		t.Fatalf("DEVICE_IDENTITY reply = %+v, want data %x", reply, want)
	}
}

func TestRequestedDeviceIdentitySentAfterCertificateAuthentication(t *testing.T) {
	s, transport := newIdentityTestSession(t)
	s.responderAuthenticated = true
	eap, err := (eapaka.Packet{Code: eapaka.CodeRequest, Identifier: 1, Type: eapTypeIdentity}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.applyEAPHandlingResult([]ikev2.Payload{identityRequest(), &ikev2.EncryptedPayloadEAP{EAPMessage: eap}})
	if err != nil {
		t.Fatal(err)
	}
	assertStandardIdentityReply(t, receiveIdentityReply(t, s, transport))
	if s.deviceIdentityRequested {
		t.Fatal("successful send retained pending identity")
	}
	if err := s.sendIKEAuthRequest(nil); err != nil {
		t.Fatal(err)
	}
	if receiveIdentityReply(t, s, transport) != nil {
		t.Fatal("unsolicited repeated identity")
	}
}

func TestRequestedDeviceIdentityWaitsForVerifiedAKA(t *testing.T) {
	s, transport := newIdentityTestSession(t)
	s.eapOnlyRequested = true
	s.cfg.EPDGAddr = "epdg.example"
	eap, err := (eapaka.Packet{Code: eapaka.CodeRequest, Identifier: 1, Type: eapTypeIdentity}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.applyEAPHandlingResult([]ikev2.Payload{identityRequest(), &ikev2.EncryptedPayloadEAP{EAPMessage: eap}})
	if err != nil {
		t.Fatal(err)
	}
	if receiveIdentityReply(t, s, transport) != nil {
		t.Fatal("identity disclosed before network authentication")
	}
	result := enginesim.AKAResult{RES: bytes.Repeat([]byte{5}, 8), CK: bytes.Repeat([]byte{1}, 16), IK: bytes.Repeat([]byte{2}, 16)}
	s.cfg.AKAProvider = &recordingAKAProvider{result: result}
	challenge := signedAKAChallenge(t, s.currentEAPIdentity(), bytes.Repeat([]byte{3}, 16), bytes.Repeat([]byte{4}, 16), result)
	raw, err := challenge.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.applyEAPHandlingResult([]ikev2.Payload{&ikev2.EncryptedPayloadEAP{EAPMessage: raw}}); err != nil {
		t.Fatal(err)
	}
	assertStandardIdentityReply(t, receiveIdentityReply(t, s, transport))
}

func TestDeviceIdentityNoReplyOnInvalidOrUncheckedAKA(t *testing.T) {
	for _, unchecked := range []bool{false, true} {
		s, _ := newIdentityTestSession(t)
		s.deviceIdentityRequested = true
		s.cfg.DisableEAPMACValidation = unchecked
		result := enginesim.AKAResult{RES: bytes.Repeat([]byte{5}, 8), CK: bytes.Repeat([]byte{1}, 16), IK: bytes.Repeat([]byte{2}, 16)}
		s.cfg.AKAProvider = &recordingAKAProvider{result: result}
		challenge := signedAKAChallenge(t, s.currentEAPIdentity(), bytes.Repeat([]byte{3}, 16), bytes.Repeat([]byte{4}, 16), result)
		challenge.Attributes[len(challenge.Attributes)-1] = eapaka.MACAttribute(make([]byte, 16))
		_, err := s.handleRFCChallenge(challenge)
		if !unchecked && err == nil {
			t.Fatal("bad MAC accepted")
		}
		reply, err := s.pendingDeviceIdentityReply()
		if err != nil || reply != nil {
			t.Fatalf("unverified network identity reply = %+v, %v", reply, err)
		}
	}
}

func TestDeviceIdentityRequestParsing(t *testing.T) {
	for _, test := range []struct {
		name          string
		data          []byte
		want, invalid bool
	}{
		{"imei", []byte{0, 1, 1}, true, false},
		{"imeisv", []byte{0, 1, 2}, true, false},
		{"reserved", []byte{0, 1, 3}, false, false},
		{"value-present", []byte{0, 2, 1, 0x12}, false, false},
		{"truncated", []byte{0, 1}, false, true},
		{"wrong-length", []byte{0, 9, 1}, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			notify := identityRequest()
			notify.NotifyData = test.data
			got, err := hasDeviceIdentityRequest([]ikev2.Payload{notify})
			if got != test.want || (err != nil) != test.invalid {
				t.Fatalf("request=%t err=%v", got, err)
			}
		})
	}
}

func TestDeviceIdentityRequestSurvivesSendFailure(t *testing.T) {
	s, transport := newIdentityTestSession(t)
	s.responderAuthenticated, s.deviceIdentityRequested = true, true
	transport.sendIKEErr = errors.New("send failed")
	if err := s.sendIKEAuthRequest(nil); err == nil || !s.deviceIdentityRequested {
		t.Fatal("failed send lost request")
	}
	transport.sendIKEErr = nil
	if err := s.sendIKEAuthRequest(nil); err != nil {
		t.Fatal(err)
	}
	assertStandardIdentityReply(t, receiveIdentityReply(t, s, transport))
}

func TestInformationalDeviceIdentityRequest(t *testing.T) {
	s, transport := newIdentityTestSession(t)
	request := &ikev2.IKEPacket{Header: newIKEHeader(s.spiI, s.spiR, ikev2.INFORMATIONAL, 0, 9), Payloads: []ikev2.Payload{identityRequest()}}
	raw, err := s.encryptAndWrap(request)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := ikev2.DecodePacket(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.handlePeerInformational(packet); err != nil {
		t.Fatal(err)
	}
	assertStandardIdentityReply(t, receiveIdentityReply(t, s, transport))
}

func TestDeviceIdentityUnavailableAndInvalid(t *testing.T) {
	s, _ := newIdentityTestSession(t)
	s.cfg.DeviceIdentityIMEI = ""
	s.responderAuthenticated, s.deviceIdentityRequested = true, true
	if reply, err := s.pendingDeviceIdentityReply(); err != nil || reply != nil {
		t.Fatalf("missing identity reply=%v err=%v", reply, err)
	}
	s.cfg.DeviceIdentityIMEI = "bad-imei"
	if _, err := s.pendingDeviceIdentityReply(); err == nil {
		t.Fatal("invalid configured identity accepted")
	}
}
