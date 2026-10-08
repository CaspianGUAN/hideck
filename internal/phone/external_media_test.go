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

func TestExternalMediaOffersRealtimeCodecsBeforeG711ToIMS(t *testing.T) {
	manager := &MediaManager{
		sessions:       make(map[string]*MediaSession),
		realtimeCodecs: []string{"AMR-WB", "AMR"},
	}
	media, err := manager.CreateExternal(ExternalMediaOptions{Owner: "sip:9001", AdvertiseIP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = media.session.Close() })
	offer := media.session.PlainSDP()
	if !strings.Contains(offer, "RTP/AVP 104 110 102 114 0 8 101\r\n") ||
		!strings.Contains(offer, "AMR-WB/16000") || !strings.Contains(offer, "PCMA/8000") {
		t.Fatalf("IMS offer = %q", offer)
	}
}

func TestExternalMediaAnswersInboundAMRAndTranscodes(t *testing.T) {
	var negotiated string
	codec := &markerRealtimeCodec{sampleRate: 8000, pcmSamples: 160}
	manager := &MediaManager{
		sessions: make(map[string]*MediaSession),
		newRealtimeCodec: func(name, fmtp string) (RealtimeCodec, error) {
			negotiated = name + " " + fmtp
			if name == "AMR-WB" {
				codec.sampleRate, codec.pcmSamples = 16000, 320
			}
			return codec, nil
		},
	}
	media, err := manager.CreateExternal(ExternalMediaOptions{Owner: "sip:9001", AdvertiseIP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = media.session.Close() })
	if strings.Contains(media.session.PlainSDP(), "AMR") {
		t.Fatalf("outbound offer includes AMR: %q", media.session.PlainSDP())
	}
	pbx, ims := listenTestRTP(t), listenTestRTP(t)
	if err := media.ApplyAnswer(g711OfferSDP(pbx.LocalAddr().(*net.UDPAddr).Port, "0 96")); err != nil {
		t.Fatal(err)
	}
	offer := fmt.Sprintf("v=0\r\nc=IN IP4 127.0.0.1\r\nm=audio %d RTP/AVP 102 0\r\n"+
		"a=rtpmap:102 AMR/8000\r\na=fmtp:102 octet-align=0;mode-set=7\r\na=rtpmap:0 PCMU/8000\r\n",
		ims.LocalAddr().(*net.UDPAddr).Port)
	if err := media.session.Attach(offer); err != nil {
		t.Fatal(err)
	}
	answer := media.session.PlainSDP()
	if !strings.Contains(answer, "AMR/8000") || !strings.Contains(answer, "a=fmtp:102 octet-align=0;mode-set=7") ||
		strings.Contains(answer, "PCMU/8000") {
		t.Fatalf("IMS answer = %q", answer)
	}
	if negotiated != "AMR octet-align=0;mode-set=7" {
		t.Fatalf("negotiated codec = %q", negotiated)
	}

	externalAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: media.session.external.conn.LocalAddr().(*net.UDPAddr).Port}
	sendTestRTP(t, pbx, externalAddr, &rtp.Packet{
		Header:  rtp.Header{Version: 2, PayloadType: 0, SequenceNumber: 1, Timestamp: 160, SSRC: 7},
		Payload: bytes.Repeat([]byte{0x00}, browserSamplesPerFrame),
	})
	if got := readRTPPayload(t, ims, 102, []byte{0x22}); !bytes.Equal(got, []byte{0x22}) {
		t.Fatalf("IMS payload = %x", got)
	}

	sendTestRTP(t, ims, media.session.rtpConn.LocalAddr().(*net.UDPAddr), &rtp.Packet{
		Header:  rtp.Header{Version: 2, PayloadType: 102, SequenceNumber: 4, Timestamp: 160, SSRC: 9},
		Payload: []byte("amr-speech"),
	})
	want := bytes.Repeat([]byte{pcmToMuLaw(1000)}, browserSamplesPerFrame)
	if got := readTestRTP(t, pbx, 0); !bytes.Equal(got.Payload, want) {
		t.Fatalf("PBX payload = %x", got.Payload)
	}
}

func readMatchingRTP(conn *net.UDPConn, payloadType uint8, want []byte) ([]byte, error) {
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return nil, err
	}
	buffer := make([]byte, 2048)
	for {
		read, _, err := conn.ReadFromUDP(buffer)
		if err != nil {
			return nil, err
		}
		packet := &rtp.Packet{}
		if err := packet.Unmarshal(append([]byte(nil), buffer[:read]...)); err != nil {
			return nil, err
		}
		if packet.PayloadType == payloadType && bytes.Equal(packet.Payload, want) {
			return packet.Payload, nil
		}
	}
}

func TestExternalMediaAnswersFirstInboundAMRCodec(t *testing.T) {
	manager := &MediaManager{
		sessions: make(map[string]*MediaSession),
		newRealtimeCodec: func(name, _ string) (RealtimeCodec, error) {
			rate := 8000
			if name == "AMR-WB" {
				rate = 16000
			}
			return &markerRealtimeCodec{sampleRate: rate, pcmSamples: rate / 50}, nil
		},
	}
	media, err := manager.CreateExternal(ExternalMediaOptions{Owner: "sip:9001", AdvertiseIP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = media.session.Close() })
	ims := listenTestRTP(t)
	offer := fmt.Sprintf("v=0\r\nc=IN IP4 127.0.0.1\r\nm=audio %d RTP/AVP 104 102 0\r\n"+
		"a=rtpmap:104 AMR-WB/16000\r\na=fmtp:104 octet-align=1\r\n"+
		"a=rtpmap:102 AMR/8000\r\na=rtpmap:0 PCMU/8000\r\n", ims.LocalAddr().(*net.UDPAddr).Port)
	if err := media.session.Attach(offer); err != nil {
		t.Fatal(err)
	}
	answer := media.session.PlainSDP()
	if !strings.Contains(answer, "AMR-WB/16000") || strings.Contains(answer, "AMR/8000") {
		t.Fatalf("IMS answer = %q", answer)
	}
}

func readRTPPayload(t *testing.T, conn *net.UDPConn, payloadType uint8, want []byte) []byte {
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
		if packet.PayloadType == payloadType && bytes.Equal(packet.Payload, want) {
			return packet.Payload
		}
	}
}

type markerRealtimeCodec struct {
	sampleRate int
	pcmSamples int
}

func (codec *markerRealtimeCodec) SampleRate() int { return codec.sampleRate }

func (codec *markerRealtimeCodec) Decode([]byte) ([]int16, error) {
	pcm := make([]int16, codec.pcmSamples)
	for index := range pcm {
		pcm[index] = 1000
	}
	return pcm, nil
}

func (codec *markerRealtimeCodec) Encode(pcm []int16) ([]byte, error) {
	for _, sample := range pcm {
		if sample != 0 {
			return []byte{0x22}, nil
		}
	}
	return []byte{0x11}, nil
}

func (codec *markerRealtimeCodec) Close() error { return nil }

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

func TestExternalMediaKeepsBothDirectionsAliveAfterAttach(t *testing.T) {
	media := newTestExternalMedia(t, nil)
	pbx, ims := listenTestRTP(t), listenTestRTP(t)
	if _, err := media.AnswerOffer(g711OfferSDP(pbx.LocalAddr().(*net.UDPAddr).Port, "0 96")); err != nil {
		t.Fatal(err)
	}
	drainRTP(t, pbx)
	imsSDP := fmt.Sprintf("v=0\r\nc=IN IP4 127.0.0.1\r\nm=audio %d RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\n",
		ims.LocalAddr().(*net.UDPAddr).Port)
	if err := media.session.Attach(imsSDP); err != nil {
		t.Fatal(err)
	}
	if got := readAnyRTP(t, ims); !bytes.Equal(got.Payload, silentPCMU()) {
		t.Fatalf("IMS keepalive = %x, want PCMU silence", got.Payload)
	}
	if got := readAnyRTP(t, pbx); !bytes.Equal(got.Payload, silentPCMU()) {
		t.Fatalf("PBX keepalive = %x, want PCMU silence", got.Payload)
	}
}

func drainRTP(t *testing.T, conn *net.UDPConn) {
	t.Helper()
	buffer := make([]byte, 2048)
	for {
		if err := conn.SetReadDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := conn.ReadFromUDP(buffer); err != nil {
			return
		}
	}
}

func readAnyRTP(t *testing.T, conn *net.UDPConn) *rtp.Packet {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 2048)
	read, _, err := conn.ReadFromUDP(buffer)
	if err != nil {
		t.Fatalf("read RTP: %v", err)
	}
	packet := &rtp.Packet{}
	if err := packet.Unmarshal(append([]byte(nil), buffer[:read]...)); err != nil {
		t.Fatal(err)
	}
	return packet
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

// An IVR handing the call to another media server restarts SSRC and sequence
// numbers mid-call (seen on T-Mobile 611); audio must keep flowing.
func TestIMSStreamSwitchKeepsAudioFlowing(t *testing.T) {
	media := newTestExternalMedia(t, nil)
	pbx, ims := listenTestRTP(t), listenTestRTP(t)
	if _, err := media.AnswerOffer(g711OfferSDP(pbx.LocalAddr().(*net.UDPAddr).Port, "0 96")); err != nil {
		t.Fatal(err)
	}
	imsSDP := fmt.Sprintf("v=0\r\nc=IN IP4 127.0.0.1\r\nm=audio %d RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\n",
		ims.LocalAddr().(*net.UDPAddr).Port)
	if err := media.session.Attach(imsSDP); err != nil {
		t.Fatal(err)
	}
	imsAddr := media.session.rtpConn.LocalAddr().(*net.UDPAddr)
	send := func(ssrc uint32, sequence uint16, fill byte) {
		sendTestRTP(t, ims, imsAddr, &rtp.Packet{
			Header:  rtp.Header{Version: 2, PayloadType: 0, SequenceNumber: sequence, Timestamp: uint32(sequence) * 160, SSRC: ssrc},
			Payload: bytes.Repeat([]byte{fill}, browserSamplesPerFrame),
		})
	}
	for index := uint16(0); index < 3; index++ {
		send(0x1111, 100+index, 0x2a)
	}
	if got := readTestRTP(t, pbx, 0); got.Payload[0] != 0x2a {
		t.Fatalf("first stream payload = %x", got.Payload[0])
	}
	for index := uint16(0); index < 10; index++ {
		send(0x2222, 40000+index, 0x3b)
		time.Sleep(jitterTick)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := readTestRTP(t, pbx, 0); got.Payload[0] == 0x3b {
			return
		}
	}
	t.Fatal("audio from the second media stream never reached the PBX leg")
}

func TestExternalMediaUplinkKeepsOneRTPStreamAcrossKeepaliveAndPBXAudio(t *testing.T) {
	manager := &MediaManager{sessions: make(map[string]*MediaSession)}
	media, err := manager.CreateExternal(ExternalMediaOptions{Owner: "sip:9001", AdvertiseIP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = media.session.Close() })
	pbx, ims := listenTestRTP(t), listenTestRTP(t)
	if err := media.ApplyAnswer(g711OfferSDP(pbx.LocalAddr().(*net.UDPAddr).Port, "0")); err != nil {
		t.Fatal(err)
	}
	offer := fmt.Sprintf("v=0\r\nc=IN IP4 127.0.0.1\r\nm=audio %d RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\n",
		ims.LocalAddr().(*net.UDPAddr).Port)
	if err := media.session.Attach(offer); err != nil {
		t.Fatal(err)
	}
	// Keepalive silence first, then the PBX stream with its own SSRC and counters.
	first := readAnyTestRTP(t, ims)
	externalAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: media.session.external.conn.LocalAddr().(*net.UDPAddr).Port}
	for index := 0; index < 3; index++ {
		sendTestRTP(t, pbx, externalAddr, &rtp.Packet{
			Header:  rtp.Header{Version: 2, PayloadType: 0, SequenceNumber: uint16(9000 + index), Timestamp: uint32(77000 + 160*index), SSRC: 0xabcdef},
			Payload: bytes.Repeat([]byte{0x10}, browserSamplesPerFrame),
		})
	}
	previous := first
	sawPBX := false
	for index := 0; index < 40 && !sawPBX; index++ {
		packet := readAnyTestRTP(t, ims)
		if packet.SSRC != first.SSRC {
			t.Fatalf("SSRC changed from %x to %x", first.SSRC, packet.SSRC)
		}
		if packet.SequenceNumber != previous.SequenceNumber+1 || packet.Timestamp != previous.Timestamp+browserSamplesPerFrame {
			t.Fatalf("packet seq %d ts %d after seq %d ts %d", packet.SequenceNumber, packet.Timestamp,
				previous.SequenceNumber, previous.Timestamp)
		}
		sawPBX = bytes.Equal(packet.Payload, bytes.Repeat([]byte{0x10}, browserSamplesPerFrame))
		previous = packet
	}
	if !sawPBX {
		t.Fatal("PBX audio never reached IMS")
	}
}

func readAnyTestRTP(t *testing.T, conn *net.UDPConn) *rtp.Packet {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 2048)
	read, _, err := conn.ReadFromUDP(buffer)
	if err != nil {
		t.Fatalf("read RTP: %v", err)
	}
	packet := &rtp.Packet{}
	if err := packet.Unmarshal(append([]byte(nil), buffer[:read]...)); err != nil {
		t.Fatal(err)
	}
	return packet
}
