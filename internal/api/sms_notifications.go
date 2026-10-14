package api

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/db"
)

// Browser cursors must remain exactly representable as JavaScript integers.
const smsNotificationCursorBits = 53

func (s *Server) handleSMSNotifications(c *gin.Context) {
	afterID, ok := parseSMSQueryAfterID(c)
	if !ok {
		return
	}
	result, err := db.GetSMSNotifications(c.Request.Context(), afterID)
	if err != nil {
		slog.Error("查询短信提醒失败", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "短信提醒查询失败，请稍后重试"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, result)
}

func parseSMSQueryAfterID(c *gin.Context) (*uint, bool) {
	values, present := c.Request.URL.Query()["after_id"]
	if !present {
		return nil, true
	}
	if len(values) != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"message": "after_id 只能提供一次"})
		return nil, false
	}
	return parseSMSCursorValue(c, values[0])
}

func parseSMSCursorValue(c *gin.Context, value string) (*uint, bool) {
	parsed, err := strconv.ParseUint(value, 10, smsNotificationCursorBits)
	if err != nil || uint64(uint(parsed)) != parsed {
		c.JSON(http.StatusBadRequest, gin.H{"message": "短信游标必须是有效的非负短信 ID"})
		return nil, false
	}
	id := uint(parsed)
	return &id, true
}
