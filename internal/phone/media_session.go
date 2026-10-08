package phone

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"github.com/pion/webrtc/v4"
)

type mediaSessionOptions struct {
	ID, Lease, Owner, Offer string
	API                     *webrtc.API
	ICEServers              []webrtc.ICEServer
	RealtimeCodecs          []string
	NewRealtimeCodec        RealtimeCodecFactory
	OnState                 func(string, webrtc.PeerConnectionState)
}

type MediaSession struct {
	ID, Lease, Owner     string
	peer                 *webrtc.PeerConnection
	track                *webrtc.TrackLocalStaticRTP
	rtpConn              *net.UDPConn
	onState              func(string, webrtc.PeerConnectionState)
	realtimeCodecs       []string
	newRealtimeCodec     RealtimeCodecFactory
	mu                   sync.RWMutex
	remote               rtpEndpoint
	realtimeCodec        RealtimeCodec
	recorder             *mixedRecorder
	attached             bool
	attachedSDP          string
	closed               chan struct{}
	closeOnce            sync.Once
	silentWorker         sync.WaitGroup
	silentStarted        bool
	externalQuietStarted bool
	uplinkStop           chan struct{}
	downlinkStop         chan struct{}
	receiveOnly          bool
	external             *externalLeg
	uplinkMu             sync.Mutex
	uplinkSequence       uint16
	uplinkTimestamp      uint32
	fromIMS              atomic.Uint64
	toIMS                atomic.Uint64
	lost                 atomic.Uint64
}

func newMediaSession(ctx context.Context, options mediaSessionOptions) (*MediaSession, string, error) {
	receiveOnly, err := browserOfferReceivesOnlyAudio(options.Offer)
	if err != nil {
		return nil, "", err
	}
	connection, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, "", fmt.Errorf("phone: listen RTP bridge: %w", err)
	}
	peer, err := options.API.NewPeerConnection(webrtc.Configuration{ICEServers: options.ICEServers})
	if err != nil {
		_ = connection.Close()
		return nil, "", fmt.Errorf("phone: create PeerConnection: %w", err)
	}
	session := &MediaSession{
		ID: options.ID, Lease: options.Lease, Owner: options.Owner,
		peer: peer, rtpConn: connection, onState: options.OnState,
		realtimeCodecs:   append([]string(nil), options.RealtimeCodecs...),
		newRealtimeCodec: options.NewRealtimeCodec, receiveOnly: receiveOnly,
		closed: make(chan struct{}),
	}
	answer, err := session.negotiate(ctx, options.Offer)
	if err != nil {
		_ = session.Close()
		return nil, "", err
	}
	go session.forwardIMSRTP()
	return session, answer, nil
}

func (s *MediaSession) negotiate(ctx context.Context, offer string) (string, error) {
	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypePCMU, ClockRate: 8000, Channels: 1,
	}, "audio", "hideck-phone")
	if err != nil {
		return "", fmt.Errorf("phone: create browser audio track: %w", err)
	}
	s.track = track
	sender, err := s.peer.AddTrack(track)
	if err != nil {
		return "", fmt.Errorf("phone: add browser audio track: %w", err)
	}
	go drainRTCP(sender, s.closed)
	s.peer.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) { go s.forwardBrowserRTP(remote) })
	s.peer.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if s.onState != nil {
			s.onState(s.ID, state)
		}
	})
	if err := s.peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		return "", fmt.Errorf("phone: apply WebRTC offer: %w", err)
	}
	gatheringComplete := webrtc.GatheringCompletePromise(s.peer)
	answer, err := s.peer.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("phone: create WebRTC answer: %w", err)
	}
	if err := s.peer.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("phone: apply WebRTC answer: %w", err)
	}
	select {
	case <-gatheringComplete:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return s.peer.LocalDescription().SDP, nil
}

func (s *MediaSession) PlainSDP() string {
	port := s.rtpConn.LocalAddr().(*net.UDPAddr).Port
	s.mu.RLock()
	endpoint, attached := s.remote, s.attached
	s.mu.RUnlock()
	if attached {
		return plainSelectedAudioSDP(port, endpoint)
	}
	return plainAudioSDP(port, s.realtimeCodecs)
}

func (s *MediaSession) Attach(remoteSDP string) error {
	s.mu.RLock()
	same := s.attached && s.attachedSDP == remoteSDP
	s.mu.RUnlock()
	if same {
		// Early media already attached this SDP; keep the running relay.
		return nil
	}
	endpoint, err := parseRTPEndpoint(remoteSDP, s.attachableCodecs()...)
	if err != nil {
		return err
	}
	codec, err := s.createRealtimeCodec(endpoint)
	if err != nil {
		return err
	}
	s.mu.Lock()
	previous := s.realtimeCodec
	s.realtimeCodec = codec
	s.remote, s.attached, s.attachedSDP = endpoint, true, remoteSDP
	s.mu.Unlock()
	var closeErr error
	if previous != nil {
		closeErr = previous.Close()
	}
	primeErr := s.primeRelay()
	if primeErr == nil && s.receiveOnly {
		s.startSilentRTP()
	}
	if primeErr == nil && s.external != nil {
		// A PBX leg does not keep a microphone stream the way the browser
		// does. Keep both directions alive until real audio replaces them.
		s.startPBXBridgeKeepalive()
	}
	return errors.Join(closeErr, primeErr)
}

func (s *MediaSession) attachableCodecs() []string {
	supported := []string{"PCMU", "PCMA"}
	if len(s.realtimeCodecs) > 0 {
		return append(supported, s.realtimeCodecs...)
	}
	if s.external != nil && s.newRealtimeCodec != nil {
		// The PBX offer stays G.711. An inbound IMS offer leads with AMR, so
		// answer that codec and transcode each 20ms frame for Asterisk.
		return append(supported, "AMR", "AMR-WB")
	}
	return supported
}

func (s *MediaSession) createRealtimeCodec(endpoint rtpEndpoint) (RealtimeCodec, error) {
	if endpoint.Codec != "AMR" && endpoint.Codec != "AMR-WB" && endpoint.Codec != "EVS" {
		return nil, nil
	}
	if s.newRealtimeCodec == nil {
		return nil, fmt.Errorf("phone: negotiated %s codec requires an unavailable encoder", endpoint.Codec)
	}
	codec, err := s.newRealtimeCodec(endpoint.Codec, endpoint.Fmtp)
	if err != nil {
		return nil, fmt.Errorf("phone: initialize negotiated %s codec: %w", endpoint.Codec, err)
	}
	if codec == nil {
		return nil, fmt.Errorf("phone: initialize negotiated %s codec: factory returned nil", endpoint.Codec)
	}
	if codec.SampleRate() != endpoint.ClockRate {
		mismatch := fmt.Errorf("phone: negotiated %s clock rate %d does not match codec rate %d", endpoint.Codec, endpoint.ClockRate, codec.SampleRate())
		return nil, errors.Join(mismatch, codec.Close())
	}
	return codec, nil
}

func (s *MediaSession) Matches(owner, lease string) bool {
	return s != nil && s.Owner == owner && secureEqual(s.Lease, lease)
}

func (s *MediaSession) SetRecorder(recorder *mixedRecorder) {
	s.mu.Lock()
	s.recorder = recorder
	s.mu.Unlock()
}

func (s *MediaSession) Stats() MediaStats {
	return MediaStats{
		PacketsFromIMS: s.fromIMS.Load(), PacketsToIMS: s.toIMS.Load(), PacketsLost: s.lost.Load(),
	}
}

func (s *MediaSession) Close() error {
	if s == nil {
		return nil
	}
	var result error
	s.closeOnce.Do(func() {
		s.stopSilentRTP()
		s.mu.Lock()
		codec := s.realtimeCodec
		s.realtimeCodec = nil
		s.recorder = nil
		s.mu.Unlock()
		if codec != nil {
			result = errors.Join(result, codec.Close())
		}
		if s.peer != nil {
			result = errors.Join(result, s.peer.Close())
		}
		if s.external != nil {
			result = errors.Join(result, s.external.conn.Close())
		}
		result = errors.Join(result, s.rtpConn.Close())
	})
	return result
}

func (s *MediaSession) startSilentRTP() {
	s.mu.Lock()
	if s.silentStarted || sessionClosed(s.closed) {
		s.mu.Unlock()
		return
	}
	s.silentStarted = true
	if s.uplinkStop == nil {
		s.uplinkStop = make(chan struct{})
	}
	s.silentWorker.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.silentWorker.Done()
		s.forwardSilentRTP()
	}()
}

func (s *MediaSession) startPBXBridgeKeepalive() {
	s.startSilentRTP()
	s.mu.Lock()
	if s.externalQuietStarted || sessionClosed(s.closed) {
		s.mu.Unlock()
		return
	}
	s.externalQuietStarted = true
	if s.downlinkStop == nil {
		s.downlinkStop = make(chan struct{})
	}
	s.silentWorker.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.silentWorker.Done()
		s.forwardExternalSilence()
	}()
}

func (s *MediaSession) stopIMSSilence() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.uplinkStop == nil {
		return
	}
	select {
	case <-s.uplinkStop:
	default:
		close(s.uplinkStop)
	}
}

func (s *MediaSession) stopExternalSilence() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.downlinkStop == nil {
		return
	}
	select {
	case <-s.downlinkStop:
	default:
		close(s.downlinkStop)
	}
}

func (s *MediaSession) stopSilentRTP() {
	s.mu.Lock()
	close(s.closed)
	s.mu.Unlock()
	s.silentWorker.Wait()
}

func sessionClosed(closed <-chan struct{}) bool {
	select {
	case <-closed:
		return true
	default:
		return false
	}
}

func drainRTCP(sender *webrtc.RTPSender, done <-chan struct{}) {
	buffer := make([]byte, 1500)
	for {
		if _, _, err := sender.Read(buffer); err != nil {
			return
		}
		select {
		case <-done:
			return
		default:
		}
	}
}
