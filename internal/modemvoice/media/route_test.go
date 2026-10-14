package media

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type routeOwner struct {
	closes atomic.Int32
	err    error
}

func (o *routeOwner) Close() error { o.closes.Add(1); return o.err }

type testRoute struct {
	owner       *routeOwner
	pcm         PCM
	startErr    error
	openErr     error
	openEntered chan struct{}
	allowOpen   chan struct{}
	openCalls   atomic.Int32
}

func (r *testRoute) Start(context.Context) (io.Closer, error) { return r.owner, r.startErr }
func (r *testRoute) OpenPCM(context.Context) (PCM, error) {
	r.openCalls.Add(1)
	if r.openEntered != nil {
		close(r.openEntered)
		<-r.allowOpen
	}
	return r.pcm, r.openErr
}

func requestForRoute(t *testing.T, route AudioRoute) OpenRequest {
	t.Helper()
	conn, relay := udpSocket(t), udpSocket(t)
	return OpenRequest{Conn: conn, Remote: relay.LocalAddr().(*net.UDPAddr).AddrPort(), Route: route}
}

func TestRouteFailuresReleasePartiallyAcquiredResources(t *testing.T) {
	for _, phase := range []string{"start", "pcm", "nil_pcm"} {
		t.Run(phase, func(t *testing.T) {
			failure, closeFailure := errors.New("adapter failed"), errors.New("cleanup failed")
			pcm := newTestPCM()
			route := &testRoute{owner: &routeOwner{err: closeFailure}, pcm: pcm}
			switch phase {
			case "start":
				route.startErr = failure
			case "pcm":
				route.openErr = failure
			case "nil_pcm":
				route.pcm = nil
			}
			req := requestForRoute(t, route)
			b, err := Open(context.Background(), req)
			if b != nil || err == nil || !errors.Is(err, closeFailure) {
				t.Fatalf("%v %v", b, err)
			}
			if phase != "nil_pcm" && !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if route.owner.closes.Load() != 1 {
				t.Fatal("route not released once")
			}
			if phase == "start" && route.openCalls.Load() != 0 {
				t.Fatal("opened PCM after route failed")
			}
			if phase == "pcm" && pcm.closes.Load() != 1 {
				t.Fatal("partial PCM leaked")
			}
			if _, err := req.Conn.WriteTo([]byte{0}, net.UDPAddrFromAddrPort(req.Remote)); !errors.Is(err, net.ErrClosed) {
				t.Fatalf("RTP socket leaked: %v", err)
			}
		})
	}
}

func TestCanceledLatePCMOpenReleasesRouteAndPCM(t *testing.T) {
	pcm := newTestPCM()
	route := &testRoute{owner: &routeOwner{}, pcm: pcm, openEntered: make(chan struct{}), allowOpen: make(chan struct{})}
	req := requestForRoute(t, route)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := Open(ctx, req); done <- err }()
	<-route.openEntered
	cancel()
	close(route.allowOpen)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if pcm.closes.Load() != 1 || route.owner.closes.Load() != 1 {
		t.Fatal("late PCM or route leaked")
	}
}

func TestBridgeOwnsRouteUntilClosed(t *testing.T) {
	pcm := newTestPCM()
	route := &testRoute{owner: &routeOwner{}, pcm: pcm}
	b, err := Open(context.Background(), requestForRoute(t, route))
	if err != nil {
		t.Fatal(err)
	}
	if route.owner.closes.Load() != 0 {
		t.Fatal("route closed while media active")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.Done():
	case <-time.After(time.Second):
		t.Fatal("workers not joined")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if pcm.closes.Load() != 1 || route.owner.closes.Load() != 1 {
		t.Fatal("resource closed more than once")
	}
}
