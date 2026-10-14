package modem

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestFileUploadHandlesPartialWritesAndExactBinary(t *testing.T) {
	var written bytes.Buffer
	lines := []string{"CONNECT", "+QFUPL: 4,ff16", "OK"}
	wire := uploadIO{write: func(p []byte) (int, error) {
		if len(p) > 2 {
			p = p[:2]
		}
		return written.Write(p)
	}, line: func(time.Time) (string, error) {
		if len(lines) == 0 {
			return "", io.EOF
		}
		line := lines[0]
		lines = lines[1:]
		return line, nil
	}}
	data := []byte{0, 0x1b, 0xff, 0x0d}
	if err := uploadFile(wire, "RAM:hideck-test.mbn", data); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(written.Bytes(), data) {
		t.Fatalf("binary changed: %x", written.Bytes())
	}
}
func TestFileUploadRejectsTruncationAndMissingProof(t *testing.T) {
	for _, lines := range [][]string{{"CONNECT", "+QFUPL: 2,1234", "OK"}, {"OK"}, {"CONNECT", "OK"}, {"+CME ERROR: 407"}} {
		wire := uploadIO{write: func(p []byte) (int, error) { return len(p), nil }, line: func(time.Time) (string, error) {
			if len(lines) == 0 {
				return "", context.DeadlineExceeded
			}
			line := lines[0]
			lines = lines[1:]
			return line, nil
		}}
		if err := uploadFile(wire, "RAM:hideck-test.mbn", []byte("data")); err == nil {
			t.Fatal("incomplete upload accepted")
		}
	}
}
func TestFileUploadDisconnectIsUnsynchronized(t *testing.T) {
	wire := uploadIO{write: func(p []byte) (int, error) { return len(p), nil }, line: func(time.Time) (string, error) { return "", io.EOF }}
	if err := uploadFile(wire, "RAM:hideck-test.mbn", []byte("data")); !errors.Is(err, ErrUploadSynchronization) {
		t.Fatal(err)
	}
}
