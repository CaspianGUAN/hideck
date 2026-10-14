package smscodec

import (
	"testing"

	"github.com/warthog618/sms/encoding/tpdu"
	"github.com/warthog618/sms/encoding/ucs2"
)

func TestBuildSubmitTPDUsUsesInternationalTypeOnlyForExplicitPlus(t *testing.T) {
	tests := []struct {
		name string
		to   string
		ton  tpdu.TypeOfNumber
	}{
		{name: "china national", to: "13800138000", ton: tpdu.TonUnknown},
		{name: "china international", to: "+8613800138000", ton: tpdu.TonInternational},
		{name: "britain national", to: "07911123456", ton: tpdu.TonUnknown},
		{name: "service code", to: "10086", ton: tpdu.TonUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, _, err := BuildSubmitTPDUs(test.to, "hello")
			if err != nil {
				t.Fatal(err)
			}
			part := tpdu.TPDU{Direction: tpdu.MO}
			if err := part.UnmarshalBinary(encoded[0]); err != nil {
				t.Fatal(err)
			}
			if part.DA.TypeOfNumber() != test.ton || part.DA.Number() != test.to {
				t.Fatalf("address = %q, TON = %d; want %q, %d", part.DA.Number(), part.DA.TypeOfNumber(), test.to, test.ton)
			}
		})
	}
}

func TestBuildSubmitTPDUsWithOptionsForcesUCS2(t *testing.T) {
	tpdus, _, err := BuildSubmitTPDUsWithOptions("10086", "hello", SubmitOptions{Encoding: SMSEncodingUCS2})
	if err != nil {
		t.Fatalf("BuildSubmitTPDUsWithOptions() error = %v", err)
	}
	if len(tpdus) != 1 {
		t.Fatalf("parts=%d want 1", len(tpdus))
	}

	pdu := &tpdu.TPDU{Direction: tpdu.MO}
	if err := pdu.UnmarshalBinary(tpdus[0]); err != nil {
		t.Fatalf("UnmarshalBinary() error = %v", err)
	}
	if pdu.DCS != tpdu.DcsUCS2Data {
		t.Fatalf("DCS=0x%02x want 0x%02x", byte(pdu.DCS), byte(tpdu.DcsUCS2Data))
	}
	if got, want := []byte(pdu.UD), ucs2.Encode([]rune("hello")); string(got) != string(want) {
		t.Fatalf("UD=%x want UCS2 %x", got, want)
	}
}

func TestBuildSubmitTPDUsKeepsAutoEncodingByDefault(t *testing.T) {
	tpdus, _, err := BuildSubmitTPDUs("10086", "hello")
	if err != nil {
		t.Fatalf("BuildSubmitTPDUs() error = %v", err)
	}
	if len(tpdus) != 1 {
		t.Fatalf("parts=%d want 1", len(tpdus))
	}

	pdu := &tpdu.TPDU{Direction: tpdu.MO}
	if err := pdu.UnmarshalBinary(tpdus[0]); err != nil {
		t.Fatalf("UnmarshalBinary() error = %v", err)
	}
	if pdu.DCS != 0x00 {
		t.Fatalf("DCS=0x%02x want auto GSM7 0x00", byte(pdu.DCS))
	}
}
