package phone

import (
	"bytes"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp"
)

func newTestExternalMedia(t *testing.T, onDTMF func(string)) *ExternalMedia {
	t.Helper()
	manager := &MediaManager{sessions: make(map[string]*MediaSession)}
	media, err := manager.CreateExternal(ExternalMediaOptions{
		Owner: "sip:9001", AdvertiseIP: "127.0.0.1", OnDTMF: onDTMF,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = media.session.Close() })
	return media
}

func listenTestRTP(t *testing.T) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func g711OfferSDP(port int, payloads string) string {
	return fmt.Sprintf("v=0\r\no=pbx 1 1 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\n"+
		"m=audio %d RTP/AVP %s\r\na=rtpmap:0 PCMU/8000\r\na=rtpmap:8 PCMA/8000\r\n"+
		"a=rtpmap:96 telephone-event/8000\r\na=fmtp:96 0-16\r\n", port, payloads)
}

func readTestRTP(t *testing.T, conn *net.UDPConn, payloadType uint8) *rtp.Packet {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 2048)
	for {
		read, _, err := conn.ReadFromUDP(buffer)
		if err != nil {
			t.Fatalf("read RTP: %v", err)
		}
		packet := &rtp.Packet{}
		if err := packet.Unmarshal(append([]byte(nil), buffer[:read]...)); err != nil {
			t.Fatal(err)
		}
		if packet.PayloadType == payloadType && !bytes.Equal(packet.Payload, silentPCMA()) &&
			!bytes.Equal(packet.Payload, silentPCMU()) {
			return packet
		}
	}
}

func silentPCMU() []byte { return bytes.Repeat([]byte{0xff}, browserSamplesPerFrame) }

func silentPCMA() []byte {
	frame := silentPCMU()
	transcodeG711(frame, "PCMU", "PCMA")
	return frame
}

func sendTestRTP(t *testing.T, from *net.UDPConn, to *net.UDPAddr, packet *rtp.Packet) {
	t.Helper()
	raw, err := packet.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := from.WriteToUDP(raw, to); err != nil {
		t.Fatal(err)
	}
}

func TestExternalMediaAnswerSelectsOfferedG711Codec(t *testing.T) {
	media := newTestExternalMedia(t, nil)
	answer, err := media.AnswerOffer(g711OfferSDP(40000, "8 0 96"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(answer, "m=audio ") || !strings.Contains(answer, "RTP/AVP 8 96\r\n") ||
		!strings.Contains(answer, "a=rtpmap:8 PCMA/8000") || !strings.Contains(answer, "a=rtpmap:96 telephone-event/8000") {
		t.Fatalf("answer SDP = %q", answer)
	}
	if !strings.Contains(media.OfferSDP(), "RTP/AVP 0 8 96") {
		t.Fatalf("offer after negotiation = %q", media.OfferSDP())
	}
}

func TestExternalMediaRejectsOfferWithoutG711(t *testing.T) {
	media := newTestExternalMedia(t, nil)
	offer := "v=0\r\nc=IN IP4 127.0.0.1\r\nm=audio 40000 RTP/AVP 9\r\na=rtpmap:9 G722/8000\r\n"
	if _, err := media.AnswerOffer(offer); err == nil {
		t.Fatal("AnswerOffer() accepted an offer without G.711")
	}
}

func TestExternalMediaBridgesPCMAToIMSAndBack(t *testing.T) {
	media := newTestExternalMedia(t, nil)
	pbx, ims := listenTestRTP(t), listenTestRTP(t)
	if _, err := media.AnswerOffer(g711OfferSDP(pbx.LocalAddr().(*net.UDPAddr).Port, "8 96")); err != nil {
		t.Fatal(err)
	}
	imsSDP := fmt.Sprintf("v=0\r\nc=IN IP4 127.0.0.1\r\nm=audio %d RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\n",
		ims.LocalAddr().(*net.UDPAddr).Port)
	if err := media.session.Attach(imsSDP); err != nil {
		t.Fatal(err)
	}
	legAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: media.session.external.conn.LocalAddr().(*net.UDPAddr).Port}

	pcmu := bytes.Repeat([]byte{0x2a}, browserSamplesPerFrame)
	pcma := append([]byte(nil), pcmu...)
	transcodeG711(pcma, "PCMU", "PCMA")
	sendTestRTP(t, pbx, legAddr, &rtp.Packet{
		Header:  rtp.Header{Version: 2, PayloadType: 8, SequenceNumber: 1, Timestamp: 160, SSRC: 7},
		Payload: pcma,
	})
	toIMS := readTestRTP(t, ims, 0)
	if !bytes.Equal(toIMS.Payload, pcmu) {
		t.Fatalf("IMS received %x, want PCMU %x", toIMS.Payload[:4], pcmu[:4])
	}

	imsAddr := media.session.rtpConn.LocalAddr().(*net.UDPAddr)
	sendTestRTP(t, ims, imsAddr, &rtp.Packet{
		Header:  rtp.Header{Version: 2, PayloadType: 0, SequenceNumber: 9, Timestamp: 320, SSRC: 9},
		Payload: pcmu,
	})
	toPBX := readTestRTP(t, pbx, 8)
	if !bytes.Equal(toPBX.Payload, pcma) {
		t.Fatalf("PBX received %x, want PCMA %x", toPBX.Payload[:4], pcma[:4])
	}
}

func TestExternalMediaReportsEachRFC4733DigitOnce(t *testing.T) {
	var mu sync.Mutex
	var digits []string
	media := newTestExternalMedia(t, func(digit string) {
		mu.Lock()
		digits = append(digits, digit)
		mu.Unlock()
	})
	pbx := listenTestRTP(t)
	if _, err := media.AnswerOffer(g711OfferSDP(pbx.LocalAddr().(*net.UDPAddr).Port, "0 96")); err != nil {
		t.Fatal(err)
	}
	legAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: media.session.external.conn.LocalAddr().(*net.UDPAddr).Port}
	send := func(event byte, end bool, timestamp uint32) {
		flags := byte(10)
		if end {
			flags |= rfc4733EndOfEventBit
		}
		sendTestRTP(t, pbx, legAddr, &rtp.Packet{
			Header:  rtp.Header{Version: 2, PayloadType: 96, Timestamp: timestamp, SSRC: 7},
			Payload: []byte{event, flags, 0x03, 0x20},
		})
	}
	send(5, false, 1000)
	for range 3 {
		send(5, true, 1000)
	}
	for range 3 {
		send(11, true, 2000)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := strings.Join(digits, "")
		mu.Unlock()
		if got == "5#" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("digits = %q, want %q", strings.Join(digits, ""), "5#")
}
