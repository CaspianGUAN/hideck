package swu

import (
	"testing"

	"github.com/iniwex5/vowifi-go/engine/ikev2"
)

func TestOmitIKEFragmentNotifiesDropsSAInitNotifies(t *testing.T) {
	omitted := NewSession(&Config{
		IKEProposals:            []string{"aes256-sha256-prfsha256-modp2048"},
		OmitIKEFragmentNotifies: true,
	})
	packet, err := omitted.buildIKESAInitPacketObject()
	if err != nil {
		t.Fatal(err)
	}
	if saInitHasNotify(packet, notifyFragmentation) || saInitHasNotify(packet, ikev2.NON_FIRST_FRAGMENTS_ALSO) {
		t.Fatal("fragment notifies present when omitted")
	}

	kept := NewSession(&Config{IKEProposals: []string{"aes256-sha256-prfsha256-modp2048"}})
	packet, err = kept.buildIKESAInitPacketObject()
	if err != nil {
		t.Fatal(err)
	}
	if !saInitHasNotify(packet, notifyFragmentation) || !saInitHasNotify(packet, ikev2.NON_FIRST_FRAGMENTS_ALSO) {
		t.Fatal("fragment notifies missing from the default SA_INIT")
	}
}

func saInitHasNotify(packet *ikev2.IKEPacket, notifyType uint16) bool {
	for _, payload := range packet.Payloads {
		notify, ok := payload.(*ikev2.EncryptedPayloadNotify)
		if ok && notify.NotifyType == notifyType {
			return true
		}
	}
	return false
}
