package media

import (
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/yibaiba/hideck/pkg/g711"
)

func TestRTPHeaderExtensionAndPaddingAreNotPlayed(t *testing.T) {
	pcm := newTestPCM()
	_, conn, relay := testBridge(t, pcm, false)
	// V2 + padding + extension + one CSRC; extension occupies one 32-bit word.
	packet := make([]byte, 24)
	packet[0] = 0xb1
	packet[16], packet[17], packet[19] = 0x10, 0x00, 1
	packet = append(packet, g711.Encode(tone(3000))...)
	packet = append(packet, 0, 0, 0, 4)
	if _, err := relay.WriteTo(packet, conn.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-pcm.playback:
		if !reflect.DeepEqual(got, g711.Decode(g711.Encode(tone(3000)))) {
			t.Fatal("RTP metadata entered PCM")
		}
	case <-time.After(time.Second):
		t.Fatal("extended RTP was not decoded")
	}
}

func TestMalformedRTPFromRelayIsReported(t *testing.T) {
	pcm := newTestPCM()
	b, conn, relay := testBridge(t, pcm, false)
	if _, err := relay.WriteTo([]byte{0x80}, conn.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.Done():
	case <-time.After(time.Second):
		t.Fatal("malformed RTP did not surface")
	}
	if b.Err() == nil || len(pcm.playback) != 0 {
		t.Fatal("invalid media was accepted")
	}
}

func TestPacketsFromAnotherPeerCannotWritePCM(t *testing.T) {
	pcm := newTestPCM()
	b, conn, relay := testBridge(t, pcm, false)
	other := udpSocket(t)
	sendAudio(t, other, conn, tone(6000))
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for b.Stats().RejectedPeer == 0 {
		select {
		case <-deadline.C:
			t.Fatal("unexpected peer was not rejected")
		case <-tick.C:
		}
	}
	sendAudio(t, relay, conn, tone(3000))
	select {
	case got := <-pcm.playback:
		if !reflect.DeepEqual(got, g711.Decode(g711.Encode(tone(3000)))) {
			t.Fatal("other peer reached PCM")
		}
	case <-time.After(time.Second):
		t.Fatal("negotiated relay was rejected")
	}
}

type shortPacketWriter struct{ net.PacketConn }

func (c shortPacketWriter) WriteTo(data []byte, _ net.Addr) (int, error) { return len(data) - 1, nil }

func TestShortDatagramWriteFailsMedia(t *testing.T) {
	pcm := newTestPCM()
	conn, relay := udpSocket(t), udpSocket(t)
	b, err := NewBridge(context.Background(), Config{Conn: shortPacketWriter{conn}, PCM: pcm, Remote: relay.LocalAddr().(*net.UDPAddr).AddrPort()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	pcm.capture <- captureFrame{samples: tone(3000)}
	select {
	case <-b.Done():
	case <-time.After(time.Second):
		t.Fatal("short datagram hidden")
	}
	if !errors.Is(b.Err(), io.ErrShortWrite) {
		t.Fatal(b.Err())
	}
}
