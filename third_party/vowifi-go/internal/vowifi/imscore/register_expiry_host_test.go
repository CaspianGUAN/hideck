package imscore

import (
	"strings"
	"testing"
	"time"
)

func TestSameRegisterContactHostMatchesEquivalentIPv6(t *testing.T) {
	if !sameRegisterContactHost("[2405:9800:9700:238f::1]", "[2405:9800:9700:238F:0:0:0:1]") {
		t.Fatal("equivalent IPv6 Contact hosts did not match")
	}
	if sameRegisterContactHost("[2405:9800:9700:238f::1]", "[2405:9800:9700:238f::2]") {
		t.Fatal("different IPv6 Contact hosts matched")
	}
	if !sameRegisterContactHost("Example.COM", "example.com") {
		t.Fatal("host names must compare case-insensitively")
	}
}

// The AIS 520/03 registrar answered an iPhone REGISTER with this 200 OK; it
// echoes the Contact with the feature tags and adds expires.
func TestRegistrationExpiresMatchesAISContactEcho(t *testing.T) {
	contact := `<sip:[2001:44c8:4d71:58ad:1b20:91ee:92aa:4488]:50544>;+g.3gpp.icsi-ref="urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel";+g.3gpp.mid-call;+g.3gpp.ps2cs-srvcc-orig-pre-alerting;+g.3gpp.smsip;+g.3gpp.srvcc-alerting;+sip.instance="<urn:gsma:imei:35671411-769797-0>"`
	raw := strings.Join([]string{
		"SIP/2.0 200 OK",
		"Via: SIP/2.0/TCP [2001:44c8:4d71:58ad:1b20:91ee:92aa:4488]:50544;branch=z9hG4bKJCTQ2IRtTIWKTeP;rport=63040",
		"Call-ID: 5QCBExXAVCKj3Laq0YOdHicX",
		"From: <sip:520030393351967@ims.mnc003.mcc520.3gppnetwork.org>;tag=PHdPREV0HZ",
		"To: <sip:520030393351967@ims.mnc003.mcc520.3gppnetwork.org>;tag=FMpjaLM4",
		"CSeq: 2 REGISTER",
		"Service-Route: <sip:orig@scscftwa1h.ims.mnc003.mcc520.3gppnetwork.org;Dpt=7f24_135ec246;ca=325;lr>",
		"Contact: " + contact + ";expires=7200",
		"Path: <sip:[2405:9800:9700:2083::2]:9900;lr>",
		"Content-Length: 0",
		"", "",
	}, "\r\n")
	response, err := parseSIPResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := registrationExpires(response, contact, time.Hour)
	if err != nil || got != 7200*time.Second {
		t.Fatalf("expires=%v err=%v, want 2h from the echoed AIS Contact", got, err)
	}
}
