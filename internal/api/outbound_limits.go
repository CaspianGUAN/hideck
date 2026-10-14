package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/outbound"
)

func respondOutboundLimit(c *gin.Context, err error) bool {
	var limited *outbound.LimitedError
	if !errors.As(err, &limited) {
		return false
	}
	if limited.RetryAfter > 0 {
		c.Header("Retry-After", strconv.FormatInt(limited.RetrySeconds(), 10))
	}
	c.JSON(http.StatusTooManyRequests, gin.H{
		"status": "error", "code": string(limited.Kind) + "_rate_limited",
		"reason": limited.Reason, "message": limited.Error(),
		"retry_after_seconds": limited.RetrySeconds(), "request_id": requestID(c),
	})
	return true
}
