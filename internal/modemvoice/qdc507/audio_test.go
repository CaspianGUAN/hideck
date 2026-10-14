package qdc507

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/yibaiba/hideck/internal/modemvoice/audio"
	"github.com/yibaiba/hideck/internal/modemvoice/media"
)

type testPCM struct{ closed int }

func (*testPCM) ReadFrame() ([]int16, error) { return nil, io.EOF }
func (*testPCM) WriteFrame([]int16) error    { return nil }
func (p *testPCM) Close() error              { p.closed++; return nil }

func TestAudioRouteRejectsAnotherUSBDeviceBeforeStarting(t *testing.T) {
	c := &testClient{}
	m := testManager(t, c)
	route, err := m.AudioRoute(AudioOptions{
		Resolve: func() (audio.Endpoint, error) { return audio.Endpoint{USBPath: "/sys/devices/3-2.2"}, nil },
		OpenPCM: func(context.Context, audio.Endpoint) (media.PCM, error) {
			t.Fatal("opened wrong module's PCM")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := route.Start(context.Background()); err == nil || len(c.scripts) != 0 {
		t.Fatalf("err=%v scripts=%v", err, c.scripts)
	}
}

func TestAudioRouteClosesPCMIfSIMChangesWhileOpening(t *testing.T) {
	c := &testClient{}
	m := testManager(t, c)
	changed := false
	failure := errors.New("SIM generation changed")
	m.options.Check = func(context.Context) error {
		if changed {
			return failure
		}
		return nil
	}
	pcm := &testPCM{}
	route, err := m.AudioRoute(AudioOptions{
		Resolve: func() (audio.Endpoint, error) { return audio.Endpoint{USBPath: "/sys/devices/3-2.1"}, nil },
		OpenPCM: func(context.Context, audio.Endpoint) (media.PCM, error) {
			changed = true
			return pcm, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := route.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := route.OpenPCM(context.Background()); !errors.Is(err, failure) || got != nil || pcm.closed != 1 {
		t.Fatalf("pcm=%v err=%v closed=%d", got, err, pcm.closed)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}
