package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/db"
)

const smsEventHeartbeatInterval = 15 * time.Second

func (s *Server) handleSMSEvents(c *gin.Context) {
	afterID, ok := parseSMSEventAfterID(c)
	if !ok {
		return
	}
	stream, cancel := db.SubscribeIncomingSMS()
	defer cancel()
	replay, err := loadSMSReplay(c.Request.Context(), afterID)
	if err != nil {
		slog.Error("查询短信事件补发失败", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "短信事件查询失败，请稍后重试"})
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-store")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	if _, err := fmt.Fprint(c.Writer, ": connected\n\n"); err != nil {
		return
	}
	c.Writer.Flush()
	if err := replay.write(c); err != nil {
		slog.Error("短信事件补发中断", "err", err)
		return
	}
	if err := s.streamSMSEvents(c, stream, replay); err != nil {
		slog.Error("短信事件推送中断", "err", err)
	}
}

func (s *Server) streamSMSEvents(c *gin.Context, stream <-chan db.SMS, replay *smsReplay) error {
	heartbeat := time.NewTicker(smsEventHeartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case sms, ok := <-stream:
			if !ok {
				return nil
			}
			if replay.duplicate(sms) {
				continue
			}
			if err := writeSMSReceivedEvent(c, sms); err != nil {
				return err
			}
			c.Writer.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(c.Writer, ": heartbeat\n\n"); err != nil {
				return err
			}
			c.Writer.Flush()
		case <-c.Request.Context().Done():
			return nil
		case <-s.shutdownCh:
			return nil
		}
	}
}

func parseSMSEventAfterID(c *gin.Context) (*uint, bool) {
	if header := strings.TrimSpace(c.GetHeader("Last-Event-ID")); header != "" {
		return parseSMSCursorValue(c, header)
	}
	return parseSMSQueryAfterID(c)
}
