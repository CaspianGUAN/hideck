package qmi

import (
	"context"
	"testing"
)

func TestUIMICCIDPreservesInteriorHexAndMatchesDMS(t *testing.T) {
	const want = "898600123456F7890123"
	data := []byte{0x98, 0x68, 0x00, 0x21, 0x43, 0x65, 0x7F, 0x98, 0x10, 0x32}
	client := newUIMUnitTestClient()
	defer serveUIMUnitTestRequests(t, client, func(req *Packet) *Packet {
		if req.MessageID == UIMReadTransparent {
			return readTransparentPacket(data)
		}
		if req.MessageID == DMSUIMGetICCID {
			return &Packet{TLVs: []TLV{successResultTLV(), {Type: 0x01, Value: []byte(want)}}}
		}
		t.Errorf("unexpected ICCID request %x", req.MessageID)
		return qmiErrorPacket(QMIErrInvalidArg)
	})()
	uim, err := (&UIMService{client: client, clientID: 1}).GetICCID(context.Background())
	if err != nil || uim != want {
		t.Fatalf("UIM ICCID = %q, err=%v", uim, err)
	}
	dms, err := (&DMSService{client: client, clientID: 1}).GetICCID(context.Background())
	if err != nil || dms != uim {
		t.Fatalf("DMS ICCID = %q, UIM=%q, err=%v", dms, uim, err)
	}
}

func TestDecodeICCIDDoesNotChangeDecimalDecoder(t *testing.T) {
	data := []byte{0x98, 0xBA, 0xDC, 0xFE, 0x10, 0xFF}
	if got := decodeICCID(data); got != "89ABCDEF01" {
		t.Fatalf("hex ICCID=%q", got)
	}
	if got := decodeSwappedBCD(data); got != "8901" {
		t.Fatalf("decimal decoder changed: %q", got)
	}
	for _, input := range [][]byte{[]byte("898600123456f7890123"), {0x98, 0x68, 0x00, 0x21, 0x43, 0x65, 0x7F, 0x98, 0x10, 0x32}} {
		if got := decodeUIMDigits(input); got != "898600123456F7890123" {
			t.Fatalf("slot ICCID=%q", got)
		}
	}
}
