package host

import (
	"context"
	"errors"
	"time"
)

const dtmfRequestTimeout = 10 * time.Second

func (c *Controller) SendCallDTMF(deviceID, callID, digit string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dtmfRequestTimeout)
	defer cancel()
	d, err := c.lockReady(ctx, deviceID)
	if err != nil {
		return err
	}
	defer d.op.Unlock()
	current, err := matchingCall(d, callID)
	if err != nil {
		return err
	}
	if current.snapshot.State != "connected" || current.trackedID == "" {
		return errors.New("模组直拨需要接通后才能发送按键")
	}
	update, err := d.session.DTMF(ctx, current.trackedID, digit)
	return errors.Join(err, c.apply(d, update))
}
