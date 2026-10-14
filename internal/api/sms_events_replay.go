package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/db"
)

type smsReplay struct {
	throughID  uint
	page       []db.SMS
	incomplete map[uint]bool
}

func loadSMSReplay(ctx context.Context, afterID *uint) (*smsReplay, error) {
	replay := &smsReplay{incomplete: make(map[uint]bool)}
	if afterID == nil {
		return replay, nil
	}
	var err error
	replay.throughID, err = db.IncomingSMSCursor(ctx)
	if err != nil {
		return nil, err
	}
	replay.page, err = db.ListIncomingUnreadSMS(ctx, db.IncomingSMSBacklogQuery{
		AfterID: *afterID, ThroughID: &replay.throughID,
	})
	return replay, err
}

func (r *smsReplay) write(c *gin.Context) error {
	for len(r.page) > 0 {
		if err := c.Request.Context().Err(); err != nil {
			return err
		}
		for _, sms := range r.page {
			if err := writeSMSReceivedEvent(c, sms); err != nil {
				return err
			}
			r.incomplete[sms.ID] = sms.Incomplete
		}
		c.Writer.Flush()
		afterID := r.page[len(r.page)-1].ID
		if afterID >= r.throughID {
			break
		}
		var err error
		r.page, err = db.ListIncomingUnreadSMS(c.Request.Context(), db.IncomingSMSBacklogQuery{
			AfterID: afterID, ThroughID: &r.throughID,
		})
		if err != nil {
			return err
		}
	}
	r.page = nil
	return nil
}

func (r *smsReplay) duplicate(sms db.SMS) bool {
	incomplete, replayed := r.incomplete[sms.ID]
	if !replayed {
		return false
	}
	// The only published update is incomplete -> complete. Suppress queued
	// snapshots, but deliver completion even when the row was already replayed.
	if incomplete && !sms.Incomplete {
		r.incomplete[sms.ID] = false
		return false
	}
	return true
}

func writeSMSReceivedEvent(c *gin.Context, sms db.SMS) error {
	payload, err := json.Marshal(sms)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.Writer, "id: %d\nevent: sms_received\ndata: %s\n\n", sms.ID, payload)
	return err
}
