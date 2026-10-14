package qdc507

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const cleanupTimeout = 20 * time.Second

type Lease struct{ manager *Manager }

// Start returns cleanup ownership even when the remote launch fails: the helper
// may already have changed the USB route before the ADB response was lost.
func (m *Manager) Start(ctx context.Context) (*Lease, error) {
	release, err := m.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if m.active != nil {
		return nil, fmt.Errorf("qdc507: audio route is already owned")
	}
	if err := m.options.Check(ctx); err != nil {
		return nil, err
	}
	if m.target.boot == "" {
		return nil, fmt.Errorf("qdc507: runtime has not been prepared")
	}
	if err := m.verifyInstalled(ctx); err != nil {
		return nil, err
	}
	if err := m.options.Check(ctx); err != nil {
		return nil, err
	}
	lease := &Lease{manager: m}
	m.active = lease
	_, err = m.command(ctx, startRouteScript)
	if err == nil {
		err = m.options.Check(ctx)
	}
	return lease, err
}

// Close is retryable. Failed cleanup retains ownership and prevents a new route
// from starting over an unconfirmed old helper.
func (l *Lease) Close() error {
	m := l.manager
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	release, err := m.enter(ctx)
	if err != nil {
		return err
	}
	defer release()
	if m.active != l {
		return nil
	}
	if retired, err := m.retireRebootedTarget(ctx); retired || err != nil {
		return err
	}
	if _, err := m.command(ctx, stopRouteScript); err != nil {
		return err
	}
	m.active = nil
	return nil
}

// Shutdown releases only owned user-space processes. Drivers stay resident until
// the module reboots. It also handles preparation that failed after starting UCM.
func (m *Manager) Shutdown(ctx context.Context) error {
	release, err := m.enter(ctx)
	if err != nil {
		return err
	}
	defer release()
	if m.target.boot == "" {
		return nil
	}
	if retired, err := m.retireRebootedTarget(ctx); retired || err != nil {
		return err
	}
	_, routeErr := m.command(ctx, stopRouteScript)
	if routeErr != nil {
		return routeErr
	}
	m.active = nil
	_, calibrationErr := m.command(ctx, stopCalibrationScript)
	return errors.Join(routeErr, calibrationErr)
}

// A confirmed new boot proves the old processes and temporary drivers are gone.
// Only discard local ownership; never send old cleanup commands to the new boot.
// An unreachable/offline device does not provide that proof and remains owned.
func (m *Manager) retireRebootedTarget(ctx context.Context) (bool, error) {
	current, err := m.options.Client.Bind(ctx, m.options.USB)
	if err != nil {
		return false, err
	}
	if current.boot == "" || current.usb != m.target.usb {
		return false, fmt.Errorf("qdc507: cannot confirm cleanup target identity")
	}
	if current.boot == m.target.boot {
		return false, nil
	}
	m.active, m.target = nil, Target{}
	return true, nil
}
