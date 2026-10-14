package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
)

// AudioRoute is injected per device and firmware. Start returns an owner for
// only this call's route; Close releases that route, not another device's runtime.
// Implementations must honor context and clean up resources they cannot return.
type AudioRoute interface {
	Start(context.Context) (io.Closer, error)
	OpenPCM(context.Context) (PCM, error)
}

type OpenRequest struct {
	Conn       net.PacketConn
	Remote     netip.AddrPort
	ListenOnly bool
	Route      AudioRoute
}

// Open acquires route then PCM. After validation it owns Conn even on failure.
// A late factory result following cancellation is closed before returning.
// Real adapters are required; no default QPCMV sequence or null PCM is assumed.
func Open(ctx context.Context, req OpenRequest) (bridge *Bridge, err error) {
	if err = validateTransport(ctx, req.Conn, req.Remote); err != nil {
		return nil, err
	}
	if req.Route == nil {
		return nil, errors.New("modem media: audio route is required")
	}
	owned := &ownedAudio{}
	defer func() {
		if err != nil {
			err = errors.Join(err, owned.Close(), req.Conn.Close())
		}
	}()
	owned.route, err = req.Route.Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("modem media: start route: %w", err)
	}
	if owned.route == nil {
		return nil, errors.New("modem media: route started without an owner")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	owned.PCM, err = req.Route.OpenPCM(ctx)
	if err != nil {
		return nil, fmt.Errorf("modem media: open PCM: %w", err)
	}
	if owned.PCM == nil {
		return nil, errors.New("modem media: adapter returned no PCM")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return NewBridge(ctx, Config{Conn: req.Conn, Remote: req.Remote, ListenOnly: req.ListenOnly, PCM: owned})
}

type ownedAudio struct {
	PCM
	route io.Closer
	once  sync.Once
	err   error
}

func (o *ownedAudio) Close() error {
	o.once.Do(func() {
		if o.PCM != nil {
			o.err = o.PCM.Close()
		}
		if o.route != nil {
			o.err = errors.Join(o.err, o.route.Close())
		}
	})
	return o.err
}
