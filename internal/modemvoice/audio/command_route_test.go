package audio

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yibaiba/hideck/internal/modemvoice/media"
)

type recordingRunner struct {
	mu       sync.Mutex
	commands []Command
	startErr error
}

func (r *recordingRunner) Run(ctx context.Context, cmd Command) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, cmd)
	if len(cmd.Args) > 0 && cmd.Args[0] == "start" {
		return r.startErr
	}
	return nil
}

type routePCM struct{ closes atomic.Int32 }

func (*routePCM) ReadFrame() ([]int16, error) { return nil, io.EOF }
func (*routePCM) WriteFrame([]int16) error    { return nil }
func (p *routePCM) Close() error              { p.closes.Add(1); return nil }

func testRouteOptions(runner Runner) RouteOptions {
	return RouteOptions{Start: Command{Program: "/example/route", Args: []string{"start"}}, Stop: Command{Program: "/example/route", Args: []string{"stop"}}, Timeout: time.Second, Runner: runner,
		Resolve: func() (Endpoint, error) { return Endpoint{USBPath: "/sys/devices/usb/3-2.1", Card: 2}, nil },
		OpenPCM: func(context.Context, Endpoint) (media.PCM, error) { return &routePCM{}, nil }}
}

func TestRouteKeepsPCMContextUntilOwnershipEnds(t *testing.T) {
	runner := &recordingRunner{}
	options := testRouteOptions(runner)
	var openedCtx context.Context
	options.OpenPCM = func(ctx context.Context, _ Endpoint) (media.PCM, error) { openedCtx = ctx; return &routePCM{}, nil }
	route, err := NewCommandRoute(options)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := route.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := route.OpenPCM(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if openedCtx.Err() != nil {
		t.Fatal("open returned and canceled live audio")
	}
	if err := pcm.Close(); err != nil {
		t.Fatal(err)
	}
	if openedCtx.Err() == nil {
		t.Fatal("PCM context retained after close")
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 {
		t.Fatal("stop command not exactly once")
	}
}

func TestRouteStopWaitsForLatePCMAndCleansIt(t *testing.T) {
	runner := &recordingRunner{}
	options := testRouteOptions(runner)
	entered, release := make(chan context.Context, 1), make(chan struct{})
	pcm := &routePCM{}
	options.OpenPCM = func(ctx context.Context, _ Endpoint) (media.PCM, error) { entered <- ctx; <-release; return pcm, nil }
	route, err := NewCommandRoute(options)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := route.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	opened := make(chan error, 1)
	go func() { _, err := route.OpenPCM(context.Background()); opened <- err }()
	ctx := <-entered
	closed := make(chan error, 1)
	go func() { closed <- owner.Close() }()
	<-ctx.Done()
	select {
	case err := <-closed:
		t.Fatalf("route stopped before open completed: %v", err)
	default:
	}
	close(release)
	if err := <-opened; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if pcm.closes.Load() != 1 {
		t.Fatal("late PCM leaked")
	}
}

func TestRouteFailedStartStillHasCleanupOwner(t *testing.T) {
	failure := errors.New("start failed after changing route")
	runner := &recordingRunner{startErr: failure}
	route, err := NewCommandRoute(testRouteOptions(runner))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	owner, err := route.Start(ctx)
	if !errors.Is(err, failure) || owner == nil {
		t.Fatalf("%v %v", owner, err)
	}
	cancel()
	if err := owner.Close(); err != nil {
		t.Fatal("stop incorrectly inherited canceled context:", err)
	}
	if len(runner.commands) != 2 || runner.commands[1].Args[0] != "stop" {
		t.Fatal(runner.commands)
	}
}

func TestRouteResolvesCardAgainBeforeOpening(t *testing.T) {
	options := testRouteOptions(&recordingRunner{})
	card := 2
	options.Resolve = func() (Endpoint, error) { return Endpoint{USBPath: "/sys/devices/usb/3-2.1", Card: card}, nil }
	options.OpenPCM = func(_ context.Context, e Endpoint) (media.PCM, error) {
		if e.Card != 7 {
			t.Fatalf("stale card %d", e.Card)
		}
		return &routePCM{}, nil
	}
	route, err := NewCommandRoute(options)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := route.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	card = 7
	pcm, err := route.OpenPCM(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := pcm.Close(); err != nil {
		t.Fatal(err)
	}
}
