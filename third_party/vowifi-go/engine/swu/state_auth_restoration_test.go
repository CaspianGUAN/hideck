package swu

import (
	"bytes"
	"testing"

	"github.com/iniwex5/vowifi-go/engine/ikev2"
	"github.com/iniwex5/vowifi-go/engine/swu/eapaka"
)

func TestNormalizeAKAChallengeModeMatchesLegacyAliases(t *testing.T) {
	tests := map[string]string{
		"": "minimal", "minimal": "minimal",
		"off": "off", "NONE": "off", "omit": "off", "no_checkcode": "off",
		"echo": "checkcode", "checkcode": "checkcode",
		"recalc": "recompute", " ReCompute ": "recompute",
		"custom": "custom",
	}
	for input, want := range tests {
		if got := normalizeAKAChallengeMode(input); got != want {
			t.Errorf("normalizeAKAChallengeMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLegacyIdentitySelectionAndKeyDerivationFallback(t *testing.T) {
	session := NewSession(&Config{IMSI: "234102356143376", FastReauthID: "fast@example"})
	if got := session.currentIKEIdentity(); got != "fast@example" {
		t.Fatalf("IKE identity = %q", got)
	}
	if got := session.currentEAPIdentity(); got != "fast@example" {
		t.Fatalf("EAP identity = %q", got)
	}
	session.eapIdentity, session.eapIdentitySet = "", true
	if got := session.currentEAPIdentity(); got != "" {
		t.Fatalf("explicit empty EAP identity = %q", got)
	}
	if got := session.currentEAPIdentityForKeyDerivation(); got != buildNAI(session.cfg.IMSI, session.cfg) {
		t.Fatalf("key derivation identity = %q", got)
	}
}

func TestBuildCPRequestPayloadHonorsIPStack(t *testing.T) {
	tests := []struct {
		mode string
		want []uint16
	}{
		{"ipv4", []uint16{1, 3, 20}},
		{"ipv6", []uint16{8, 10, 21, 16390}},
		{"", []uint16{1, 3, 20, 8, 10, 21, 16390}},
	}
	for _, test := range tests {
		session := NewSession(&Config{IPStackType: test.mode})
		payload := session.buildCPRequestPayload()
		if len(payload.Attributes) != len(test.want) {
			t.Fatalf("IPStack %q attributes = %d", test.mode, len(payload.Attributes))
		}
		for index, want := range test.want {
			attribute := payload.Attributes[index]
			if attribute.Type != want {
				t.Errorf("IPStack %q attribute[%d] = %d, want %d", test.mode, index, attribute.Type, want)
			}
			if want == ikev2.CPAttrIP6Address && (len(attribute.Value) != 17 || attribute.Value[16] != 64) {
				t.Errorf("IPv6 request = %x", attribute.Value)
			}
		}
	}
}

func TestInitialIKEAuthRestoresNotifyOrderAndDeviceIdentity(t *testing.T) {
	session := NewSession(&Config{
		IMSI: "234102356143376", APN: "ims", DeviceIdentityIMEI: "358983361433761",
	})
	payloads, err := session.buildIKEAuthInitPayloads()
	if err != nil {
		t.Fatalf("buildIKEAuthInitPayloads: %v", err)
	}
	wantNotify := []uint16{
		ikev2.EAP_ONLY_AUTHENTICATION, ikev2.MOBIKE_SUPPORTED,
		ikev2.TICKET_REQUEST, ikev2.INITIAL_CONTACT,
		ikev2.DEVICE_IDENTITY_3GPP, ikev2.DEVICE_IDENTITY,
	}
	var notifications []*ikev2.EncryptedPayloadNotify
	for _, payload := range payloads {
		if notification, ok := payload.(*ikev2.EncryptedPayloadNotify); ok {
			notifications = append(notifications, notification)
		}
	}
	if len(notifications) != len(wantNotify) {
		t.Fatalf("notifications = %d, want %d", len(notifications), len(wantNotify))
	}
	for index, want := range wantNotify {
		if notifications[index].NotifyType != want {
			t.Errorf("notification[%d] = %d, want %d", index, notifications[index].NotifyType, want)
		}
	}
	// TS 24.302 8.2.9.2: 2-octet length (type + value), type 1 = IMEI, BCD.
	want := []byte{0x00, 0x09, 0x01, 0x53, 0x98, 0x38, 0x63, 0x41, 0x33, 0x67, 0xf1}
	if !bytes.Equal(notifications[4].NotifyData, want) || !bytes.Equal(notifications[5].NotifyData, want) {
		t.Fatalf("device identity notify data = %x / %x, want %x", notifications[4].NotifyData, notifications[5].NotifyData, want)
	}
}

func TestOverlappingReauthOmitsInitialContact(t *testing.T) {
	session := NewSession(&Config{
		IMSI: "234102356143376", APN: "ims", OmitInitialContact: true,
	})
	payloads, err := session.buildIKEAuthInitPayloads()
	if err != nil {
		t.Fatalf("buildIKEAuthInitPayloads: %v", err)
	}
	for _, payload := range payloads {
		notification, ok := payload.(*ikev2.EncryptedPayloadNotify)
		if ok && notification.NotifyType == ikev2.INITIAL_CONTACT {
			t.Fatal("overlapping IKE_AUTH included INITIAL_CONTACT")
		}
	}
}

func TestSpoofAppleIMEIRestoresFixedTACAndLuhn(t *testing.T) {
	if got := spoofAppleIMEI("234102356143376"); got != "358983361433761" {
		t.Fatalf("spoofAppleIMEI = %q", got)
	}
	if got := spoofAppleIMEI("short"); got != "358983361234565" {
		t.Fatalf("short IMSI fallback = %q", got)
	}
}

func TestHandleEAPReturnsPayloadWithoutSending(t *testing.T) {
	transport := newTestIKETransport()
	session := NewSession(&Config{IMSI: "234102356143376"})
	session.socket = transport
	request := []byte{eapaka.CodeRequest, 7, 0, 5, eapTypeIdentity}
	payloads, err := session.handleEAP(request)
	if err != nil {
		t.Fatalf("handleEAP: %v", err)
	}
	if transport.sendCount.Load() != 0 {
		t.Fatal("handleEAP sent on the transport")
	}
	if len(payloads) != 1 || payloads[0].Type() != ikev2.PayloadEAP {
		t.Fatalf("response payloads = %#v", payloads)
	}
	response, err := eapaka.ParsePacket(payloads[0].(*ikev2.EncryptedPayloadEAP).EAPMessage)
	if err != nil {
		t.Fatalf("parse EAP response: %v", err)
	}
	if response.Code != eapaka.CodeResponse || response.Identifier != 7 || string(response.Data) != buildNAI(session.cfg.IMSI, session.cfg) {
		t.Fatalf("EAP identity response = %#v", response)
	}
}

func TestAKAIdentityWithoutIDRequestReturnsNoIdentityAttribute(t *testing.T) {
	session := NewSession(&Config{IMSI: "234102356143376"})
	request := eapaka.Packet{
		Code: eapaka.CodeRequest, Identifier: 9, Type: eapaka.TypeAKA,
		Subtype: eapaka.SubtypeIdentity,
	}
	raw, err := request.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	payloads, err := session.handleEAP(raw)
	if err != nil {
		t.Fatalf("handleEAP: %v", err)
	}
	response, err := eapaka.ParsePacket(payloads[0].(*ikev2.EncryptedPayloadEAP).EAPMessage)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if len(response.Attributes) != 0 || !session.eapIdentitySet || session.eapIdentity != "" {
		t.Fatalf("identity response = %#v, stored=%q/%t", response.Attributes, session.eapIdentity, session.eapIdentitySet)
	}
}

func TestDisableEAPMACValidationIsExplicitAndScoped(t *testing.T) {
	result := testAKAResult()
	strict := NewSession(&Config{
		IMSI: "234102356143376", AKAProvider: &recordingAKAProvider{result: result},
	})
	challenge := signedAKAChallenge(
		t, strict.currentEAPIdentity(), bytes.Repeat([]byte{0x31}, 16),
		bytes.Repeat([]byte{0x42}, 16), result,
	)
	challenge.Attributes[len(challenge.Attributes)-1].Data[2] ^= 0xff
	if _, err := strict.handleRFCChallenge(challenge); err == nil {
		t.Fatal("strict session accepted an invalid challenge MAC")
	}

	diagnostic := NewSession(&Config{
		IMSI: "234102356143376", SIM: &recordingAKAProvider{result: result},
		DisableEAPMACValidation: true,
	})
	if payloads, err := diagnostic.handleRFCChallenge(challenge); err != nil || len(payloads) != 1 {
		t.Fatalf("explicit diagnostic mode payloads=%d err=%v", len(payloads), err)
	}
}

func testAKAResult() AKAResult {
	return AKAResult{
		RES: bytes.Repeat([]byte{0x53}, 8),
		CK:  bytes.Repeat([]byte{0x14}, 16),
		IK:  bytes.Repeat([]byte{0x25}, 16),
	}
}

func notifyTypes(payloads []ikev2.Payload) []uint16 {
	var types []uint16
	for _, payload := range payloads {
		if notification, ok := payload.(*ikev2.EncryptedPayloadNotify); ok {
			types = append(types, notification.NotifyType)
		}
	}
	return types
}

func TestMinimalIKEAuthSendsOnlyEAPOnly(t *testing.T) {
	session := NewSession(&Config{
		IMSI: "520030393351967", APN: "ims", DeviceIdentityIMEI: "358983361433761", IKEAuthMinimal: true,
	})
	payloads, err := session.buildIKEAuthInitPayloads()
	if err != nil {
		t.Fatalf("buildIKEAuthInitPayloads: %v", err)
	}
	if got := notifyTypes(payloads); len(got) != 1 || got[0] != ikev2.EAP_ONLY_AUTHENTICATION {
		t.Fatalf("minimal IKE_AUTH notifies = %v, want only EAP_ONLY_AUTHENTICATION", got)
	}
}

func TestMinimalIKEAuthAnswersRequestedDeviceIdentity(t *testing.T) {
	session := NewSession(&Config{
		IMSI: "520030393351967", APN: "ims", DeviceIdentityIMEI: "358983361433761", IKEAuthMinimal: true,
	})
	request := []ikev2.Payload{&ikev2.EncryptedPayloadNotify{NotifyType: ikev2.DEVICE_IDENTITY_3GPP, NotifyData: []byte{0, 1, 1}}}
	if !ikeAuthRequestsDeviceIdentity(request) {
		t.Fatal("DEVICE_IDENTITY request not detected")
	}
	session.deviceIdentityAsked = true
	eap := []ikev2.Payload{&ikev2.EncryptedPayloadEAP{}}
	response, err := session.withRequestedDeviceIdentity(eap)
	if err != nil {
		t.Fatal(err)
	}
	if got := notifyTypes(response); len(got) != 1 || got[0] != ikev2.DEVICE_IDENTITY_3GPP {
		t.Fatalf("response notifies = %v, want one DEVICE_IDENTITY_3GPP", got)
	}
	if again, _ := session.withRequestedDeviceIdentity(eap); len(notifyTypes(again)) != 0 {
		t.Fatal("DEVICE_IDENTITY sent twice for one request")
	}
}

func TestIKEIdentityModeSelectsAKAPrimeNAI(t *testing.T) {
	for mode, want := range map[string]string{
		"":          "0520030393351967@nai.epc.mnc003.mcc520.3gppnetwork.org",
		"epc_nai":   "0520030393351967@nai.epc.mnc003.mcc520.3gppnetwork.org",
		"aka_prime": "6520030393351967@nai.epc.mnc003.mcc520.3gppnetwork.org",
	} {
		got := buildNAI("520030393351967", &Config{MCC: "520", MNC: "03", IKEIdentityMode: mode})
		if got != want {
			t.Errorf("mode %q NAI = %q, want %q", mode, got, want)
		}
	}
}

func TestMinimalIKEAuthCanSendConfiguredDeviceIdentity(t *testing.T) {
	session := NewSession(&Config{
		IMSI: "520030393351967", APN: "IMS", DeviceIdentityIMEI: "358983361433761",
		IKEAuthMinimal: true, EnableDeviceIdentitySpoof: true,
	})
	payloads, err := session.buildIKEAuthInitPayloads()
	if err != nil {
		t.Fatalf("buildIKEAuthInitPayloads: %v", err)
	}
	got := notifyTypes(payloads)
	if len(got) != 2 || got[0] != ikev2.EAP_ONLY_AUTHENTICATION || got[1] != ikev2.DEVICE_IDENTITY_3GPP {
		t.Fatalf("notifies = %v, want EAP_ONLY_AUTHENTICATION then DEVICE_IDENTITY_3GPP", got)
	}

	// No configured IMEI: never fall back to a generated one.
	session = NewSession(&Config{IMSI: "520030393351967", APN: "IMS", IKEAuthMinimal: true, EnableDeviceIdentitySpoof: true})
	payloads, err = session.buildIKEAuthInitPayloads()
	if err != nil {
		t.Fatalf("buildIKEAuthInitPayloads: %v", err)
	}
	if got := notifyTypes(payloads); len(got) != 1 {
		t.Fatalf("notifies without IMEI = %v, want only EAP_ONLY_AUTHENTICATION", got)
	}
}
