package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/device"
	"github.com/yibaiba/hideck/internal/outbound"
)

type deniedOutbound struct{}

func (deniedOutbound) Consume(context.Context, outbound.Request) error {
	return &outbound.LimitedError{Kind: outbound.SMS, Reason: "最小间隔", RetryAfter: 4500 * time.Millisecond}
}

func TestHandleSendSMSReturnsSharedLimitBeforeSending(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := device.NewPool(&config.Config{})
	pool.AttachWorkerForTest(&device.Worker{ID: "dev", Backend: &ussdDeviceBackendStub{}})
	pool.SetOutboundLimiter(deniedOutbound{})
	sent := false
	pool.SetRoutedSMSTestSenders(nil, func(string, string, string) error { sent = true; return nil })
	server := &Server{pool: pool}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sms/send", strings.NewReader(`{"device_id":"dev","phone":"10010","message":"test"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	server.handleSendSMS(c)
	if recorder.Code != http.StatusTooManyRequests || sent || recorder.Header().Get("Retry-After") != "5" {
		t.Fatalf("status=%d sent=%v body=%s", recorder.Code, sent, recorder.Body)
	}
	if !strings.Contains(recorder.Body.String(), "sms_rate_limited") {
		t.Fatal(recorder.Body)
	}
}

func TestPhoneLimitUses429AndRetainsRetryAfter(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	server := &Server{}
	server.respondPhoneError(c, errors.Join(errors.New("dial rejected"), &outbound.LimitedError{Kind: outbound.Call, RetryAfter: 10 * time.Second}))
	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") != "10" {
		t.Fatal(recorder)
	}
}
