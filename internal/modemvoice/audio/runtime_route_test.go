package audio

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/yibaiba/hideck/internal/modemvoice/media"
)

type closeFunc func() error

func (f closeFunc) Close() error { return f() }

func TestRuntimeRouteClosesPCMBeforeRouteAndRetriesCleanup(t *testing.T) {
	pcm := &routePCM{}
	cleanupFailure := errors.New("USB route still active")
	attempts := 0
	route, err := NewRuntimeRoute(RuntimeRouteOptions{
		Resolve: func() (Endpoint, error) { return Endpoint{USBPath: "/sys/devices/3-2.1"}, nil },
		Start: func(context.Context) (io.Closer, error) {
			return closeFunc(func() error {
				if pcm.closes.Load() != 1 {
					t.Fatal("hardware route stopped while PCM was still owned")
				}
				attempts++
				if attempts == 1 {
					return cleanupFailure
				}
				return nil
			}), nil
		},
		OpenPCM: func(context.Context, Endpoint) (media.PCM, error) { return pcm, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := route.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	opened, err := route.OpenPCM(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := route.OpenPCM(context.Background()); err == nil {
		t.Fatal("opened a second PCM on one route")
	}
	if err := owner.Close(); !errors.Is(err, cleanupFailure) {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := opened.Close(); err != nil || pcm.closes.Load() != 1 || attempts != 2 {
		t.Fatalf("err=%v PCM closes=%d route attempts=%d", err, pcm.closes.Load(), attempts)
	}
	if _, err := route.OpenPCM(context.Background()); err == nil {
		t.Fatal("opened PCM on closed route")
	}
}
