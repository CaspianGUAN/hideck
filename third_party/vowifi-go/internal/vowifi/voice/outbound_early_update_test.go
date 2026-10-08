package voice

import (
	"strings"
	"testing"
)

// SDPs from an iPhone MO call on AIS (2026-10-08): the 183 negotiates AMR-WB
// 104, then the network's early UPDATE moves AMR-WB to 111.
const (
	aisEarly183SDP = "v=0\r\no=- 380685538 380685538 IN IP6 2405:9800:9700:238F::E\r\ns=SBC call\r\n" +
		"c=IN IP6 2405:9800:9700:238F::E\r\nt=0 0\r\nm=audio 27168 RTP/AVP 104 105\r\n" +
		"a=rtpmap:104 AMR-WB/16000\r\na=fmtp:104 mode-set=0,1,2\r\na=rtpmap:105 telephone-event/16000\r\n" +
		"a=fmtp:105 0-15\r\na=ptime:20\r\n"
	aisEarlyUpdateOffer = "v=0\r\no=- 380685538 380685539 IN IP6 2405:9800:9700:238F::E\r\ns=SBC call\r\n" +
		"c=IN IP6 2405:9800:9700:238F::E\r\nt=0 0\r\nm=audio 27168 RTP/AVP 111 8 0 108 102 116 100 18 103 114 106\r\n" +
		"a=rtpmap:111 AMR-WB/16000\r\na=rtpmap:8 PCMA/8000\r\na=rtpmap:0 PCMU/8000\r\na=rtpmap:108 AMR/8000\r\n" +
		"a=rtpmap:102 AMR/8000\r\na=rtpmap:116 telephone-event/8000\r\na=rtpmap:100 AMR/8000\r\n" +
		"a=rtpmap:18 G729/8000\r\na=rtpmap:103 telephone-event/16000\r\na=fmtp:103 0-15\r\n" +
		"a=rtpmap:114 EVS/16000\r\na=rtpmap:106 EVS/16000\r\na=ptime:20\r\na=3gOoBTC\r\n"
	hideckMOOffer = "v=0\r\no=- 1 1 IN IP6 2001:44c8::5\r\ns=-\r\nc=IN IP6 2001:44c8::5\r\nt=0 0\r\n" +
		"m=audio 40000 RTP/AVP 104 110 102 114 8 0 101\r\nb=AS:88\r\na=rtpmap:104 AMR-WB/16000\r\n" +
		"a=rtpmap:110 AMR-WB/16000\r\na=fmtp:110 octet-align=1\r\na=rtpmap:102 AMR/8000\r\n" +
		"a=rtpmap:114 AMR/8000\r\na=fmtp:114 octet-align=1\r\na=rtpmap:8 PCMA/8000\r\na=rtpmap:0 PCMU/8000\r\n" +
		"a=rtpmap:101 telephone-event/8000\r\na=fmtp:101 0-15\r\na=ptime:20\r\na=sendrecv\r\n"
)

func TestEarlyUpdateAnswerUsesOfferPayloadTypes(t *testing.T) {
	answer, err := earlyUpdateAnswer(aisEarlyUpdateOffer, aisEarly183SDP, hideckMOOffer)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"c=IN IP6 2001:44c8::5\r\n", "m=audio 40000 RTP/AVP 111 103\r\n", "a=rtpmap:111 AMR-WB/16000\r\n",
		"a=rtpmap:103 telephone-event/16000\r\n", "a=maxptime:240\r\n", "a=sendrecv\r\n",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("answer missing %q:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "b=") || strings.Contains(answer, " 104") {
		t.Fatalf("answer = %s", answer)
	}
}

func TestEarlyUpdateAnswerRejectsOfferWithoutCurrentCodec(t *testing.T) {
	offer := strings.Replace(aisEarlyUpdateOffer, "a=rtpmap:111 AMR-WB/16000\r\n", "a=rtpmap:111 AMR/8000\r\n", 1)
	if _, err := earlyUpdateAnswer(offer, aisEarly183SDP, hideckMOOffer); err == nil {
		t.Fatal("expected an error without AMR-WB in the offer")
	}
}
