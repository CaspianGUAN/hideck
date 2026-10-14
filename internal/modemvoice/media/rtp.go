package media

import (
	"fmt"
	"io"
	"net"
	"net/netip"
	"time"

	"github.com/pion/rtp"
	"github.com/yibaiba/hideck/pkg/g711"
)

const (
	pcmuPayload  = 0
	maxUDPPacket = 65535
)

func (b *Bridge) fromRelay() {
	defer b.workers.Done()
	buffer := make([]byte, maxUDPPacket)
	for {
		n, from, err := b.conn.ReadFrom(buffer)
		if err != nil {
			b.stop(fmt.Errorf("modem media: read RTP: %w", err))
			return
		}
		b.remoteMu.RLock()
		matches := sameEndpoint(from, b.remote)
		b.remoteMu.RUnlock()
		if !matches {
			b.rejectedPeer.Add(1)
			continue
		}
		var packet rtp.Packet
		if err := packet.Unmarshal(buffer[:n]); err != nil {
			b.stop(fmt.Errorf("modem media: invalid RTP: %w", err))
			return
		}
		if packet.Version != 2 {
			b.stop(fmt.Errorf("modem media: RTP version %d", packet.Version))
			return
		}
		if packet.PayloadType != pcmuPayload {
			b.otherPayload.Add(1)
			continue
		}
		b.remoteMu.RLock()
		if !sameEndpoint(from, b.remote) {
			b.remoteMu.RUnlock()
			continue
		}
		err = b.consumePCMU(packet.Payload)
		b.remoteMu.RUnlock()
		if err != nil {
			b.stop(err)
			return
		}
	}
}

func sameEndpoint(from net.Addr, want netip.AddrPort) bool {
	addr, ok := from.(*net.UDPAddr)
	if !ok {
		return false
	}
	got := addr.AddrPort()
	return got.Port() == want.Port() && got.Addr().Unmap() == want.Addr()
}

func (b *Bridge) consumePCMU(payload []byte) error {
	b.sendMu.Lock()
	defer b.sendMu.Unlock()
	b.pending = append(b.pending, g711.Decode(payload)...)
	for len(b.pending) >= FrameSamples {
		if err := b.writePCM(b.pending[:FrameSamples]); err != nil {
			return err
		}
		b.pending = b.pending[FrameSamples:]
	}
	return nil
}

// Called with sendMu held, including while the adapter writes the frame.
func (b *Bridge) writePCM(samples []int16) error {
	select {
	case <-b.closed:
		return net.ErrClosed
	default:
	}
	frame := samples
	if b.listenOnly {
		frame = make([]int16, FrameSamples)
	}
	if err := b.pcm.WriteFrame(frame); err != nil {
		return fmt.Errorf("modem media: write PCM: %w", err)
	}
	for _, sample := range frame {
		magnitude := int64(sample)
		if magnitude < 0 {
			magnitude = -magnitude
		}
		for old := b.playbackPeak.Load(); uint64(magnitude) > old; old = b.playbackPeak.Load() {
			if b.playbackPeak.CompareAndSwap(old, uint64(magnitude)) {
				break
			}
		}
	}
	if b.listenOnly {
		b.muted.Add(1)
	} else {
		b.toModem.Add(1)
	}
	return nil
}

func (b *Bridge) fromPCM() {
	defer b.workers.Done()
	ticker := time.NewTicker(FrameDuration)
	defer ticker.Stop()
	for {
		select {
		case <-b.closed:
			return
		case <-ticker.C:
		}
		frame, err := b.pcm.ReadFrame()
		if err != nil {
			b.stop(fmt.Errorf("modem media: read PCM: %w", err))
			return
		}
		if len(frame) != FrameSamples {
			b.stop(fmt.Errorf("modem media: short PCM frame: got %d samples, want %d", len(frame), FrameSamples))
			return
		}
		packet := rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: pcmuPayload,
			SequenceNumber: b.seq, Timestamp: b.timestamp, SSRC: b.ssrc}, Payload: g711.Encode(frame)}
		data, err := packet.Marshal()
		if err != nil {
			b.stop(fmt.Errorf("modem media: encode RTP: %w", err))
			return
		}
		b.remoteMu.RLock()
		n, err := b.conn.WriteTo(data, net.UDPAddrFromAddrPort(b.remote))
		b.remoteMu.RUnlock()
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
		if err != nil {
			b.stop(fmt.Errorf("modem media: write RTP: %w", err))
			return
		}
		b.seq++
		b.timestamp += FrameSamples
		b.fromModem.Add(1)
	}
}
