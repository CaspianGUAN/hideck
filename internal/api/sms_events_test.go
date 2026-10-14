package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/db"
)

func TestSMSEventsAuthenticationAndCursorValidation(t *testing.T) {
	previous := db.DB
	if err := db.Init(filepath.Join(t.TempDir(), "sms-events.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.DB = previous })
	server := &Server{auth: config.WebConfig{Username: "admin", Password: "secret"}, shutdownCh: make(chan struct{})}
	router := gin.New()
	group := router.Group("/api", server.authMiddleware())
	group.GET("/sms/events", server.handleSMSEvents)
	token := testSessionToken(t, "secret", time.Now().Add(time.Hour))
	for _, sample := range []struct {
		query  string
		header string
		auth   bool
		want   int
	}{
		{"", "", false, http.StatusUnauthorized},
		{"?after_id=", "", true, http.StatusBadRequest},
		{"?after_id=-1", "", true, http.StatusBadRequest},
		{"?after_id=1junk", "", true, http.StatusBadRequest},
		{"?after_id=1&after_id=2", "", true, http.StatusBadRequest},
		{"", "not-a-number", true, http.StatusBadRequest},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/sms/events"+sample.query, nil)
		if sample.auth {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if sample.header != "" {
			req.Header.Set("Last-Event-ID", sample.header)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != sample.want {
			t.Fatalf("query=%q header=%q auth=%t status=%d body=%s", sample.query, sample.header, sample.auth, response.Code, response.Body)
		}
	}
}

func TestSMSEventStreamDeliversIncomingSMSAndBacklog(t *testing.T) {
	identity := db.SMSIdentity{ICCID: "ICC-STREAM", IMSI: "IMSI-STREAM"}
	router, token := newSMSEventTestRouter(t, "sms-events-stream.db", identity)
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)

	first := saveIncomingTestSMS(t, identity, "first")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/sms/events?after_id=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := ts.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") || response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("headers content-type=%q buffering=%q", response.Header.Get("Content-Type"), response.Header.Get("X-Accel-Buffering"))
	}
	reader := bufio.NewReader(response.Body)
	if got := readSSEComment(t, reader); got != ": connected" {
		t.Fatalf("prelude=%q", got)
	}
	event := readSMSReceivedEventNamed(t, reader, "first")
	if event.ID != first.ID {
		t.Fatalf("backlog event=%+v want id=%d", event, first.ID)
	}

	second := saveIncomingTestSMS(t, identity, "second")
	live := readSMSReceivedEventNamed(t, reader, "second")
	if live.ID != second.ID {
		t.Fatalf("live event=%+v want id=%d", live, second.ID)
	}
}

func TestSMSEventStreamOmitsReplayWithoutCursor(t *testing.T) {
	identity := db.SMSIdentity{ICCID: "ICC-LIVE", IMSI: "IMSI-LIVE"}
	router, token := newSMSEventTestRouter(t, "sms-events-live.db", identity)
	saveIncomingTestSMS(t, identity, "old-inbox")
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/sms/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := ts.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	if got := readSSEComment(t, reader); got != ": connected" {
		t.Fatalf("prelude=%q", got)
	}
	fresh := saveIncomingTestSMS(t, identity, "fresh")
	for {
		event := readSMSReceivedEvent(t, reader)
		if event.Content == "old-inbox" {
			t.Fatalf("replayed inbox SMS %+v", event)
		}
		if event.Content == "fresh" {
			if event.ID != fresh.ID {
				t.Fatalf("live event=%+v want id=%d", event, fresh.ID)
			}
			return
		}
	}
}

func newSMSEventTestRouter(t *testing.T, dbName string, identity db.SMSIdentity) (http.Handler, string) {
	t.Helper()
	previous := db.DB
	if err := db.Init(filepath.Join(t.TempDir(), dbName)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.DB = previous })
	if err := db.DB.Create(&db.SIMCard{ICCID: identity.ICCID, IMSI: identity.IMSI}).Error; err != nil {
		t.Fatal(err)
	}
	server := &Server{auth: config.WebConfig{Username: "admin", Password: "secret"}, shutdownCh: make(chan struct{})}
	router := gin.New()
	api := router.Group("/api", server.authMiddleware())
	api.GET("/sms/events", server.handleSMSEvents)
	return router, testSessionToken(t, "secret", time.Now().Add(time.Hour))
}

func saveIncomingTestSMS(t *testing.T, identity db.SMSIdentity, content string) db.SMS {
	t.Helper()
	if err := db.SaveSMSForIdentity(db.SMSRecord{
		Identity: identity, Sender: "10086", Content: content, Type: 1, Status: 0, Timestamp: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	var sms db.SMS
	if err := db.DB.Where("content = ?", content).Last(&sms).Error; err != nil {
		t.Fatal(err)
	}
	return sms
}

func readSSEComment(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read SSE comment: %v", err)
	}
	blank, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read SSE comment terminator: %v", err)
	}
	if blank != "\n" {
		t.Fatalf("SSE comment terminator=%q", blank)
	}
	return strings.TrimRight(line, "\r\n")
}

func readSMSReceivedEventNamed(t *testing.T, reader *bufio.Reader, content string) db.SMS {
	t.Helper()
	for {
		sms := readSMSReceivedEvent(t, reader)
		if sms.Content == content {
			return sms
		}
	}
}

func readSMSReceivedEvent(t *testing.T, reader *bufio.Reader) db.SMS {
	t.Helper()
	var idLine, eventLine, dataLine string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE event: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, ":") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "id:"):
			idLine = line
		case strings.HasPrefix(line, "event:"):
			eventLine = line
		case strings.HasPrefix(line, "data:"):
			dataLine = line
		case line == "":
			if idLine == "" && eventLine == "" && dataLine == "" {
				continue
			}
			if strings.TrimSpace(strings.TrimPrefix(eventLine, "event:")) != "sms_received" {
				t.Fatalf("event line=%q", eventLine)
			}
			payload := strings.TrimSpace(strings.TrimPrefix(dataLine, "data:"))
			var sms db.SMS
			if err := json.Unmarshal([]byte(payload), &sms); err != nil {
				t.Fatalf("decode SMS event %q: %v", payload, err)
			}
			if strings.TrimSpace(strings.TrimPrefix(idLine, "id:")) != fmt.Sprintf("%d", sms.ID) {
				t.Fatalf("id line=%q sms id=%d", idLine, sms.ID)
			}
			return sms
		}
	}
}
