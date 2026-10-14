package audio

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yibaiba/hideck/internal/modemvoice/media"
)

// RTP can arrive in a burst after USB startup or a network stall. A normal
// OS pipe retains seconds of that burst while ALSA consumes only 20ms per
// frame. Keep two frames in the pipe and two waiting here; retain recent
// speech rather than replaying the entire historical backlog.
const playbackQueueFrames = 2
const playbackPipeBytes = media.FrameSamples * 2 * playbackQueueFrames

type playbackQueue struct {
	frames  chan []int16
	done    <-chan struct{}
	stopped chan struct{}
	mu      sync.Mutex
	dropped atomic.Uint64
	silence atomic.Uint64
}

func newPlaybackQueue(done <-chan struct{}) *playbackQueue {
	return &playbackQueue{frames: make(chan []int16, playbackQueueFrames), done: done, stopped: make(chan struct{})}
}

func (q *playbackQueue) start(write func([]int16) error, queued func() (int, error), fail func(error)) {
	done := q.done
	go func() {
		defer close(q.stopped)
		ticker := time.NewTicker(media.FrameDuration)
		defer ticker.Stop()
		silence := make([]int16, media.FrameSamples)
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
			}
			n, err := queued()
			if err != nil {
				select {
				case <-done:
					return
				default:
				}
				fail(err)
				return
			}
			if n >= playbackPipeBytes {
				continue
			}
			frame := silence
			select {
			case frame = <-q.frames:
			default:
				q.silence.Add(1)
			}
			select {
			case <-done:
				return
			default:
			}
			if err := write(frame); err != nil {
				fail(err)
				return
			}
		}
	}()
}

func (q *playbackQueue) offer(frame []int16) error {
	if len(frame) != media.FrameSamples {
		return errors.New("audio: playback requires a complete 160-sample frame")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	select {
	case <-q.done:
		return net.ErrClosed
	default:
	}
	frame = append([]int16(nil), frame...)
	for {
		select {
		case <-q.done:
			return net.ErrClosed
		case q.frames <- frame:
			return nil
		default:
		}
		select {
		case <-q.frames:
			q.dropped.Add(1)
		default: // The worker took a frame; retry the enqueue.
		}
	}
}
