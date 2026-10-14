// Package media owns the modem-direct PCM/RTP boundary. It does not configure
// radio, USB interfaces or vendor audio routes.
package media

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

const (
	SampleRate    = 8000
	FrameSamples  = 160
	FrameDuration = 20 * time.Millisecond
)

// PCM exchanges complete S16 mono frames. Close must unblock both concurrent
// ReadFrame and WriteFrame calls. A short frame is a hardware/adapter failure.
type PCM interface {
	ReadFrame() ([]int16, error)
	WriteFrame([]int16) error
	Close() error
}

type Config struct {
	Conn       net.PacketConn
	Remote     netip.AddrPort
	PCM        PCM
	ListenOnly bool
}

type Stats struct {
	FromModem    uint64
	ToModem      uint64
	Muted        uint64
	RejectedPeer uint64
	OtherPayload uint64
	PlaybackPeak uint64
}

type Bridge struct {
	conn         net.PacketConn
	remote       netip.AddrPort
	remoteMu     sync.RWMutex
	pcm          PCM
	sendMu       sync.Mutex
	listenOnly   bool
	pending      []int16 // Protected by sendMu; never carried across mute changes.
	closed       chan struct{}
	done         chan struct{}
	stopOnce     sync.Once
	workers      sync.WaitGroup
	errMu        sync.Mutex
	err          error
	seq          uint16
	timestamp    uint32
	ssrc         uint32
	fromModem    atomic.Uint64
	toModem      atomic.Uint64
	muted        atomic.Uint64
	rejectedPeer atomic.Uint64
	otherPayload atomic.Uint64
	playbackPeak atomic.Uint64
}

// NewBridge transfers ownership of Conn and PCM only on success. Remote is
// the negotiated local WebRTC relay, never an address learned from traffic.
func NewBridge(ctx context.Context, cfg Config) (*Bridge, error) {
	if err := validateTransport(ctx, cfg.Conn, cfg.Remote); err != nil {
		return nil, err
	}
	if cfg.PCM == nil {
		return nil, errors.New("modem media: PCM is required")
	}
	var seed [10]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, fmt.Errorf("modem media: RTP identity: %w", err)
	}
	b := &Bridge{conn: cfg.Conn, remote: netip.AddrPortFrom(cfg.Remote.Addr().Unmap(), cfg.Remote.Port()), pcm: cfg.PCM,
		listenOnly: cfg.ListenOnly, closed: make(chan struct{}), done: make(chan struct{}),
		seq: binary.BigEndian.Uint16(seed[:2]), timestamp: binary.BigEndian.Uint32(seed[2:6]), ssrc: binary.BigEndian.Uint32(seed[6:])}
	b.workers.Add(2)
	go b.fromRelay()
	go b.fromPCM()
	go func() { b.workers.Wait(); b.stop(nil); close(b.done) }()
	go func() {
		select {
		case <-ctx.Done():
			b.stop(ctx.Err())
		case <-b.done:
		}
	}()
	return b, nil
}

func validateTransport(ctx context.Context, conn net.PacketConn, remote netip.AddrPort) error {
	if ctx == nil || conn == nil {
		return errors.New("modem media: context and RTP socket are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !remote.IsValid() || remote.Port() == 0 || !remote.Addr().IsLoopback() {
		return errors.New("modem media: remote must be the local phone relay")
	}
	return nil
}

// SetListenOnly waits for an in-flight playback write before returning. Future
// browser frames become silence; modem capture remains enabled in both modes.
func (b *Bridge) SetListenOnly(enabled bool) error {
	b.sendMu.Lock()
	defer b.sendMu.Unlock()
	select {
	case <-b.closed:
		return net.ErrClosed
	default:
	}
	if enabled != b.listenOnly {
		b.pending = nil
	}
	b.listenOnly = enabled
	return nil
}

func (b *Bridge) Stats() Stats {
	return Stats{FromModem: b.fromModem.Load(), ToModem: b.toModem.Load(), Muted: b.muted.Load(),
		RejectedPeer: b.rejectedPeer.Load(), OtherPayload: b.otherPayload.Load(), PlaybackPeak: b.playbackPeak.Load()}
}

// SetRemote accepts only a relay authorized by the phone control lease.
// No packet can change this endpoint; sends to the previous relay finish first.
func (b *Bridge) SetRemote(remote netip.AddrPort) error {
	if !remote.IsValid() || remote.Port() == 0 || !remote.Addr().IsLoopback() {
		return errors.New("modem media: remote must be the local phone relay")
	}
	b.remoteMu.Lock()
	defer b.remoteMu.Unlock()
	b.sendMu.Lock()
	defer b.sendMu.Unlock()
	select {
	case <-b.closed:
		return net.ErrClosed
	default:
	}
	b.pending = nil
	b.remote = netip.AddrPortFrom(remote.Addr().Unmap(), remote.Port())
	return nil
}

func (b *Bridge) Done() <-chan struct{} { return b.done }

func (b *Bridge) Err() error {
	b.errMu.Lock()
	defer b.errMu.Unlock()
	return b.err
}

func (b *Bridge) Close() error {
	b.stop(nil)
	<-b.done
	return b.Err()
}

func (b *Bridge) stop(cause error) {
	b.stopOnce.Do(func() {
		close(b.closed)
		err := errors.Join(cause, b.conn.Close(), b.pcm.Close())
		b.errMu.Lock()
		b.err = err
		b.errMu.Unlock()
	})
}
