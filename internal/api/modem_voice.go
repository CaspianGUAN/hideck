package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/device"
)

func (s *Server) allowModemVoiceModeChange(c *gin.Context, id, nextMode string) bool {
	if s.pool == nil || s.pool.ModemVoiceController() == nil {
		return true
	}
	controller := s.pool.ModemVoiceController()
	if controller.ActiveCall(id) == nil || nextMode == device.PhoneModeModemVoice {
		return true
	}
	c.JSON(http.StatusConflict, gin.H{"status": "error", "message": "请先挂断模组直拨通话，再切换或关闭电话服务"})
	return false
}
