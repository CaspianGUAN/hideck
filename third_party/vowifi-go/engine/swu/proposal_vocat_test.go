package swu

import (
	"testing"

	"github.com/iniwex5/vowifi-go/engine/ikev2"
)

func TestVocatIKEProposalKeepsModernTransformOrder(t *testing.T) {
	session := NewSession(&Config{IKEProposals: []string{"vocat"}})
	packet, err := session.buildIKESAInitPacketObject()
	if err != nil {
		t.Fatal(err)
	}
	sa, ok := packet.Payloads[0].(*ikev2.EncryptedPayloadSA)
	if !ok || len(sa.Proposals) != 1 {
		t.Fatalf("SA_INIT SA = %#v", packet.Payloads[0])
	}
	want := []struct {
		kind   ikev2.TransformType
		id     ikev2.AlgorithmType
		keyLen uint16
	}{
		{ikev2.TransformTypeEncr, ikev2.ENCR_AES_CBC, 128},
		{ikev2.TransformTypeEncr, ikev2.ENCR_AES_CBC, 256},
		{ikev2.TransformTypePRF, ikev2.PRF_HMAC_SHA2_256, 0},
		{ikev2.TransformTypePRF, ikev2.PRF_HMAC_SHA1, 0},
		{ikev2.TransformTypeInteg, ikev2.AUTH_HMAC_SHA2_256_128, 0},
		{ikev2.TransformTypeInteg, ikev2.AUTH_HMAC_SHA1_96, 0},
		{ikev2.TransformTypeDH, ikev2.MODP_2048_bit, 0},
	}
	transforms := sa.Proposals[0].Transforms
	if len(transforms) != len(want) {
		t.Fatalf("IKE transforms = %d, want %d", len(transforms), len(want))
	}
	for index, transform := range transforms {
		if transform.Type != want[index].kind || transform.ID != want[index].id || encryptionKeyBits(transform) != want[index].keyLen {
			t.Fatalf("IKE transform[%d] = type %d id %d bits %d", index, transform.Type, transform.ID, encryptionKeyBits(transform))
		}
	}
	if session.dhGroup != uint16(ikev2.MODP_2048_bit) {
		t.Fatalf("KE group = %d, want MODP2048", session.dhGroup)
	}
	types := saInitNotifyTypes(packet.Payloads)
	if len(types) != 1 || types[0] != notifyFragmentation {
		t.Fatalf("SA_INIT notifies = %v, want only %d", types, notifyFragmentation)
	}
}

func TestVocatSAInitKeepsFragmentationWhenOmitIsSet(t *testing.T) {
	session := NewSession(&Config{
		IKEProposals:            []string{"vocat"},
		OmitIKEFragmentNotifies: true,
	})
	packet, err := session.buildIKESAInitPacketObject()
	if err != nil {
		t.Fatal(err)
	}
	if !saInitHasNotify(packet, notifyFragmentation) || saInitHasNotify(packet, ikev2.NON_FIRST_FRAGMENTS_ALSO) {
		t.Fatal("vocat SA_INIT must send fragmentation and omit NON_FIRST_FRAGMENTS_ALSO")
	}
}

func TestVocatESPProposalKeepsModernTransformOrder(t *testing.T) {
	proposals, err := buildESPProposals(&Config{ESPProposals: []string{"vocat"}}, []byte{1, 2, 3, 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 1 {
		t.Fatalf("ESP proposals = %d", len(proposals))
	}
	want := []struct {
		kind   ikev2.TransformType
		id     ikev2.AlgorithmType
		keyLen uint16
	}{
		{ikev2.TransformTypeEncr, ikev2.ENCR_AES_CBC, 128},
		{ikev2.TransformTypeEncr, ikev2.ENCR_AES_CBC, 256},
		{ikev2.TransformTypeInteg, ikev2.AUTH_HMAC_SHA2_256_128, 0},
		{ikev2.TransformTypeInteg, ikev2.AUTH_HMAC_SHA1_96, 0},
		{ikev2.TransformTypeESN, 0, 0},
	}
	if len(proposals[0].Transforms) != len(want) {
		t.Fatalf("ESP transforms = %d, want %d", len(proposals[0].Transforms), len(want))
	}
	for index, transform := range proposals[0].Transforms {
		if transform.Type != want[index].kind || transform.ID != want[index].id || encryptionKeyBits(transform) != want[index].keyLen {
			t.Fatalf("ESP transform[%d] = type %d id %d bits %d", index, transform.Type, transform.ID, encryptionKeyBits(transform))
		}
	}
	if proposalDHGroup(proposals[0]) != 0 {
		t.Fatal("vocat ESP proposal includes a DH group")
	}
}

func TestVocatIKEAuthMatchesWholeHandshake(t *testing.T) {
	session := NewSession(&Config{
		IMSI: "520030393351967", APN: "ims", MCC: "520", MNC: "03",
		IKEAuthMinimal: true, IPStackType: "ipv4v6",
		IKEProposals:              []string{"vocat"},
		ESPProposals:              []string{"vocat"},
		CPRequestAttributes:       []uint16{1, 8, 3, 10, 20, 21, 7},
		EnableDeviceIdentitySpoof: true,
		DeviceIdentityIMEI:        "356714117697975",
	})
	payloads, err := session.buildIKEAuthInitPayloads()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"IDi", "IDr", "EAP_ONLY_AUTHENTICATION", "MOBIKE_SUPPORTED", "INITIAL_CONTACT",
		"SA", "TSi", "TSr", "CP",
	}
	if got := ikeAuthPayloadNames(payloads); !sameStrings(got, want) {
		t.Fatalf("payload order = %v, want %v", got, want)
	}
	for _, payload := range payloads {
		notify, ok := payload.(*ikev2.EncryptedPayloadNotify)
		if ok && notify.NotifyType == ikev2.DEVICE_IDENTITY_3GPP {
			t.Fatal("vocat first IKE_AUTH included DEVICE_IDENTITY")
		}
		if ok && notify.NotifyType == ikev2.EAP_ONLY_AUTHENTICATION && notify.ProtocolID != 0 {
			t.Fatalf("EAP_ONLY protocol = %d, want 0", notify.ProtocolID)
		}
	}
	var tsi *ikev2.EncryptedPayloadTS
	var cp *ikev2.EncryptedPayloadCP
	for _, payload := range payloads {
		if value, ok := payload.(*ikev2.EncryptedPayloadTS); ok && value.IsInitiator {
			tsi = value
		}
		if value, ok := payload.(*ikev2.EncryptedPayloadCP); ok {
			cp = value
		}
	}
	if tsi == nil || len(tsi.TrafficSelectors) != 2 || len(tsi.TrafficSelectors[0].StartAddr) != 4 || len(tsi.TrafficSelectors[1].StartAddr) != 16 {
		t.Fatalf("TSi = %+v, want IPv4 then IPv6", tsi)
	}
	wantCP := []uint16{1, 8, 3, 10, 20, 21, 7}
	if cp == nil || len(cp.Attributes) != len(wantCP) {
		t.Fatalf("CP = %+v", cp)
	}
	for index, attribute := range cp.Attributes {
		if attribute.Type != wantCP[index] || len(attribute.Value) != 0 {
			t.Fatalf("CP[%d] type %d value %x", index, attribute.Type, attribute.Value)
		}
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
