package imscore

import (
	"net"
	"strconv"
	"strings"
	"testing"
)

func locationRegisterConfig(presetID string) *IMSConfig {
	return &IMSConfig{
		CarrierPresetID: presetID, IMEI: "356714117697975", IMSI: "310280229262005",
		IMPI: "310280229262005@one.att.net", IMPU: "sip:310280229262005@one.att.net",
		Domain: "one.att.net", PAccessNetworkCountry: "US",
		LocalIP: net.IPv4(192, 0, 2, 10), LocalPort: 5060, Transport: "tcp",
	}
}

func TestATTRegisterCarriesPIDFLocation(t *testing.T) {
	service := &Service{cfg: locationRegisterConfig("att_310280")}
	request := service.buildRegister(&registerSession{callID: "call-1", fromTag: "tag-1", cseq: 1}, "")
	if got := sipHeaderValue(request, "Geolocation"); got != "<cid:310280229262005@one.att.net>" {
		t.Fatalf("Geolocation = %q", got)
	}
	if got := sipHeaderValue(request, "Geolocation-Routing"); got != "yes" {
		t.Fatalf("Geolocation-Routing = %q", got)
	}
	if got := sipHeaderValue(request, "Content-Type"); got != "application/pidf+xml" {
		t.Fatalf("Content-Type = %q", got)
	}
	head, body, found := strings.Cut(request, "\r\n\r\n")
	if !found || !strings.Contains(body, "<cl:country>US</cl:country>") ||
		!strings.Contains(body, `entity="sip:310280229262005@one.att.net"`) {
		t.Fatalf("body = %q", body)
	}
	if want := "Content-Length: " + strconv.Itoa(len(body)); !strings.Contains(head, want) {
		t.Fatalf("missing %q in\n%s", want, head)
	}
}

func TestNonATTRegisterHasNoLocationBody(t *testing.T) {
	service := &Service{cfg: locationRegisterConfig("T-Mobile_240")}
	request := service.buildRegister(&registerSession{callID: "call-1", fromTag: "tag-1", cseq: 1}, "")
	if strings.Contains(request, "Geolocation") || !strings.HasSuffix(request, "Content-Length: 0\r\n\r\n") {
		t.Fatalf("REGISTER = %q", request)
	}
}
