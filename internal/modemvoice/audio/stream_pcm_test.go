package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"testing"
)

type fragmentedWriter struct {
	bytes.Buffer
	max int
}

func (w *fragmentedWriter) Write(p []byte) (int, error) {
	if len(p) > w.max {
		p = p[:w.max]
	}
	return w.Buffer.Write(p)
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }

func TestStreamPCMHandlesPartialFramesWithoutPaddingOrLoss(t *testing.T) {
	data := make([]byte, 320)
	want := make([]int16, 160)
	for i := range want {
		want[i] = int16(i*101 - 8000)
		binary.LittleEndian.PutUint16(data[i*2:], uint16(want[i]))
	}
	w := &fragmentedWriter{max: 13}
	p := &streamPCM{capture: bytes.NewReader(data), playback: w}
	got, err := p.ReadFrame()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%v %v", got, err)
	}
	if err := p.WriteFrame(got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, w.Bytes()) {
		t.Fatal("short writes lost samples")
	}
	p.capture = bytes.NewReader(data[:319])
	if f, err := p.ReadFrame(); f != nil || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short read padded: %v %v", f, err)
	}
	p.playback = zeroWriter{}
	if err := p.WriteFrame(want); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
}
