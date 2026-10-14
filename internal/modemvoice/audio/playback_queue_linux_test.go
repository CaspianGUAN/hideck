package audio

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

func TestPlaybackQueueBoundsBurstAndKeepsRecentSpeech(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close(); writer.Close() })
	done := make(chan struct{})
	stream := &streamPCM{playback: writer}
	failures := make(chan error, 1)
	q := newPlaybackQueue(done)
	q.start(stream.WriteFrame, func() (int, error) { return queuedPipeBytes(writer) }, func(err error) { failures <- err })
	t.Cleanup(func() { close(done); <-q.stopped })
	frame := make([]int16, 160)
	// Fill the small pipe budget before simulating a delayed RTP burst.
	for i := 0; i < playbackQueueFrames; i++ {
		if err := q.offer(frame); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(time.Second)
	for {
		n, err := queuedPipeBytes(writer)
		if err != nil {
			t.Fatal(err)
		}
		if n >= playbackPipeBytes {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("playback never reached pipe budget")
		}
		time.Sleep(time.Millisecond)
	}
	for i := 1; i <= 100; i++ {
		for j := range frame {
			frame[j] = int16(i)
		}
		if err := q.offer(frame); err != nil {
			t.Fatal(err)
		}
	}
	// Caller mutation must not corrupt the asynchronously accepted frame.
	for j := range frame {
		frame[j] = -1
	}
	if q.dropped.Load() < 95 {
		t.Fatalf("retained historical burst: dropped=%d", q.dropped.Load())
	}
	n, err := queuedPipeBytes(writer)
	if err != nil || n > playbackPipeBytes {
		t.Fatalf("pipe bytes=%d err=%v", n, err)
	}
	if err := reader.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	gotLatest := false
	for i := 0; i < 6; i++ {
		data := make([]byte, 320)
		if _, err := io.ReadFull(reader, data); err != nil {
			t.Fatal(err)
		}
		if int16(binary.LittleEndian.Uint16(data)) == 100 {
			gotLatest = true
			break
		}
	}
	if !gotLatest {
		t.Fatal("fresh speech remained behind old frames")
	}
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
}

func TestPlaybackQueueCloseInterruptsStalledConsumer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pcm, err := openPrograms(ctx, pcmPrograms{capture: helperProgram(t, "capture"), playback: helperProgram(t, "playback-stalled")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pcm.Close() })
	for i := 0; i < 200; i++ {
		if err := pcm.WriteFrame(make([]int16, 160)); err != nil {
			t.Fatal(err)
		}
	}
	closed := make(chan error, 1)
	go func() { closed <- pcm.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stalled playback prevented close")
	}
	if err := pcm.WriteFrame(make([]int16, 160)); !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
}

func TestPlaybackQueuePadsMissingPacketsWithSilence(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close(); writer.Close() })
	done := make(chan struct{})
	stream := &streamPCM{playback: writer}
	q := newPlaybackQueue(done)
	q.start(stream.WriteFrame, func() (int, error) { return queuedPipeBytes(writer) }, func(err error) { t.Error(err) })
	t.Cleanup(func() { close(done); <-q.stopped })
	if err := reader.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		frame := make([]byte, 320)
		if _, err := io.ReadFull(reader, frame); err != nil {
			t.Fatal(err)
		}
		for _, sample := range frame {
			if sample != 0 {
				t.Fatal("non-silent padding")
			}
		}
	}
	if q.silence.Load() < 5 {
		t.Fatal("missing packets did not keep playback running")
	}
}
