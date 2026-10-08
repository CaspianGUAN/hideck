package voice

import (
	"strings"
	"testing"

	"github.com/emiago/sipgo/sip"
)

func TestAISHandsetContactMatchesIPhone(t *testing.T) {
	request := sip.NewRequest(sip.INVITE, sip.Uri{
		Scheme: "sip", User: "0b395af5-8a47", Host: "2001:44C8:4D74:5A5E:0001:0000:DB98:872D", Port: 51083,
		UriParams: sip.NewParams(),
	})
	request.Recipient.UriParams.Add("transport", "tcp")
	contact := aisHandsetContact(request).Value()
	want := `<sip:[2001:44C8:4D74:5A5E:0001:0000:DB98:872D]:51083>;+g.3gpp.icsi-ref="urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel"` +
		`;+g.3gpp.mid-call;+g.3gpp.ps2cs-srvcc-orig-pre-alerting;+g.3gpp.srvcc-alerting`
	if contact != want {
		t.Fatalf("contact = %q\nwant    = %q", contact, want)
	}
}

func TestAISHandsetSDPDropsBandwidthAndAddsMaxPtime(t *testing.T) {
	sdp := "v=0\r\nm=audio 4000 RTP/AVP 101 116\r\nb=AS:49\r\na=rtpmap:101 AMR-WB/16000\r\na=ptime:20\r\na=sendrecv\r\n"
	got := aisHandsetSDP(sdp)
	if strings.Contains(got, "b=AS") || !strings.Contains(got, "a=ptime:20\r\na=maxptime:240\r\n") {
		t.Fatalf("sdp = %q", got)
	}
	if !aisHomeDomain("ims.mnc003.mcc520.3gppnetwork.org") || aisHomeDomain("ims.mnc240.mcc310.3gppnetwork.org") {
		t.Fatal("aisHomeDomain")
	}
}
