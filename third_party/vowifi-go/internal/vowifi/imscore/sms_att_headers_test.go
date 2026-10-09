package imscore

import (
	"net"
	"strings"
	"testing"
)

func registeredSMSServiceForPreset(t *testing.T, presetID string) *Service {
	t.Helper()
	service, err := New(&IMSConfig{
		DeviceID: "wwan0", IMSI: "310280229262005", IMPI: "310280229262005@one.att.net",
		IMPU: "sip:310280229262005@one.att.net", Domain: "one.att.net", SMSC: "+13123149810",
		LocalIP: net.IPv4(10, 0, 0, 2), LocalPort: 5060, Transport: "tcp", CarrierPresetID: presetID,
	})
	if err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	service.regState = regRegistered
	service.externalTransport = true
	service.regSession = &registerSession{
		contactUser: "registered-contact", cseq: 3, publicID: "tel:+14807471509",
		security: &securityAgreement{verifyHeader: "ipsec-3gpp;alg=hmac-sha-1-96"},
	}
	service.mu.Unlock()
	t.Cleanup(service.StopCurrent)
	return service
}

func TestATTSMSMessageUsesHandsetHeaders(t *testing.T) {
	request, err := registeredSMSServiceForPreset(t, "att_310280").buildSMSMESSAGE("tel:+13123149810", []byte{0x00, 0x01})
	if err != nil {
		t.Fatal(err)
	}
	if got := rawSIPHeaderValue(request, "Accept-Contact"); got != "*;+g.3gpp.smsip" {
		t.Fatalf("Accept-Contact = %q", got)
	}
	if got := rawSIPHeaderValue(request, "Request-Disposition"); got != "no-fork" {
		t.Fatalf("Request-Disposition = %q", got)
	}
	for _, absent := range []string{"P-Preferred-Service:", "Content-Transfer-Encoding:"} {
		if strings.Contains(request, absent) {
			t.Fatalf("AT&T MESSAGE still has %s\n%s", absent, request)
		}
	}
	if got := rawSIPHeaderValue(request, "P-Preferred-Identity"); got != "<tel:+14807471509>" {
		t.Fatalf("P-Preferred-Identity = %q", got)
	}
}

func TestNonATTSMSMessageKeepsICSIHeaders(t *testing.T) {
	request, err := registeredSMSServiceForPreset(t, "T-Mobile_240").buildSMSMESSAGE("tel:+13123149810", []byte{0x00, 0x01})
	if err != nil {
		t.Fatal(err)
	}
	if got := rawSIPHeaderValue(request, "P-Preferred-Service"); got != imsSMSPreferredService {
		t.Fatalf("P-Preferred-Service = %q", got)
	}
	if got := rawSIPHeaderValue(request, "Accept-Contact"); got != imsSMSAcceptContact {
		t.Fatalf("Accept-Contact = %q", got)
	}
}
