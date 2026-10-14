package volte

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
)

// DisableForHandoff confirms physical calls have ended before another backend
// takes ownership. Control failure is not evidence that the modem hung up.
func (c *Controller) DisableForHandoff(ctx context.Context, deviceID string) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("volte: handoff context is required")
	}
	deviceID = strings.TrimSpace(deviceID)
	return c.withDevice(deviceID, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.mu.Lock()
		s := c.sess[deviceID]
		c.mu.Unlock()
		if s == nil {
			return nil
		}
		if err := c.endCallsForHandoff(ctx, deviceID); err != nil {
			return err
		}
		if err := c.host.ReleaseIMSClients(deviceID); err != nil {
			return fmt.Errorf("volte: release IMS clients for handoff: %w", err)
		}
		c.mu.Lock()
		s.gen++
		delete(c.sess, deviceID)
		c.mu.Unlock()
		return nil
	})
}

func (c *Controller) endCallsForHandoff(ctx context.Context, deviceID string) error {
	info, err := c.callsForHandoff(ctx, deviceID)
	if err != nil {
		return err
	}
	for _, call := range info.Calls {
		if call.State == qmiCallIdle || call.State == qmiCallEnd {
			continue
		}
		err := c.host.VOICEHangup(ctx, deviceID, call.ID)
		if err != nil && !qmi.VoiceCallAlreadyGone(err) {
			return fmt.Errorf("volte: hang up call %d for handoff: %w", call.ID, err)
		}
	}
	// An accepted hangup alone is insufficient: pending disconnects or new
	// incoming calls leave ownership here until an explicit retry succeeds.
	info, err = c.callsForHandoff(ctx, deviceID)
	if err != nil {
		return err
	}
	for _, call := range info.Calls {
		if call.State != qmiCallIdle && call.State != qmiCallEnd {
			return fmt.Errorf("volte: call %d is still active; retry switching after it ends", call.ID)
		}
	}
	if vs := c.sessionVoice(deviceID); vs != nil {
		c.handleVoiceInfo(deviceID, vs, info)
	}
	return nil
}

func (c *Controller) callsForHandoff(ctx context.Context, deviceID string) (*qmi.VoiceAllCallInfo, error) {
	if c.host == nil {
		return nil, errors.New("volte: controller is not configured")
	}
	info, err := c.host.VOICEGetAllCallInfo(ctx, deviceID)
	if err != nil {
		return nil, fmt.Errorf("volte: query calls for handoff: %w", err)
	}
	if info == nil {
		return nil, errors.New("volte: empty call query response during handoff")
	}
	return info, nil
}
