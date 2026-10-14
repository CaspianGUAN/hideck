package qdc507

import (
	"strings"
	"testing"
)

func TestLegacyADBKeyKnownVector(t *testing.T) {
	// Public interoperability vector from carp4/qadbkey-unlock, not a live device challenge.
	key, err := deriveADBKey("12345678")
	if err != nil || key != "0jXKXQwSwMxYoeg" {
		t.Fatal(key, err)
	}
	// Independently checked with openssl passwd -1 using this public test salt.
	key, err = deriveADBKey("87654321")
	if err != nil || key != "FRtkAuWIXtIsNpt" {
		t.Fatal(key, err)
	}
	for _, input := range []string{"", "1234", "123456789", "1234567a", "1234\n678"} {
		if _, err := deriveADBKey(input); err == nil {
			t.Fatal("accepted invalid challenge")
		}
	}
}

func TestADBChallengeParsing(t *testing.T) {
	for _, input := range []string{"12345678", "AT+QADBKEY?\r\n+QADBKEY: 12345678\r\nOK", "12345678\r\nOK"} {
		if got, err := parseADBChallenge(input); err != nil || got != "12345678" {
			t.Fatal(got, err)
		}
	}
	for _, input := range []string{"ERROR", "OK", "+QADBKEY: abcdefgh", "12345678\n87654321", "+QADBKEY: 12345678 garbage"} {
		if _, err := parseADBChallenge(input); err == nil {
			t.Fatal("accepted unknown challenge format")
		}
	}
}

func TestUSBConfigRejectsUnknownLayoutsAndPreservesOtherFlags(t *testing.T) {
	for _, input := range []string{"ERROR", usbOff + ",1", strings.TrimSuffix(usbOff, ",1"),
		strings.Replace(usbOff, ",0,1", ",2,1", 1), strings.Replace(usbOff, "0x125", "bad", 1), usbOff + "\n" + usbOff} {
		if _, err := parseUSBConfig(input); err == nil {
			t.Fatal("accepted unknown USB layout", input)
		}
	}
	config, err := parseUSBConfig(`+QCFG: "usbcfg",0x2C7C,0x125,0,1,1,0,1,0,0`)
	if err != nil {
		t.Fatal(err)
	}
	config.flags[adbUSBFlag] = 1
	if config.command() != `AT+QCFG="usbcfg",0x2C7C,0x125,0,1,1,0,1,1,0` {
		t.Fatal(config.command())
	}
}
