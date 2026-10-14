package host

import (
	"errors"

	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
)

// ATD is never retried. Return a snapshot alongside the error while the call
// remains owned, so the phone service can expose it for explicit hangup.
func (c *Controller) recoverFailedDial(d *device, current *call, dialErr error) (voicehost.CallSnapshot, error) {
	current.dialUncertain = true
	update, refreshErr := d.session.Refresh(d.ctx)
	refreshErr = errors.Join(refreshErr, c.apply(d, update))
	if refreshErr == nil {
		refreshErr = c.finishUnconfirmedDial(d)
	}
	if refreshErr == nil && d.call == current {
		refreshErr = c.hangup(d.ctx, d, current)
	}
	err := errors.Join(dialErr, refreshErr)
	if d.call == current {
		return current.snapshot, err
	}
	return voicehost.CallSnapshot{}, err
}

// Called with op held, only after a successful CLCC and application of all its
// changes. An empty tracker ID then proves the uncertain dial created no call.
func (c *Controller) finishUnconfirmedDial(d *device) error {
	if d.call != nil && d.call.dialUncertain && d.call.trackedID == "" {
		return c.endMedia(d, "dial_failed")
	}
	return nil
}
