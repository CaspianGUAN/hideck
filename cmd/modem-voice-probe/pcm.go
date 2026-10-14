package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/yibaiba/hideck/internal/modemvoice/media"
)

type pcmSample struct {
	pcm    media.PCM
	output io.Writer
}

type pcmStats struct {
	frames, nonzero, writes int
	peak                    int32
}

type captureResult struct {
	stats pcmStats
	err   error
}

// Sends deliberate silence, never a synthetic capture. This test cannot prove
// microphone uplink or that nonzero capture samples contain intelligible speech.
func samplePCM(ctx context.Context, p pcmSample) (pcmStats, error) {
	var stopping atomic.Bool
	captured := make(chan captureResult, 1)
	played := make(chan captureResult, 1)
	go func() { captured <- p.capture(&stopping) }()
	go func() { played <- p.play(ctx, &stopping) }()
	var c, w captureResult
	var gotCapture, gotPlayback bool
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case <-time.After(5 * time.Second):
	case c = <-captured:
		gotCapture = true
		err = errors.New("capture ended early")
	case w = <-played:
		gotPlayback = true
		err = errors.New("playback ended early")
	}
	stopping.Store(true)
	err = errors.Join(err, p.pcm.Close())
	if !gotCapture {
		c = <-captured
	}
	if !gotPlayback {
		w = <-played
	}
	c.stats.writes = w.stats.writes
	if c.stats.frames == 0 || w.stats.writes == 0 {
		err = errors.Join(err, errors.New("PCM did not exchange any complete frames"))
	}
	return c.stats, errors.Join(err, c.err, w.err)
}

func (p pcmSample) capture(stopping *atomic.Bool) captureResult {
	var result captureResult
	for {
		frame, err := p.pcm.ReadFrame()
		if err != nil {
			if !stopping.Load() {
				result.err = err
			}
			return result
		}
		data := make([]byte, 2*len(frame))
		for i, sample := range frame {
			binary.LittleEndian.PutUint16(data[2*i:], uint16(sample))
			if sample != 0 {
				result.stats.nonzero++
			}
			absolute := int32(sample)
			if absolute < 0 {
				absolute = -absolute
			}
			if absolute > result.stats.peak {
				result.stats.peak = absolute
			}
		}
		n, err := p.output.Write(data)
		if err != nil || n != len(data) {
			result.err = errors.Join(err, fmt.Errorf("capture file write %d/%d bytes", n, len(data)))
			return result
		}
		result.stats.frames++
	}
}

func (p pcmSample) play(ctx context.Context, stopping *atomic.Bool) captureResult {
	var result captureResult
	ticker := time.NewTicker(media.FrameDuration)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			result.err = ctx.Err()
			return result
		case <-ticker.C:
		}
		if stopping.Load() {
			return result
		}
		if err := p.pcm.WriteFrame(make([]int16, media.FrameSamples)); err != nil {
			if !stopping.Load() {
				result.err = err
			}
			return result
		}
		result.stats.writes++
	}
}
