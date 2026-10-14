// modem-voice-probe exercises the new modemvoice libraries against real
// hardware. It is not a replacement for the unfinished phone UI/backend.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/yibaiba/hideck/internal/modemvoice"
	"github.com/yibaiba/hideck/internal/modemvoice/audio"
)

var build = "development"

type options struct {
	server, device, iccid, number, usb, recording string
	runtimeDir, adbProgram, adbState              string
	pcm, runtimeOnly                              bool
}

func main() {
	var o options
	flag.StringVar(&o.server, "server", "http://127.0.0.1:7575", "local HiDeck API")
	flag.StringVar(&o.device, "device", "", "explicit device ID")
	flag.StringVar(&o.iccid, "iccid", "", "expected SIM ICCID; checked before dialing")
	flag.StringVar(&o.number, "number", "", "authorized test destination")
	flag.StringVar(&o.usb, "usb", "", "expected USB path, checked against device overview")
	flag.BoolVar(&o.pcm, "pcm", false, "explicit hardware experiment: open USB audio; may disrupt same-device QMI")
	flag.StringVar(&o.recording, "recording", "", "new raw S16_LE/8000/mono capture file, required with -pcm")
	flag.StringVar(&o.runtimeDir, "qdc507-runtime-dir", "", "explicitly prepare the pinned QDC507 runtime from this local directory")
	flag.StringVar(&o.adbProgram, "adb", "/usr/bin/adb", "absolute host ADB executable")
	flag.StringVar(&o.adbState, "adb-state", "", "absolute private state directory for the isolated ADB server")
	flag.BoolVar(&o.runtimeOnly, "runtime-only", false, "prepare, start and stop QDC507 routing without dialing or opening host PCM")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, o); err != nil {
		fmt.Fprintln(os.Stderr, "FAILED:", err)
		os.Exit(1)
	}
}

func run(parent context.Context, o options) (err error) {
	if o.device == "" || o.iccid == "" || (o.number == "" && !o.runtimeOnly) || o.usb == "" {
		return errors.New("device, ICCID, USB path and authorized destination are required")
	}
	if o.pcm && o.recording == "" {
		return errors.New("-pcm requires a new -recording path")
	}
	if o.runtimeOnly && o.runtimeDir == "" {
		return errors.New("-runtime-only requires -qdc507-runtime-dir")
	}
	if o.runtimeDir != "" && ((!o.pcm && !o.runtimeOnly) || o.adbState == "") {
		return errors.New("QDC507 runtime preparation requires -pcm or -runtime-only, and -adb-state")
	}
	api, err := newAPI(o, &http.Client{Timeout: 65 * time.Second})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	if err := api.checkDevice(ctx, o); err != nil {
		return err
	}
	client, err := modemvoice.NewClient(api)
	if err != nil {
		return err
	}
	cleanup, err := prepareRuntime(ctx, runtimeProbe{options: o, api: api, client: client})
	if cleanup != nil {
		defer func() { err = errors.Join(err, cleanup()) }()
	}
	if err != nil {
		return err
	}
	if o.runtimeOnly {
		fmt.Println("RUNTIME: preparation and route start succeeded; verifying cleanup next")
		return nil
	}
	fmt.Printf("build=%s device=%s destination=%s PCM=%v\n", build, o.device, o.number, o.pcm)
	return testCall(ctx, callProbe{client: client, options: o})
}

type callProbe struct {
	client  *modemvoice.Client
	options options
}

func testCall(ctx context.Context, p callProbe) (err error) {
	before, err := p.client.Calls(ctx)
	if err != nil {
		return err
	}
	for _, c := range before {
		if c.Mode != 1 && c.Mode != 2 {
			return errors.New("existing voice/unknown call; test not started")
		}
	}
	// Even a failed dial may have reached the modem. Cleanup uses a fresh
	// snapshot and the library's identity checks, never unconditionally ATH.
	defer func() { err = errors.Join(err, p.cleanup(before)) }()
	if err := p.client.Dial(ctx, p.options.number); err != nil {
		return fmt.Errorf("new client dial: %w", err)
	}
	if err := p.waitActive(ctx); err != nil {
		return err
	}
	if p.options.pcm {
		return p.testPCM(ctx)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return nil
	}
}

func (p callProbe) waitActive(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for real CLCC active state: %w", ctx.Err())
		case <-ticker.C:
			calls, err := p.client.Calls(ctx)
			if err != nil {
				return err
			}
			for _, c := range calls {
				if p.owns(c) && c.State == modemvoice.Active {
					fmt.Printf("%s CONNECTED: CLCC id=%d voice active\n", time.Now().Format(time.RFC3339), c.Index)
					return nil
				}
			}
		}
	}
}

func (p callProbe) owns(c modemvoice.Call) bool {
	return c.Mode == 0 && !c.Inbound && c.Number == p.options.number
}

func (p callProbe) cleanup(before []modemvoice.Call) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	calls, err := p.client.Calls(ctx)
	if err != nil {
		return fmt.Errorf("cleanup cannot inspect calls: %w", err)
	}
	for _, c := range calls {
		if p.owns(c) {
			if err := p.client.Hangup(ctx, c); err != nil {
				return fmt.Errorf("cleanup hangup failed: %w", err)
			}
		}
	}
	calls, err = p.client.Calls(ctx)
	if err != nil {
		return err
	}
	for _, c := range calls {
		if p.owns(c) {
			return errors.New("test voice call remains after hangup")
		}
	}
	for _, original := range before {
		if !containsOriginal(calls, original) {
			return fmt.Errorf("preexisting data/fax call %d changed during test", original.Index)
		}
	}
	fmt.Println("CLEANUP: test voice absent; preexisting data/fax calls preserved")
	return nil
}

func containsOriginal(calls []modemvoice.Call, original modemvoice.Call) bool {
	for _, c := range calls {
		if c == original {
			return true
		}
	}
	return false
}

func (p callProbe) testPCM(ctx context.Context) error {
	endpoint, err := audio.ResolveUSBPCM(p.options.usb)
	if err != nil {
		return err
	}
	fmt.Printf("PCM endpoint=%s USB=%s; existing firmware route, no QPCMV/USB reconfiguration\n", endpoint.ALSADevice(), endpoint.USBPath)
	f, err := os.OpenFile(p.options.recording, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	alsa := audio.ALSA{CaptureProgram: "/usr/bin/arecord", PlaybackProgram: "/usr/bin/aplay"}
	pcm, err := alsa.Open(ctx, endpoint)
	if err != nil {
		return err
	}
	stats, err := samplePCM(ctx, pcmSample{pcm: pcm, output: f})
	fmt.Printf("PCM captured_frames=%d nonzero_samples=%d peak=%d silence_playback_frames=%d\n", stats.frames, stats.nonzero, stats.peak, stats.writes)
	err = errors.Join(err, f.Sync())
	if err == nil && stats.nonzero == 0 {
		return errors.New("PCM capture contained only zeros; real call audio is not proven")
	}
	if err == nil {
		fmt.Println("Captured nonzero PCM; listen to recording to verify speech. Microphone/browser path NOT tested.")
	}
	return err
}

func validateDeviceID(id string) bool {
	return id != "" && !strings.ContainsAny(id, "/\\?#%\r\n")
}
