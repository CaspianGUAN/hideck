package audio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/yibaiba/hideck/internal/modemvoice/media"
	"github.com/yibaiba/hideck/pkg/logger"
)

// ALSA runs the standard alsa-utils tools; it does not select an endpoint or
// authorize opening USB audio. The owner supplies a freshly resolved Endpoint.
type ALSA struct{ CaptureProgram, PlaybackProgram string }

func (a ALSA) Open(ctx context.Context, endpoint Endpoint) (media.PCM, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("audio: ALSA requires Linux")
	}
	if endpoint.Card < 0 || endpoint.Device < 0 || endpoint.USBPath == "" {
		return nil, errors.New("audio: invalid USB PCM endpoint")
	}
	args := alsaArgs(endpoint)
	return openPrograms(ctx, pcmPrograms{
		capture:  program{path: a.CaptureProgram, args: append([]string{"-C"}, args...)},
		playback: program{path: a.PlaybackProgram, args: append([]string{"-P"}, args...)},
	})
}

const alsaBufferFrames = 4

func alsaArgs(endpoint Endpoint) []string {
	return []string{"-q", "-N", "-D", endpoint.ALSADevice(), "-t", "raw", "-f", "S16_LE", "-r", strconv.Itoa(media.SampleRate), "-c", "1",
		"--period-size=" + strconv.Itoa(media.FrameSamples), "--buffer-size=" + strconv.Itoa(media.FrameSamples*alsaBufferFrames)}
}

type program struct {
	path string
	args []string
}
type pcmPrograms struct{ capture, playback program }

type programPCM struct {
	*streamPCM
	captureProcess, playbackProcess *pcmProcess
	captureReader, playbackWriter   *os.File
	captureLog, playbackLog         diagnosticBuffer
	cancel                          context.CancelFunc
	once                            sync.Once
	done                            chan struct{}
	err                             error
	playbackQueue                   *playbackQueue
}

func openPrograms(ctx context.Context, programs pcmPrograms) (pcm media.PCM, err error) {
	if ctx == nil {
		return nil, errors.New("audio: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, prg := range []program{programs.capture, programs.playback} {
		if !filepath.IsAbs(prg.path) {
			return nil, errors.New("audio: ALSA tools require absolute executable paths")
		}
		if _, err := exec.LookPath(prg.path); err != nil {
			return nil, fmt.Errorf("audio: ALSA tool unavailable: %w", err)
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	p := &programPCM{cancel: cancel, done: make(chan struct{})}
	defer func() {
		if err != nil {
			err = errors.Join(err, p.finish(nil))
		}
	}()
	if err = p.start(runCtx, programs); err != nil {
		return nil, err
	}
	p.streamPCM = &streamPCM{capture: p.captureReader, playback: p.playbackWriter, stop: func() error { return p.finish(nil) }}
	p.playbackQueue = newPlaybackQueue(p.done)
	p.playbackQueue.start(p.streamPCM.WriteFrame, func() (int, error) {
		return queuedPipeBytes(p.playbackWriter)
	}, func(err error) { p.finish(err) })
	go p.monitor(ctx)
	return p, nil
}

func (p *programPCM) start(ctx context.Context, programs pcmPrograms) error {
	captureReader, captureWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	p.captureReader = captureReader
	defer captureWriter.Close()
	playbackReader, playbackWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	p.playbackWriter = playbackWriter
	defer playbackReader.Close()
	capture := pcmCommand(ctx, programs.capture)
	capture.cmd.Stdout, capture.cmd.Stderr = captureWriter, &p.captureLog
	if err := capture.cmd.Start(); err != nil {
		return fmt.Errorf("audio: start capture: %w", err)
	}
	p.captureProcess = capture
	go capture.wait()
	playback := pcmCommand(ctx, programs.playback)
	playback.cmd.Stdin, playback.cmd.Stderr = playbackReader, &p.playbackLog
	if err := playback.cmd.Start(); err != nil {
		return fmt.Errorf("audio: start playback: %w", err)
	}
	p.playbackProcess = playback
	go playback.wait()
	return ctx.Err()
}

// WaitDelay also prevents inherited stderr pipes in a broken wrapper from
// retaining the command forever after cancellation. Helpers must not daemonize.
const processPipeWait = 2 * time.Second

type pcmProcess struct {
	cmd    *exec.Cmd
	done   chan struct{}
	err    error // Written before closing done; readers must wait for done.
	killed atomic.Bool
}

func pcmCommand(ctx context.Context, prg program) *pcmProcess {
	cmd := exec.CommandContext(ctx, prg.path, prg.args...)
	cmd.WaitDelay = processPipeWait
	p := &pcmProcess{cmd: cmd, done: make(chan struct{})}
	cmd.Cancel = func() error {
		err := cmd.Process.Kill()
		if err == nil {
			p.killed.Store(true)
		}
		return err
	}
	return p
}

func (p *pcmProcess) wait() {
	p.err = p.cmd.Wait()
	close(p.done)
}

func (p *programPCM) monitor(ctx context.Context) {
	select {
	case <-p.captureProcess.done:
		p.finish(processExit("capture", p.captureProcess.err, p.captureLog.String()))
	case <-p.playbackProcess.done:
		p.finish(processExit("playback", p.playbackProcess.err, p.playbackLog.String()))
	case <-ctx.Done():
		p.finish(ctx.Err())
	case <-p.done:
	}
}

func processExit(stage string, err error, diagnostic string) error {
	if err == nil {
		err = errors.New("process exited before call ended")
	}
	return fmt.Errorf("audio: %s: %w (%s)", stage, err, diagnostic)
}

func (p *programPCM) finish(cause error) error {
	p.once.Do(func() {
		p.cancel()
		if p.captureReader != nil {
			_ = p.captureReader.Close()
		}
		if p.playbackWriter != nil {
			_ = p.playbackWriter.Close()
		}
		p.err = errors.Join(cause, awaitStopped(p.captureProcess, "capture", &p.captureLog), awaitStopped(p.playbackProcess, "playback", &p.playbackLog))
		close(p.done)
		if p.playbackQueue != nil {
			logger.Info("声卡播放队列统计", "discarded_frames", p.playbackQueue.dropped.Load(), "silence_frames", p.playbackQueue.silence.Load())
		}
	})
	return p.err
}

func awaitStopped(p *pcmProcess, stage string, log *diagnosticBuffer) error {
	if p == nil {
		return nil
	}
	<-p.done
	err := p.err
	var exited *exec.ExitError
	if errors.Is(err, context.Canceled) {
		return nil
	}
	if p.killed.Load() && errors.As(err, &exited) {
		if status, ok := exited.Sys().(syscall.WaitStatus); ok && status.Signaled() && status.Signal() == syscall.SIGKILL {
			return nil
		}
	}
	if err != nil {
		return processExit(stage, err, log.String())
	}
	return nil
}

func (p *programPCM) ReadFrame() ([]int16, error) {
	frame, err := p.streamPCM.ReadFrame()
	if err != nil {
		return nil, errors.Join(err, p.finish(err))
	}
	return frame, nil
}

func (p *programPCM) WriteFrame(frame []int16) error {
	return p.playbackQueue.offer(frame)
}

func (p *programPCM) Close() error {
	err := p.finish(nil)
	if p.playbackQueue != nil {
		<-p.playbackQueue.stopped
	}
	return err
}
