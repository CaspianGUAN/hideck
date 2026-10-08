package phone

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtp"
)

const (
	externalSSRCSeed          = 0x48494445
	externalPrimeFrames       = 5
	defaultExternalDTMFType   = 101
	rfc4733EndOfEventBit      = 0x80
	externalReadBufferSize    = 2048
	externalTelephoneEventMax = 15
)

// ExternalMediaOptions configures a plain-RTP client leg (for example a SIP
// PBX) that stands in for the browser's WebRTC leg.
type ExternalMediaOptions struct {
	Owner string
	// AdvertiseIP is written into the SDP c= line; it must be reachable from
	// the peer or the peer must latch onto our RTP source (symmetric RTP).
	AdvertiseIP string
	// OnDTMF receives RFC 4733 digits sent by the peer.
	OnDTMF func(digit string)
}

// ExternalMedia is a media session whose client leg is plain G.711 RTP.
type ExternalMedia struct {
	ID      string
	Lease   string
	session *MediaSession
}

type externalLeg struct {
	conn        *net.UDPConn
	advertiseIP string
	onDTMF      func(string)

	mu              sync.Mutex
	remote          *net.UDPAddr
	codec           string
	payloadType     uint8
	dtmfPayloadType uint8
	sequence        uint16
	timestamp       uint32
	ssrc            uint32
	lastDTMFStamp   uint32
	dtmfEnded       bool
}

func newExternalMediaSession(id, lease string, options ExternalMediaOptions, manager *MediaManager) (*MediaSession, error) {
	advertise := net.ParseIP(strings.TrimSpace(options.AdvertiseIP)).To4()
	if advertise == nil {
		return nil, errors.New("phone: external media requires an IPv4 advertise address")
	}
	imsConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, fmt.Errorf("phone: listen RTP bridge: %w", err)
	}
	externalConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		_ = imsConn.Close()
		return nil, fmt.Errorf("phone: listen external RTP: %w", err)
	}
	leg := &externalLeg{
		conn: externalConn, advertiseIP: advertise.String(), onDTMF: options.OnDTMF,
		codec: "PCMU", payloadType: 0, dtmfPayloadType: defaultExternalDTMFType,
		ssrc: randomSSRC(), sequence: uint16(randomSSRC()),
	}
	session := &MediaSession{
		ID: id, Lease: lease, Owner: options.Owner, rtpConn: imsConn, external: leg,
		// AMR-WB/AMR lead the IMS offer. A G.711-only offer forces the
		// network to transcode toward a VoLTE handset; AIS does not, so a call
		// to a Thai mobile drops after a second with "no media" while IVRs
		// that speak G.711 still work. HiDeck transcodes AMR for the PBX.
		realtimeCodecs:   append([]string(nil), manager.realtimeCodecs...),
		newRealtimeCodec: manager.newRealtimeCodec, closed: make(chan struct{}),
	}
	go session.forwardIMSRTP()
	go session.readExternalRTP()
	return session, nil
}

// CreateExternal registers a plain-RTP media session under a fresh lease.
func (m *MediaManager) CreateExternal(options ExternalMediaOptions) (*ExternalMedia, error) {
	if m == nil {
		return nil, errors.New("phone: media manager is unavailable")
	}
	mediaID, err := randomToken(16)
	if err != nil {
		return nil, err
	}
	lease, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	session, err := newExternalMediaSession(mediaID, lease, options, m)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.sessions[mediaID] = session
	m.mu.Unlock()
	return &ExternalMedia{ID: mediaID, Lease: lease, session: session}, nil
}

// CreateExternalMedia creates a plain-RTP client leg usable with StartCall and Answer.
func (s *Service) CreateExternalMedia(options ExternalMediaOptions) (*ExternalMedia, error) {
	return s.media.CreateExternal(options)
}

// ReleaseExternalMedia closes a leg that never got bound to a call.
func (s *Service) ReleaseExternalMedia(media *ExternalMedia) {
	if media != nil {
		s.media.Remove(media.ID)
	}
}

// OfferSDP describes the leg for an INVITE we originate.
func (m *ExternalMedia) OfferSDP() string {
	return m.session.external.sdp([]uint8{0, 8})
}

// AnswerOffer selects a codec from the peer's offer and returns our answer.
func (m *ExternalMedia) AnswerOffer(offer string) (string, error) {
	leg := m.session.external
	if err := leg.applyRemote(offer); err != nil {
		return "", err
	}
	leg.prime()
	leg.mu.Lock()
	payload := leg.payloadType
	leg.mu.Unlock()
	return leg.sdp([]uint8{payload}), nil
}

// ApplyAnswer adopts the peer's answer to an offer we sent.
func (m *ExternalMedia) ApplyAnswer(answer string) error {
	leg := m.session.external
	if err := leg.applyRemote(answer); err != nil {
		return err
	}
	leg.prime()
	return nil
}

func (leg *externalLeg) sdp(payloads []uint8) string {
	port := leg.conn.LocalAddr().(*net.UDPAddr).Port
	leg.mu.Lock()
	dtmf := leg.dtmfPayloadType
	leg.mu.Unlock()
	formats := make([]string, 0, len(payloads)+1)
	attributes := make([]string, 0, len(payloads)+2)
	for _, payload := range payloads {
		formats = append(formats, strconv.Itoa(int(payload)))
		name := "PCMU"
		if payload == 8 {
			name = "PCMA"
		}
		attributes = append(attributes, fmt.Sprintf("a=rtpmap:%d %s/8000", payload, name))
	}
	formats = append(formats, strconv.Itoa(int(dtmf)))
	attributes = append(attributes,
		fmt.Sprintf("a=rtpmap:%d telephone-event/8000", dtmf),
		fmt.Sprintf("a=fmtp:%d 0-%d", dtmf, externalTelephoneEventMax))
	session := time.Now().Unix()
	return fmt.Sprintf(
		"v=0\r\no=hideck %d %d IN IP4 %s\r\ns=HiDeck\r\nc=IN IP4 %s\r\nt=0 0\r\nm=audio %d RTP/AVP %s\r\n%s\r\na=ptime:20\r\na=sendrecv\r\n",
		session, session, leg.advertiseIP, leg.advertiseIP, port,
		strings.Join(formats, " "), strings.Join(attributes, "\r\n"),
	)
}

func (leg *externalLeg) applyRemote(raw string) error {
	endpoint, err := parseRTPEndpoint(raw, "PCMU", "PCMA")
	if err != nil {
		return err
	}
	dtmf, ok := telephoneEventPayloadType(raw)
	leg.mu.Lock()
	defer leg.mu.Unlock()
	leg.remote = endpoint.Address
	leg.codec, leg.payloadType = endpoint.Codec, endpoint.PayloadType
	if ok {
		leg.dtmfPayloadType = dtmf
	}
	return nil
}

func telephoneEventPayloadType(raw string) (uint8, bool) {
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "a=rtpmap:"))
		if len(fields) != 2 || !strings.HasPrefix(strings.TrimSpace(line), "a=rtpmap:") {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(fields[1]), "telephone-event/8000") {
			continue
		}
		if value, err := strconv.Atoi(fields[0]); err == nil && value >= 0 && value < 128 {
			return uint8(value), true
		}
	}
	return 0, false
}

// prime sends a few silent frames so a NAT between us and the peer opens and
// a symmetric-RTP peer latches onto our real source address.
func (leg *externalLeg) prime() {
	silence := make([]byte, browserSamplesPerFrame)
	for index := range silence {
		silence[index] = 0xff
	}
	for range externalPrimeFrames {
		_ = leg.write(&rtp.Packet{Payload: append([]byte(nil), silence...)})
	}
}

// write sends one PCMU frame to the peer, converting to its negotiated codec.
func (leg *externalLeg) write(packet *rtp.Packet) error {
	leg.mu.Lock()
	remote, codec, payloadType := leg.remote, leg.codec, leg.payloadType
	leg.sequence++
	leg.timestamp += browserSamplesPerFrame
	header := rtp.Header{
		Version: 2, PayloadType: payloadType, SequenceNumber: leg.sequence,
		Timestamp: leg.timestamp, SSRC: leg.ssrc, Marker: packet.Marker,
	}
	leg.mu.Unlock()
	if remote == nil {
		return errors.New("phone: external RTP peer is not negotiated yet")
	}
	payload := append([]byte(nil), packet.Payload...)
	transcodeG711(payload, "PCMU", codec)
	raw, err := (&rtp.Packet{Header: header, Payload: payload}).Marshal()
	if err != nil {
		return err
	}
	_, err = leg.conn.WriteToUDP(raw, remote)
	return err
}

func (s *MediaSession) readExternalRTP() {
	leg := s.external
	buffer := make([]byte, externalReadBufferSize)
	for {
		read, source, err := leg.conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		packet := &rtp.Packet{}
		if packet.Unmarshal(append([]byte(nil), buffer[:read]...)) != nil {
			continue
		}
		leg.latch(source)
		leg.mu.Lock()
		codec, payloadType, dtmfType := leg.codec, leg.payloadType, leg.dtmfPayloadType
		leg.mu.Unlock()
		switch {
		case packet.PayloadType == dtmfType:
			leg.handleDTMF(packet)
		case packet.PayloadType == payloadType:
			s.forwardExternalAudio(packet, codec)
		}
	}
}

// latch follows the peer's actual RTP source (symmetric RTP), which differs
// from its SDP address whenever a NAT sits between the two legs.
func (leg *externalLeg) latch(source *net.UDPAddr) {
	leg.mu.Lock()
	defer leg.mu.Unlock()
	if leg.remote == nil || !leg.remote.IP.Equal(source.IP) || leg.remote.Port != source.Port {
		leg.remote = source
	}
}

func (s *MediaSession) forwardExternalAudio(packet *rtp.Packet, codec string) {
	payload := packet.Payload
	if len(payload) == 0 || len(payload)%browserSamplesPerFrame != 0 {
		s.lost.Add(1)
		return
	}
	for offset := 0; offset < len(payload); offset += browserSamplesPerFrame {
		frame := append([]byte(nil), payload[offset:offset+browserSamplesPerFrame]...)
		transcodeG711(frame, codec, "PCMU")
		header := packet.Header
		header.Timestamp += uint32(offset)
		header.SequenceNumber += uint16(offset / browserSamplesPerFrame)
		s.forwardBrowserPacket(&rtp.Packet{Header: header, Payload: frame})
	}
}

func (leg *externalLeg) handleDTMF(packet *rtp.Packet) {
	if len(packet.Payload) < 4 || packet.Payload[1]&rfc4733EndOfEventBit == 0 {
		return
	}
	leg.mu.Lock()
	duplicate := leg.dtmfEnded && leg.lastDTMFStamp == packet.Timestamp
	leg.lastDTMFStamp, leg.dtmfEnded = packet.Timestamp, true
	callback := leg.onDTMF
	leg.mu.Unlock()
	if duplicate || callback == nil {
		return
	}
	if digit := rfc4733Digit(packet.Payload[0]); digit != "" {
		callback(digit)
	}
}

func rfc4733Digit(event byte) string {
	switch {
	case event <= 9:
		return string(rune('0' + event))
	case event == 10:
		return "*"
	case event == 11:
		return "#"
	default:
		return ""
	}
}

func randomSSRC() uint32 {
	var buffer [4]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return externalSSRCSeed
	}
	return binary.BigEndian.Uint32(buffer[:])
}
