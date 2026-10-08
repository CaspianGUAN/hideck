package voice

import (
	"strings"
	"testing"
)

func TestShapeIMSSDPAddsBandwidthAndOrigin(t *testing.T) {
	answer := "v=0\r\no=hideck 0 0 IN IP6 2001:db8::1\r\ns=HiDeck Phone\r\nc=IN IP6 2001:db8::1\r\nt=0 0\r\n" +
		"m=audio 4000 RTP/AVP 101 96\r\na=rtpmap:101 AMR-WB/16000\r\na=rtpmap:96 telephone-event/16000\r\na=ptime:20\r\n"
	shaped := shapeIMSSDP(answer)
	if !strings.Contains(shaped, "m=audio 4000 RTP/AVP 101 96\r\nb=AS:49\r\n") || strings.Contains(shaped, "o=hideck 0 0") ||
		!strings.Contains(shaped, "\r\ns=-\r\n") {
		t.Fatalf("shaped = %q", shaped)
	}
	offer := "v=0\r\no=hideck 0 0 IN IP6 2001:db8::1\r\ns=HiDeck Phone\r\nc=IN IP6 2001:db8::1\r\nt=0 0\r\n" +
		"m=audio 4000 RTP/AVP 104 0 8 101\r\na=rtpmap:104 AMR-WB/16000\r\na=rtpmap:0 PCMU/8000\r\na=rtpmap:8 PCMA/8000\r\n"
	if shaped := shapeIMSSDP(offer); !strings.Contains(shaped, "b=AS:88\r\n") {
		t.Fatalf("offer shaped = %q", shaped)
	}
	withBandwidth := strings.Replace(offer, "t=0 0\r\n", "t=0 0\r\nb=AS:30\r\n", 1)
	if shaped := shapeIMSSDP(withBandwidth); strings.Count(shaped, "b=AS") != 1 {
		t.Fatalf("existing b= duplicated: %q", shaped)
	}
}
