package audio

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/yibaiba/hideck/internal/modemvoice/media"
)

type Command struct {
	Program string
	Args    []string
}
type Runner interface {
	Run(context.Context, Command) error
}

// RouteOptions is per call. Commands must address only this call/device's
// route, with an idempotent stop command. No shell or argument templates run.
type RouteOptions struct {
	Start, Stop Command
	Timeout     time.Duration
	Runner      Runner
	Resolve     func() (Endpoint, error)
	OpenPCM     func(context.Context, Endpoint) (media.PCM, error)
}

type CommandRoute struct{ *RuntimeRoute }

func NewCommandRoute(options RouteOptions) (*CommandRoute, error) {
	if options.Runner == nil || options.Timeout <= 0 {
		return nil, errors.New("audio: route requires runner and positive command timeout")
	}
	for _, cmd := range []Command{options.Start, options.Stop} {
		if err := validateCommand(cmd); err != nil {
			return nil, err
		}
	}
	options.Start.Args = append([]string(nil), options.Start.Args...)
	options.Stop.Args = append([]string(nil), options.Stop.Args...)
	route, err := NewRuntimeRoute(RuntimeRouteOptions{
		Resolve: options.Resolve, OpenPCM: options.OpenPCM,
		Start: func(ctx context.Context) (io.Closer, error) {
			owner := &commandLease{options: options}
			bounded, cancel := context.WithTimeout(ctx, options.Timeout)
			defer cancel()
			err := options.Runner.Run(bounded, options.Start)
			return owner, errors.Join(err, bounded.Err())
		},
	})
	if err != nil {
		return nil, err
	}
	return &CommandRoute{RuntimeRoute: route}, nil
}

func validateCommand(cmd Command) error {
	if !filepath.IsAbs(cmd.Program) || strings.ContainsRune(cmd.Program, 0) {
		return errors.New("audio: route command requires an absolute executable path")
	}
	for _, arg := range cmd.Args {
		if strings.ContainsRune(arg, 0) {
			return errors.New("audio: route argument contains NUL")
		}
	}
	return nil
}

type commandLease struct{ options RouteOptions }

func (l *commandLease) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), l.options.Timeout)
	defer cancel()
	err := l.options.Runner.Run(ctx, l.options.Stop)
	return errors.Join(err, ctx.Err())
}
