package volte

import (
	"errors"
	"fmt"
	"strings"
)

// RetireDevice discards local state when its owning Worker is removed. The
// caller must stop that Worker's transport; this does not claim a modem hangup
// succeeded and deliberately performs no device-ID-based hardware operations.
func (c *Controller) RetireDevice(deviceID string) error {
	if c == nil {
		return nil
	}
	deviceID = strings.TrimSpace(deviceID)
	return c.withDevice(deviceID, func() error {
		c.mu.Lock()
		s := c.sess[deviceID]
		c.mu.Unlock()
		if s == nil {
			return nil
		}
		// Drain callbacks already running before discarding their call/media state.
		s.events.Lock()
		defer s.events.Unlock()
		c.mu.Lock()
		delete(c.sess, deviceID)
		c.mu.Unlock()
		if s.voice == nil {
			return nil
		}
		var errs []error
		for _, call := range s.voice.list() {
			if c.audio != nil {
				errs = append(errs, c.audio.Stop(call.ID))
			}
			if media := c.media.take(call.ID); media != nil {
				errs = append(errs, media.Close())
			}
			s.voice.forget(call.ID)
		}
		if err := errors.Join(errs...); err != nil {
			return fmt.Errorf("volte: retire device media: %w", err)
		}
		return nil
	})
}
