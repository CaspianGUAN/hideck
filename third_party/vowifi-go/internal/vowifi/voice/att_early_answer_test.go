package voice

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
)

// The AT&T MT offer an iPhone answered on 2026-10-09 (Red Pocket).
const attMTOffer = "v=0\r\n" +
	"o=LucentPCSF 1413485701 1413485701 IN IP6 imsgroup0-003.so0ca14pcf.so1ca.uvp.els-an.att.net\r\n" +
	"s=-\r\n" +
	"c=IN IP6 2001:1890:1f8:21f8::1:1\r\n" +
	"t=0 0\r\n" +
	"m=audio 37130 RTP/AVP 104 110 102 114 0 96 106 101\r\n" +
	"a=rtpmap:104 AMR-WB/16000\r\n" +
	"a=fmtp:104 mode-set=0,1,2; max-red=0; mode-change-capability=2\r\n" +
	"a=rtpmap:110 AMR-WB/16000\r\n" +
	"a=fmtp:110 octet-align=1; mode-set=0,1,2; max-red=0; mode-change-capability=2\r\n" +
	"a=rtpmap:102 AMR/8000\r\n" +
	"a=fmtp:102 max-red=0; mode-change-capability=2\r\n" +
	"a=rtpmap:114 AMR/8000\r\n" +
	"a=fmtp:114 octet-align=1; max-red=0; mode-change-capability=2\r\n" +
	"a=rtpmap:96 AMR-WB/16000\r\n" +
	"a=fmtp:96 mode-set=0,1,2; max-red=0\r\n" +
	"a=rtpmap:106 telephone-event/16000\r\n" +
	"a=rtpmap:101 telephone-event/8000\r\n" +
	"a=sendrecv\r\n" +
	"a=ptime:20\r\n" +
	"a=maxptime:40\r\n"

func TestATTEarlyFormatsMatchIPhoneAnswer(t *testing.T) {
	if got := fmt.Sprint(attEarlyFormats(attMTOffer)); got != "[104 106]" {
		t.Fatalf("formats = %s, want the iPhone's [104 106]", got)
	}
	g711 := "v=0\r\nc=IN IP4 192.0.2.1\r\nm=audio 4000 RTP/AVP 0 8 101\r\n" +
		"a=rtpmap:0 PCMU/8000\r\na=rtpmap:8 PCMA/8000\r\na=rtpmap:101 telephone-event/8000\r\n"
	if got := fmt.Sprint(attEarlyFormats(g711)); got != "[0 101]" {
		t.Fatalf("G.711 formats = %s", got)
	}
}

func TestRestrictSDPFormatsKeepsOnlyChosenPayloadTypes(t *testing.T) {
	got := restrictSDPFormats(attMTOffer, []int{104, 106})
	if !strings.Contains(got, "m=audio 37130 RTP/AVP 104 106\r\n") {
		t.Fatalf("m= line not restricted:\n%s", got)
	}
	for _, gone := range []string{"a=rtpmap:110", "a=fmtp:110", "a=rtpmap:96", "a=rtpmap:101", "a=fmtp:102"} {
		if strings.Contains(got, gone) {
			t.Fatalf("%s kept:\n%s", gone, got)
		}
	}
	for _, kept := range []string{"a=fmtp:104 mode-set=0,1,2", "a=rtpmap:106 telephone-event/16000", "a=maxptime:40", "c=IN IP6"} {
		if !strings.Contains(got, kept) {
			t.Fatalf("%s dropped:\n%s", kept, got)
		}
	}
}

func TestATTEarlySDPMatchesIPhoneShape(t *testing.T) {
	sdp := attEarlySDP(attMTOffer, []int{104, 106}, "2600:380:84d1:5125::1", 49120, "+14807471509",
		time.Unix(1791522114, 0))
	for _, want := range []string{
		"o=tel:+14807471509 1791522114 0 IN IP6 2600:380:84d1:5125::1\r\n",
		"c=IN IP6 2600:380:84d1:5125::1\r\n",
		"m=audio 49120 RTP/AVP 104 106\r\n",
		"b=AS:",
		"a=rtpmap:104 AMR-WB/16000\r\n",
		"a=fmtp:104 mode-set=0,1,2; max-red=0; mode-change-capability=2\r\n",
		"a=rtpmap:106 telephone-event/16000\r\n",
		"a=ptime:20\r\na=maxptime:40\r\na=sendrecv\r\n",
	} {
		if !strings.Contains(sdp, want) {
			t.Fatalf("early SDP missing %q:\n%s", want, sdp)
		}
	}
	info, err := ParseSDP([]byte(sdp))
	if err != nil || info.MediaPort != 49120 || len(info.Codecs) != 2 {
		t.Fatalf("early SDP does not parse: %+v err=%v", info, err)
	}
}

func TestATTReliable183CarriesSDPAndLocationAsMultipart(t *testing.T) {
	request := sip.NewRequest(sip.INVITE, sip.Uri{Scheme: "sip", Host: "2600:380::1"})
	request.AppendHeader(sip.NewHeader("To", "<sip:+14807471509@one.att.net;user=phone>"))
	response := sip.NewResponseFromRequest(request, 183, "Session Progress", nil)
	response.AppendHeader(sip.NewHeader("Content-Type", "application/pidf+xml"))
	response.SetBody([]byte(civicPIDF("sip:+14807471509@one.att.net", "US", time.Now())))
	attachSDPWithLocation(response, "v=0\r\nm=audio 49120 RTP/AVP 104 106\r\n", request)

	contentType := response.GetHeader("Content-Type").Value()
	if contentType != "multipart/mixed;boundary="+attMultipartBoundary {
		t.Fatalf("Content-Type = %q", contentType)
	}
	body := string(response.Body())
	sdpAt := strings.Index(body, "Content-Type: application/sdp\r\n\r\nv=0\r\n")
	pidfAt := strings.Index(body, "Content-ID: <+14807471509@one.att.net>\r\n"+
		"Content-Disposition: render; handling=optional\r\nContent-Type: application/pidf+xml\r\n\r\n<?xml")
	if sdpAt < 0 || pidfAt < sdpAt || !strings.HasSuffix(body, "--"+attMultipartBoundary+"--\r\n") {
		t.Fatalf("multipart body:\n%s", body)
	}
}
