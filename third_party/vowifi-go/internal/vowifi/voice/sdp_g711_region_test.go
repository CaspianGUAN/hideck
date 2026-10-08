package voice

import (
	"strings"
	"testing"
)

func TestPreferRegionalG711(t *testing.T) {
	offer := "v=0\r\nc=IN IP6 ::1\r\nm=audio 4000 RTP/AVP 0 8 101\r\na=rtpmap:0 PCMU/8000\r\n" +
		"a=rtpmap:8 PCMA/8000\r\na=rtpmap:101 telephone-event/8000\r\n"
	if mcc := homeMCCFromDomain("ims.mnc003.mcc520.3gppnetwork.org"); mcc != "520" {
		t.Fatalf("mcc = %q", mcc)
	}
	thai := preferRegionalG711(offer, "520")
	if !strings.Contains(thai, "m=audio 4000 RTP/AVP 8 0 101\r\n") {
		t.Fatalf("Thai offer = %q", thai)
	}
	if us := preferRegionalG711(offer, "310"); us != offer {
		t.Fatalf("US offer changed: %q", us)
	}
	if unknown := preferRegionalG711(offer, ""); unknown != offer {
		t.Fatalf("offer without MCC changed: %q", unknown)
	}
}
