package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/phone"
)

func putRecordingSettings(t *testing.T, server *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/settings/recordings", bytes.NewBufferString(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	server.handleUpdateRecordingSettings(ctx)
	return recorder
}

func TestRecordingSettingsPersistAndApplyRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := writeSystemSettingsFixture(path); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	retention := phone.StartRecordingRetention(ctx, phone.RecordingRetentionOptions{Days: 30})
	server := &Server{fullCfg: &config.Config{}, configPath: path, recordingRetention: retention}

	if response := putRecordingSettings(t, server, `{"recording_retention_days":7}`); response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Server.RecordingRetentionDays != 7 || retention.Days() != 7 || server.fullCfg.Server.RecordingRetentionDays != 7 {
		t.Fatalf("file=%d runtime=%d cfg=%d", loaded.Server.RecordingRetentionDays, retention.Days(),
			server.fullCfg.Server.RecordingRetentionDays)
	}
	for _, body := range []string{`{"recording_retention_days":-1}`, `{"recording_retention_days":4000}`, `{}`} {
		if response := putRecordingSettings(t, server, body); response.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d", body, response.Code)
		}
	}
	if retention.Days() != 7 {
		t.Fatalf("rejected update changed Days to %d", retention.Days())
	}
}
