package audio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPCMProgramHelper(t *testing.T) {
	args := os.Args
	if len(args) < 2 || args[len(args)-2] != "--pcm-helper" {
		return
	}
	switch args[len(args)-1] {
	case "capture":
		data := make([]byte, 320)
		for i := 0; i < len(data); i += 2 {
			data[i] = 0x34
			data[i+1] = 0x12
		}
		for {
			if _, err := os.Stdout.Write(data); err != nil {
				os.Exit(0)
			}
			time.Sleep(time.Millisecond)
		}
	case "playback":
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	case "playback-stalled":
		for {
			time.Sleep(time.Second)
		}
	case "fail":
		fmt.Fprintln(os.Stderr, "PCM device unavailable")
		os.Exit(7)
	default:
		os.Exit(8)
	}
}

func helperProgram(t *testing.T, mode string) program {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return program{path: path, args: []string{"-test.run=^TestPCMProgramHelper$", "-test.timeout=60s", "--", "--pcm-helper", mode}}
}

func TestALSAProcessesExchangeFramesAndStop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pcm, err := openPrograms(ctx, pcmPrograms{capture: helperProgram(t, "capture"), playback: helperProgram(t, "playback")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pcm.Close() })
	frame, err := pcm.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if len(frame) != 160 || frame[0] != 0x1234 || frame[159] != 0x1234 {
		t.Fatal("bad S16 frame")
	}
	if err := pcm.WriteFrame(frame); err != nil {
		t.Fatal(err)
	}
	if err := pcm.Close(); err != nil {
		t.Fatal(err)
	}
	p := pcm.(*programPCM)
	select {
	case <-p.captureProcess.done:
	default:
		t.Fatal("capture process leaked")
	}
	select {
	case <-p.playbackProcess.done:
	default:
		t.Fatal("playback process leaked")
	}
	if err := pcm.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestALSAProcessFailurePreservesDiagnostics(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pcm, err := openPrograms(ctx, pcmPrograms{capture: helperProgram(t, "fail"), playback: helperProgram(t, "playback")})
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := pcm.ReadFrame()
	err = errors.Join(readErr, pcm.Close())
	if err == nil || !strings.Contains(err.Error(), "exit status 7") || !strings.Contains(err.Error(), "PCM device unavailable") {
		t.Fatal(err)
	}
}

func TestALSAUsesExplicitRawFormatAndNoShell(t *testing.T) {
	want := []string{"-q", "-N", "-D", "hw:4,1", "-t", "raw", "-f", "S16_LE", "-r", "8000", "-c", "1", "--period-size=160", "--buffer-size=640"}
	if got := alsaArgs(Endpoint{Card: 4, Device: 1}); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	if _, err := openPrograms(context.Background(), pcmPrograms{capture: program{path: "arecord;touch /tmp/x"}}); err == nil {
		t.Fatal("relative/shell command accepted")
	}
}
