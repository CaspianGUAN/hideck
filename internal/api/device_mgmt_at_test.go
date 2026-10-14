package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/backend"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/device"
)

type fakeManualATSession struct {
	resp     string
	err      error
	cmd      string
	timeout  time.Duration
	closed   bool
	closeErr error
}

func (s *fakeManualATSession) Execute(cmd string, timeout time.Duration) (string, error) {
	s.cmd = cmd
	s.timeout = timeout
	return s.resp, s.err
}

func (s *fakeManualATSession) Close() error {
	s.closed = true
	return s.closeErr
}

func TestHandleDeviceMgmtExecuteATDoesNotOpenSecondSessionForMBIMBackend(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orig := openManualATSession
	defer func() { openManualATSession = orig }()

	opened := false
	openManualATSession = func(port string) (manualATSession, error) {
		opened = true
		return nil, errors.New("must not open a second MBIM AT reader")
	}

	p := device.NewPool(&config.Config{})
	be := &ussdDeviceBackendStub{mode: backend.BackendMBIM}
	setNestedPrivateField(t, p, []string{"workers"}, map[string]*device.Worker{
		"dev-mbim": {
			ID:      "dev-mbim",
			Config:  config.DeviceConfig{ID: "dev-mbim", DeviceBackend: backend.BackendMBIM, ATPort: "/dev/ttyUSB9"},
			Backend: be,
		},
	})
	server := &Server{pool: p}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Params = gin.Params{{Key: "device_id", Value: "dev-mbim"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/devices/dev-mbim/actions/at", strings.NewReader(`{"cmd":"AT+CSQ","timeout_ms":7000}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	server.handleDeviceMgmtExecuteAT(ctx)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if opened {
		t.Fatal("MBIM manual AT opened a transient serial session")
	}
	if !strings.Contains(rec.Body.String(), "没有可用 AT 管理器") {
		t.Fatalf("body=%s want explicit scheduler error", rec.Body.String())
	}
}
