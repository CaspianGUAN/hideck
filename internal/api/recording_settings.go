package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/phone"
)

type recordingSettingsPayload struct {
	RecordingRetentionDays *int `json:"recording_retention_days"`
}

func (s *Server) handleGetRecordingSettings(c *gin.Context) {
	days := s.recordingRetention.Days()
	if s.recordingRetention == nil && s.fullCfg != nil {
		days = s.fullCfg.Server.RecordingRetentionDays
	}
	c.JSON(http.StatusOK, gin.H{"recording_retention_days": days})
}

func (s *Server) handleUpdateRecordingSettings(c *gin.Context) {
	var request recordingSettingsPayload
	if err := c.ShouldBindJSON(&request); err != nil || request.RecordingRetentionDays == nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "参数错误"})
		return
	}
	days := *request.RecordingRetentionDays
	if days < 0 || days > phone.MaxRecordingRetentionDays {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error",
			"message": fmt.Sprintf("保留天数需在 0 到 %d 之间", phone.MaxRecordingRetentionDays)})
		return
	}
	if err := config.UpdateRecordingRetentionInFile(s.configPath, days); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "写入配置文件失败: " + err.Error()})
		return
	}
	if s.fullCfg != nil {
		s.fullCfg.Server.RecordingRetentionDays = days
	}
	s.recordingRetention.SetDays(days)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "recording_retention_days": days})
}
