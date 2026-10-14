package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/yibaiba/hideck/pkg/g711"
)

type captureFrame struct {
	samples []int16
	err     error
}

type testPCM struct {
	capture  chan captureFrame
	playback chan []int16
	closed   chan struct{}
	once     sync.Once
	closes   atomic.Int32
	writeErr error
	closeErr error
}

func newTestPCM() *testPCM {
	return &testPCM{capture: make(chan captureFrame, 4), playback: make(chan []int16, 4), closed: make(chan struct{})}
}

func (p *testPCM) ReadFrame() ([]int16, error) {
	select {
	case <-p.closed:
		return nil, net.ErrClosed
	case f := <-p.capture:
		return f.samples, f.err
	}
}

func (p *testPCM) WriteFrame(f []int16) error {
	if p.writeErr != nil {
		return p.writeErr
	}
	select {
	case <-p.closed:
		return net.ErrClosed
	case p.playback <- append([]int16(nil), f...):
		return nil
	}
}

func (p *testPCM) Close() error {
	p.once.Do(func() { p.closes.Add(1); close(p.closed) })
	return p.closeErr
}

func udpSocket(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func tone(value int16) []int16 {
	out := make([]int16, FrameSamples)
	for i := range out {
		out[i] = value
	}
	return out
}

func testBridge(t *testing.T, pcm PCM, listen bool) (*Bridge, *net.UDPConn, *net.UDPConn) {
	t.Helper()
	conn, relay := udpSocket(t), udpSocket(t)
	b, err := NewBridge(context.Background(), Config{Conn: conn, PCM: pcm, Remote: relay.LocalAddr().(*net.UDPAddr).AddrPort(), ListenOnly: listen})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b, conn, relay
}

func sendAudio(t *testing.T, from, to *net.UDPConn, samples []int16) {
	t.Helper()
	p := rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 0}, Payload: g711.Encode(samples)}
	data, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := from.WriteTo(data, to.LocalAddr()); err != nil {
		t.Fatal(err)
	}
}

func receiveAudio(t *testing.T, relay *net.UDPConn) []byte {
	t.Helper()
	if err := relay.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 2048)
	n, _, err := relay.ReadFrom(data)
	if err != nil {
		t.Fatal(err)
	}
	var packet rtp.Packet
	if err := packet.Unmarshal(data[:n]); err != nil {
		t.Fatal(err)
	}
	return packet.Payload
}

func TestBridgeRebindRejectsOldRelayAndPreservesCapture(t *testing.T) {
	pcm := newTestPCM()
	b, conn, oldRelay := testBridge(t, pcm, false)
	newRelay := udpSocket(t)
	if err := b.SetRemote(newRelay.LocalAddr().(*net.UDPAddr).AddrPort()); err != nil {
		t.Fatal(err)
	}
	sendAudio(t, oldRelay, conn, tone(1000))
	sendAudio(t, newRelay, conn, tone(7000))
	select {
	case got := <-pcm.playback:
		if !reflect.DeepEqual(got, g711.Decode(g711.Encode(tone(7000)))) {
			t.Fatal("old relay injected audio")
		}
	case <-time.After(time.Second):
		t.Fatal("new relay audio missing")
	}
	pcm.capture <- captureFrame{samples: tone(3000)}
	if got := receiveAudio(t, newRelay); !bytes.Equal(got, g711.Encode(tone(3000))) {
		t.Fatal("capture did not follow relay")
	}
}

func TestBridgeDirectionsAndListenOnly(t *testing.T) {
	for _, listen := range []bool{false, true} {
		t.Run(map[bool]string{false: "duplex", true: "listen_only"}[listen], func(t *testing.T) {
			pcm := newTestPCM()
			b, conn, relay := testBridge(t, pcm, listen)
			pcm.capture <- captureFrame{samples: tone(3000)}
			sendAudio(t, relay, conn, tone(7000))
			if got := receiveAudio(t, relay); !bytes.Equal(got, g711.Encode(tone(3000))) {
				t.Fatal("modem capture was silenced or replaced")
			}
			want := g711.Decode(g711.Encode(tone(7000)))
			if listen {
				want = tone(0)
			}
			select {
			case written := <-pcm.playback:
				if !reflect.DeepEqual(written, want) {
					t.Fatal("wrong browser-to-modem audio")
				}
			case <-time.After(time.Second):
				t.Fatal("no modem playback")
			}
			if err := b.Close(); err != nil {
				t.Fatal(err)
			}
			if pcm.closes.Load() != 1 {
				t.Fatal("PCM was not released once")
			}
		})
	}
}

func TestBridgeMuteTransitionDropsPartialMicrophoneFrame(t *testing.T) {
	pcm := newTestPCM()
	b, _, _ := testBridge(t, pcm, false)
	// Drive the same packet-consumer entry point synchronously to fix the exact
	// half-frame/mute/unmute ordering without scheduler sleeps.
	if err := b.consumePCMU(g711.Encode(tone(4000)[:80])); err != nil {
		t.Fatal(err)
	}
	if err := b.SetListenOnly(true); err != nil {
		t.Fatal(err)
	}
	if err := b.consumePCMU(g711.Encode(tone(5000)[:80])); err != nil {
		t.Fatal(err)
	}
	if err := b.SetListenOnly(false); err != nil {
		t.Fatal(err)
	}
	if err := b.consumePCMU(g711.Encode(tone(6000))); err != nil {
		t.Fatal(err)
	}
	if got := <-pcm.playback; !reflect.DeepEqual(got, g711.Decode(g711.Encode(tone(6000)))) {
		t.Fatal("mute transition leaked buffered audio")
	}
}

func TestBridgePCMFailureClosesBothDirections(t *testing.T) {
	for _, frame := range []captureFrame{{err: io.ErrUnexpectedEOF}, {samples: []int16{123}}} {
		pcm := newTestPCM()
		b, _, _ := testBridge(t, pcm, false)
		pcm.capture <- frame
		select {
		case <-b.Done():
		case <-time.After(time.Second):
			t.Fatal("failed media remained active")
		}
		if b.Err() == nil || b.Close() == nil || pcm.closes.Load() != 1 {
			t.Fatal("PCM failure hidden")
		}
	}
}

func TestBridgePlaybackFailureAndCloseErrorRemainVisible(t *testing.T) {
	pcm := newTestPCM()
	pcm.writeErr, pcm.closeErr = io.ErrShortWrite, errors.New("route cleanup failed")
	b, conn, relay := testBridge(t, pcm, false)
	sendAudio(t, relay, conn, tone(1))
	select {
	case <-b.Done():
	case <-time.After(time.Second):
		t.Fatal("playback failure not surfaced")
	}
	if !errors.Is(b.Err(), io.ErrShortWrite) || !errors.Is(b.Close(), pcm.closeErr) {
		t.Fatal(b.Err())
	}
}

func TestBridgeCanceledContextClosesBlockedCapture(t *testing.T) {
	pcm := newTestPCM()
	conn, relay := udpSocket(t), udpSocket(t)
	ctx, cancel := context.WithCancel(context.Background())
	b, err := NewBridge(ctx, Config{Conn: conn, PCM: pcm, Remote: relay.LocalAddr().(*net.UDPAddr).AddrPort()})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-b.Done():
	case <-time.After(time.Second):
		t.Fatal("canceled bridge leaked workers")
	}
	if !errors.Is(b.Close(), context.Canceled) {
		t.Fatal(b.Err())
	}
}
