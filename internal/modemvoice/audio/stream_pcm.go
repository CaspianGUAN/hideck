package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/yibaiba/hideck/internal/modemvoice/media"
)

// streamPCM adapts raw S16_LE streams without padding incomplete reads or
// losing short writes. stop must close both streams to interrupt blocked I/O.
type streamPCM struct {
	capture  io.Reader
	playback io.Writer
	stop     func() error
	readMu   sync.Mutex
	writeMu  sync.Mutex
}

func (p *streamPCM) ReadFrame() ([]int16, error) {
	p.readMu.Lock()
	defer p.readMu.Unlock()
	data := make([]byte, media.FrameSamples*2)
	if _, err := io.ReadFull(p.capture, data); err != nil {
		return nil, fmt.Errorf("audio: capture frame: %w", err)
	}
	frame := make([]int16, media.FrameSamples)
	for i := range frame {
		frame[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
	}
	return frame, nil
}

func (p *streamPCM) WriteFrame(frame []int16) error {
	if len(frame) != media.FrameSamples {
		return errors.New("audio: playback requires a complete 160-sample frame")
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	data := make([]byte, len(frame)*2)
	for i, sample := range frame {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(sample))
	}
	for len(data) > 0 {
		n, err := p.playback.Write(data)
		if err != nil {
			return fmt.Errorf("audio: playback frame: %w", err)
		}
		if n <= 0 || n > len(data) {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func (p *streamPCM) Close() error { return p.stop() }
