package audio

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/yibaiba/hideck/internal/modemvoice/media"
)

type RuntimeRouteOptions struct {
	// Start retains cleanup ownership even after partial startup failure.
	Start   func(context.Context) (io.Closer, error)
	Resolve func() (Endpoint, error)
	OpenPCM func(context.Context, Endpoint) (media.PCM, error)
}

// RuntimeRoute is per call; the injected runtime can be shared by a device.
// PCM is closed before the hardware route, including a late OpenPCM result.
type RuntimeRoute struct {
	options                RuntimeRouteOptions
	mu                     sync.Mutex
	openMu                 sync.Mutex
	ctx                    context.Context
	cancel                 context.CancelFunc
	started, ready, closed bool
	usbPath                string
	pcm                    *contextPCM // Protected by openMu.
}

func NewRuntimeRoute(options RuntimeRouteOptions) (*RuntimeRoute, error) {
	if options.Start == nil || options.Resolve == nil || options.OpenPCM == nil {
		return nil, errors.New("audio: route requires runtime, resolver and PCM opener")
	}
	return &RuntimeRoute{options: options}, nil
}

func (r *RuntimeRoute) Start(ctx context.Context) (io.Closer, error) {
	if ctx == nil {
		return nil, errors.New("audio: route context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return nil, errors.New("audio: route already used; create one per call")
	}
	r.started = true
	r.mu.Unlock()
	endpoint, err := r.options.Resolve()
	if err != nil {
		return nil, err
	}
	if endpoint.USBPath == "" {
		return nil, errors.New("audio: route requires a USB-bound endpoint")
	}
	r.ctx, r.cancel = context.WithCancel(ctx)
	owner, err := r.options.Start(r.ctx)
	if err == nil && owner == nil {
		err = errors.New("audio: runtime started without a cleanup owner")
	}
	err = errors.Join(err, r.ctx.Err())
	r.mu.Lock()
	r.usbPath, r.ready = endpoint.USBPath, err == nil
	r.mu.Unlock()
	return &runtimeLease{route: r, owner: owner}, err
}

func (r *RuntimeRoute) OpenPCM(ctx context.Context) (media.PCM, error) {
	if ctx == nil {
		return nil, errors.New("audio: PCM context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.openMu.Lock()
	defer r.openMu.Unlock()
	r.mu.Lock()
	ready, root := r.ready && !r.closed, r.usbPath
	r.mu.Unlock()
	if !ready || r.pcm != nil {
		return nil, errors.New("audio: route is inactive or PCM is already owned")
	}
	openCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.ctx, cancel)
	release := func() { stop(); cancel() }
	pcm, err := r.open(openCtx, root)
	if err != nil {
		release()
		return nil, err
	}
	r.pcm = &contextPCM{PCM: pcm, release: release}
	return r.pcm, nil
}

func (r *RuntimeRoute) open(ctx context.Context, root string) (media.PCM, error) {
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	endpoint, err := r.options.Resolve()
	if err != nil {
		return nil, err
	}
	if endpoint.USBPath != root {
		return nil, errors.New("audio: USB identity changed during route startup")
	}
	pcm, err := r.options.OpenPCM(ctx, endpoint)
	err = errors.Join(err, ctx.Err(), r.ctx.Err())
	if err == nil && pcm == nil {
		err = errors.New("audio: PCM opener returned no device")
	}
	if err != nil && pcm != nil {
		err = errors.Join(err, pcm.Close())
		pcm = nil
	}
	return pcm, err
}

type contextPCM struct {
	media.PCM
	release func()
	once    sync.Once
	err     error
}

func (p *contextPCM) Close() error {
	p.once.Do(func() { p.err = p.PCM.Close(); p.release() })
	return p.err
}

type runtimeLease struct {
	route *RuntimeRoute
	owner io.Closer
	mu    sync.Mutex
	done  bool
	err   error
}

// Hardware cleanup is retryable; a failed Close is not a released route.
func (l *runtimeLease) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.done {
		return l.err
	}
	r := l.route
	r.mu.Lock()
	r.closed, r.ready = true, false
	r.mu.Unlock()
	// Cancel an in-flight opener before waiting. Once PCM ownership has been
	// handed off, close it explicitly first so normal shutdown is not reported
	// by the process monitor as an unexpected context cancellation.
	if !r.openMu.TryLock() {
		r.cancel()
		r.openMu.Lock()
	}
	defer r.openMu.Unlock()
	var pcmErr, routeErr error
	if r.pcm != nil {
		pcmErr = r.pcm.Close()
	}
	r.cancel()
	if l.owner != nil {
		routeErr = l.owner.Close()
	}
	l.err = errors.Join(pcmErr, routeErr)
	l.done = routeErr == nil
	return l.err
}
