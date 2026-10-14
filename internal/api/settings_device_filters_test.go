package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/yibaiba/hideck/internal/config"
)

func putNotificationSettingsForTest(t *testing.T, server *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/settings/notifications", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	server.handleUpdateNotificationSettings(c)
	return recorder
}

func TestUpdateNotificationSettingsDeviceFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("server:\n  port: 7575\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &Server{configPath: configPath, fullCfg: &config.Config{
		NotificationDeviceFilters: map[string][]string{"bark": {"wws27u2i4"}},
	}}

	// An older client that omits the field keeps the stored filters.
	if rec := putNotificationSettingsForTest(t, server, `{}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := server.fullCfg.NotificationDeviceFilters["bark"]; len(got) != 1 {
		t.Fatalf("omitted field changed filters: %#v", server.fullCfg.NotificationDeviceFilters)
	}

	rec := putNotificationSettingsForTest(t, server,
		`{"device_filters":{"telegram":[" wws27u1i4","wws27u1i4",""],"bark":[]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	got := server.fullCfg.NotificationDeviceFilters
	if len(got) != 1 || len(got["telegram"]) != 1 || got["telegram"][0] != "wws27u1i4" {
		t.Fatalf("filters = %#v", got)
	}

	rec = putNotificationSettingsForTest(t, server, `{"device_filters":{"sms":["wws27u1i4"]}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown channel status = %d", rec.Code)
	}
}
