package qdc507

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/yibaiba/hideck/internal/modemvoice/audio"
	"github.com/yibaiba/hideck/internal/modemvoice/media"
)

type AudioOptions struct {
	Resolve func() (audio.Endpoint, error)
	OpenPCM func(context.Context, audio.Endpoint) (media.PCM, error)
}

// AudioRoute is created per call after Prepare. All calls on this device must
// share one Manager. The caller closes media before shutting down the Manager.
func (m *Manager) AudioRoute(options AudioOptions) (*audio.RuntimeRoute, error) {
	if options.Resolve == nil || options.OpenPCM == nil {
		return nil, fmt.Errorf("qdc507: audio resolver and PCM opener are required")
	}
	var lease *Lease
	return audio.NewRuntimeRoute(audio.RuntimeRouteOptions{
		Resolve: func() (audio.Endpoint, error) {
			endpoint, err := options.Resolve()
			if err == nil && filepath.Base(endpoint.USBPath) != m.options.USB {
				err = fmt.Errorf("qdc507: audio endpoint belongs to another USB device")
			}
			return endpoint, err
		},
		Start: func(ctx context.Context) (io.Closer, error) {
			var err error
			lease, err = m.Start(ctx)
			if lease == nil {
				return nil, err
			}
			return lease, err
		},
		OpenPCM: func(ctx context.Context, endpoint audio.Endpoint) (media.PCM, error) {
			if err := lease.Check(ctx); err != nil {
				return nil, err
			}
			pcm, err := options.OpenPCM(ctx, endpoint)
			if err == nil {
				err = lease.Check(ctx)
			}
			if err != nil && pcm != nil {
				return nil, errors.Join(err, pcm.Close())
			}
			return pcm, err
		},
	})
}

// Check confirms this exact route and SIM generation, including the module boot
// after USB re-enumeration. Matching USB topology alone cannot prove continuity.
func (l *Lease) Check(ctx context.Context) error {
	if l == nil {
		return fmt.Errorf("qdc507: no audio route lease")
	}
	m := l.manager
	release, err := m.enter(ctx)
	if err != nil {
		return err
	}
	defer release()
	if m.active != l {
		return fmt.Errorf("qdc507: audio route lease is no longer active")
	}
	if err := m.options.Check(ctx); err != nil {
		return err
	}
	_, err = m.command(ctx, checkRouteScript)
	return err
}
