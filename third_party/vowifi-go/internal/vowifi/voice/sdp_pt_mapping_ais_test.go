package voice

import "testing"

func TestCodecPTMappingKeepsAnsweredAMRWBPayloadType(t *testing.T) {
	// AIS MT offer lists AMR-WB as 101 and 112; the answer keeps 101.
	offer, err := ParseSDP([]byte("v=0\r\nc=IN IP6 2405:9800::1\r\nm=audio 4000 RTP/AVP 101 108 102 8 0 116 96 112 100\r\n" +
		"a=rtpmap:101 AMR-WB/16000\r\na=fmtp:101 mode-set=0,1,2\r\na=rtpmap:108 AMR/8000\r\na=rtpmap:102 AMR/8000\r\n" +
		"a=rtpmap:8 PCMA/8000\r\na=rtpmap:0 PCMU/8000\r\na=rtpmap:116 telephone-event/16000\r\na=rtpmap:96 telephone-event/8000\r\n" +
		"a=rtpmap:112 AMR-WB/16000\r\na=rtpmap:100 AMR/8000\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	answer, err := ParseSDP([]byte("v=0\r\nc=IN IP4 127.0.0.1\r\nm=audio 5000 RTP/AVP 101 116\r\n" +
		"a=rtpmap:101 AMR-WB/16000\r\na=fmtp:101 mode-set=0,1,2\r\na=rtpmap:116 telephone-event/16000\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	mapping := codecPTMapping(offer, answer)
	for ims, client := range mapping {
		if client == 101 || client == 116 {
			t.Fatalf("IMS payload type %d maps onto answered client payload type %d: %v", ims, client, mapping)
		}
	}
}
