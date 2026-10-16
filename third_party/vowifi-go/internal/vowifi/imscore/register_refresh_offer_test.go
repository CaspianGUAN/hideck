package imscore

import (
	"strings"
	"testing"

	"github.com/iniwex5/vowifi-go/internal/vowifi/policy"
)

func TestRefreshSecurityOfferAdvertisesFreshPortCAndSPIs(t *testing.T) {
	template := policy.DefaultIMSRegisterTemplate()
	active := securityMechanism{
		Name: "ipsec-3gpp", Auth: "hmac-sha-1-96", Encryption: "aes-cbc", Protocol: "esp", Mode: "trans",
		SPIC: 111, SPIS: 222, PortC: 37294, PortS: 20171,
	}
	current := &securityAgreement{
		client: active, clientHeader: buildTemplateSecurityClient(active, template), verifyHeader: "verify",
	}
	refreshed, err := refreshSecurityOffer(current, template)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.client != active || refreshed.verifyHeader != "verify" {
		t.Fatal("refresh changed the SA it is sent over")
	}
	if current.clientHeader == refreshed.clientHeader {
		t.Fatal("refresh repeated the active Security-Client")
	}
	offers := splitSecurityMechanisms(refreshed.clientHeader)
	if len(offers) == 0 {
		t.Fatalf("no offers in %q", refreshed.clientHeader)
	}
	for _, raw := range offers {
		offer, err := parseSecurityMechanism(raw)
		if err != nil {
			t.Fatal(err)
		}
		if offer.PortS != active.PortS {
			t.Fatalf("port-s changed: %q", raw)
		}
		if offer.PortC == active.PortC || offer.SPIC == active.SPIC || offer.SPIS == active.SPIS {
			t.Fatalf("offer repeats the active SA: %q", raw)
		}
	}
	if !strings.Contains(refreshed.clientHeader, "port-s=20171") {
		t.Fatalf("header %q", refreshed.clientHeader)
	}
}

func TestOnlyAISRefreshesWithFreshOffer(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	service.cfg.CarrierPresetID = "ais_th_52003"
	if !service.refreshOffersFreshClientSA() {
		t.Fatal("AIS refresh keeps the active Security-Client")
	}
	service.cfg.CarrierPresetID = "tmobile_us_310260"
	if service.refreshOffersFreshClientSA() {
		t.Fatal("T-Mobile refresh changed")
	}
}
