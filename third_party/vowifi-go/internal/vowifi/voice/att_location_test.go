package voice

import (
	"strings"
	"testing"
	"time"
)

func TestCivicPIDFMatchesIPhoneShape(t *testing.T) {
	body := civicPIDF("sip:+14807471509@one.att.net;user=phone", "US", time.Date(2026, 10, 9, 5, 1, 54, 0, time.UTC))
	for _, want := range []string{
		`entity="sip:+14807471509@one.att.net;user=phone"`, "<tuple id=\"Wifi\">",
		"<cl:country>US</cl:country>", "<dm:timestamp>2026-10-09T05:01:54Z</dm:timestamp>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("PIDF missing %q:\n%s", want, body)
		}
	}
	if !attHomeDomain("one.att.net") || attHomeDomain("ims.mnc003.mcc520.3gppnetwork.org") {
		t.Fatal("attHomeDomain")
	}
}
