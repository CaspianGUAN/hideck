package phone

import (
	"strings"
	"testing"
)

func TestParseRTPEndpointSelectsNegotiatedG711(t *testing.T) {
	tests := []struct {
		name, mapping, codec string
		payload              uint8
	}{
		{name: "static PCMU", mapping: "m=audio 41000 RTP/AVP 0\r\n", codec: "PCMU", payload: 0},
		{name: "dynamic PCMA", mapping: "m=audio 41000 RTP/AVP 112\r\na=rtpmap:112 PCMA/8000\r\n", codec: "PCMA", payload: 112},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			endpoint, err := parseRTPEndpoint("v=0\r\nc=IN IP4 127.0.0.1\r\n" + test.mapping)
			if err != nil {
				t.Fatal(err)
			}
			if endpoint.Codec != test.codec || endpoint.PayloadType != test.payload || endpoint.Address.Port != 41000 {
				t.Fatalf("endpoint = %+v", endpoint)
			}
		})
	}
}

func TestParseRTPEndpointAcceptsBandwidthEfficientAMRAndEVS(t *testing.T) {
	amr, err := parseRTPEndpoint("v=0\r\nc=IN IP4 127.0.0.1\r\nm=audio 41000 RTP/AVP 102\r\na=rtpmap:102 AMR/8000\r\na=fmtp:102 mode-change-capability=2;max-red=0\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if amr.Codec != "AMR" || amr.Fmtp != "mode-change-capability=2;max-red=0" {
		t.Fatalf("BE AMR endpoint = %+v", amr)
	}
	evs, err := parseRTPEndpoint("v=0\r\nc=IN IP4 127.0.0.1\r\nm=audio 41000 RTP/AVP 106\r\na=rtpmap:106 EVS/16000\r\na=fmtp:106 br=5.9-24.4;bw=nb-wb\r\n", "EVS")
	if err != nil {
		t.Fatal(err)
	}
	if evs.Codec != "EVS" || evs.ClockRate != 16000 {
		t.Fatalf("EVS endpoint = %+v", evs)
	}
}

func TestParseRTPEndpointPreservesAMRParameters(t *testing.T) {
	endpoint, err := parseRTPEndpoint("v=0\r\nc=IN IP4 127.0.0.1\r\nm=audio 41000 RTP/AVP 104\r\na=rtpmap:104 AMR-WB/16000\r\na=fmtp:104 octet-align=1; mode-set=0,2\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Codec != "AMR-WB" || endpoint.ClockRate != 16000 || endpoint.Fmtp != "octet-align=1; mode-set=0,2" {
		t.Fatalf("endpoint = %+v", endpoint)
	}
}

func TestParseRTPEndpointUsesG711WhenAMREncoderIsUnavailable(t *testing.T) {
	raw := "v=0\r\nc=IN IP4 127.0.0.1\r\nm=audio 41000 RTP/AVP 104 0\r\na=rtpmap:104 AMR-WB/16000\r\na=fmtp:104 octet-align=1\r\n"
	endpoint, err := parseRTPEndpoint(raw, "PCMU", "PCMA")
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Codec != "PCMU" {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	_, err = parseRTPEndpoint(strings.Replace(raw, "104 0", "104", 1), "PCMU", "PCMA")
	if err == nil || !strings.Contains(err.Error(), "AMR-WB") || !strings.Contains(err.Error(), "unavailable encoder") {
		t.Fatalf("AMR-only error = %v", err)
	}
}

func TestPlainAudioSDPAdvertisesOnlySupportedIMSCodecsAndDTMF(t *testing.T) {
	sdp := plainAudioSDP(42000, nil)
	for _, expected := range []string{"m=audio 42000 RTP/AVP 0 8 101", "PCMU/8000", "PCMA/8000", "telephone-event/8000", "a=ptime:20"} {
		if !strings.Contains(sdp, expected) {
			t.Fatalf("SDP missing %q: %s", expected, sdp)
		}
	}
	withAMR := plainAudioSDP(42000, []string{"AMR-WB", "AMR"})
	for _, expected := range []string{"RTP/AVP 104 110 102 114 0 8 101", "AMR-WB/16000", "AMR/8000", "octet-align=1", "mode-change-capability=2"} {
		if !strings.Contains(withAMR, expected) {
			t.Fatalf("AMR SDP missing %q: %s", expected, withAMR)
		}
	}
	withEVS := plainAudioSDP(42000, []string{"EVS"})
	for _, expected := range []string{"RTP/AVP 106 0 8 101", "EVS/16000", "evs-mode-switch=1", "br=6.6-23.85"} {
		if !strings.Contains(withEVS, expected) {
			t.Fatalf("EVS SDP missing %q: %s", expected, withEVS)
		}
	}
	answer := plainSelectedAudioSDP(42000, rtpEndpoint{
		Codec: "PCMU", PayloadType: 0, ClockRate: 8000,
	})
	if !strings.Contains(answer, "RTP/AVP 0 101") || strings.Contains(answer, "PCMA") {
		t.Fatalf("selected answer SDP = %s", answer)
	}
}

// With EVS disabled, an IMS offer that prefers EVS must fall back to AMR-WB
// rather than negotiating EVS, whose uplink we can only send in AMR-WB IO mode.
func TestParseRTPEndpointSkipsDisabledEVSForAMRWB(t *testing.T) {
	offer := "v=0\r\nc=IN IP4 127.0.0.1\r\nm=audio 40000 RTP/AVP 115 116 0\r\n" +
		"a=rtpmap:115 EVS/16000\r\na=rtpmap:116 AMR-WB/16000\r\na=fmtp:116 octet-align=1\r\na=rtpmap:0 PCMU/8000\r\n"
	endpoint, err := parseRTPEndpoint(offer, "PCMU", "PCMA", "AMR-WB", "AMR")
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Codec != "AMR-WB" || endpoint.PayloadType != 116 || endpoint.Fmtp != "octet-align=1" {
		t.Fatalf("endpoint = %+v, want AMR-WB PT 116", endpoint)
	}
}

func TestSelectedAnswerDTMFAvoidsOfferedAudioPayloadType(t *testing.T) {
	// AIS MT offer: AMR-WB is 101; telephone-event/16000 is 96, /8000 is 97.
	offer := "v=0\r\nc=IN IP6 2405:9800:9700:2080::5\r\nm=audio 21074 RTP/AVP 101 108 8 0 96 97\r\n" +
		"a=rtpmap:101 AMR-WB/16000\r\na=fmtp:101 mode-set=0,1,2\r\na=rtpmap:108 AMR/8000\r\n" +
		"a=rtpmap:8 PCMA/8000\r\na=rtpmap:0 PCMU/8000\r\n" +
		"a=rtpmap:96 telephone-event/16000\r\na=rtpmap:97 telephone-event/8000\r\n"
	endpoint, err := parseRTPEndpoint(offer, "AMR-WB")
	if err != nil {
		t.Fatal(err)
	}
	answer := plainSelectedAudioSDP(4000, endpoint)
	if !strings.Contains(answer, "m=audio 4000 RTP/AVP 101 96\r\n") ||
		!strings.Contains(answer, "a=rtpmap:96 telephone-event/16000") {
		t.Fatalf("answer = %q", answer)
	}

	noDTMF := strings.ReplaceAll(strings.ReplaceAll(offer, " 96 97", ""),
		"a=rtpmap:96 telephone-event/16000\r\na=rtpmap:97 telephone-event/8000\r\n", "")
	endpoint, err = parseRTPEndpoint(noDTMF, "AMR-WB")
	if err != nil {
		t.Fatal(err)
	}
	answer = plainSelectedAudioSDP(4000, endpoint)
	if strings.Contains(answer, "a=rtpmap:101 telephone-event") || !strings.Contains(answer, "RTP/AVP 101 96\r\n") {
		t.Fatalf("answer without offered DTMF = %q", answer)
	}
}

func TestSelectedAnswerDTMFUses8000ForG711(t *testing.T) {
	offer := "v=0\r\nc=IN IP4 127.0.0.1\r\nm=audio 4000 RTP/AVP 8 116 96\r\n" +
		"a=rtpmap:8 PCMA/8000\r\na=rtpmap:116 telephone-event/16000\r\na=rtpmap:96 telephone-event/8000\r\n"
	endpoint, err := parseRTPEndpoint(offer)
	if err != nil {
		t.Fatal(err)
	}
	answer := plainSelectedAudioSDP(4000, endpoint)
	if !strings.Contains(answer, "RTP/AVP 8 96\r\n") || !strings.Contains(answer, "a=rtpmap:96 telephone-event/8000") {
		t.Fatalf("answer = %q", answer)
	}
}
