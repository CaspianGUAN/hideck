package api

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/db"
)

func TestSMSEventReplayDeliversAllPagesBeforeLiveSMS(t *testing.T) {
	identity := db.SMSIdentity{ICCID: "ICC-PAGES", IMSI: "IMSI-PAGES"}
	router, token := newSMSEventTestRouter(t, "sms-pages.db", identity)
	rows := make([]db.SMS, 513) // More than two 256-row database pages.
	for i := range rows {
		rows[i] = db.SMS{IMSI: identity.IMSI, ICCID: identity.ICCID, Type: 1, Content: fmt.Sprintf("backlog-%d", i)}
	}
	if err := db.DB.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	reader := openSMSReplayTestStream(t, router, token)
	live := saveIncomingTestSMS(t, identity, "live-after-backlog")
	for _, want := range rows {
		got := readSMSReceivedEvent(t, reader)
		if got.ID != want.ID || got.Content != want.Content {
			t.Fatalf("replay id=%d content=%q, want id=%d content=%q", got.ID, got.Content, want.ID, want.Content)
		}
	}
	if got := readSMSReceivedEvent(t, reader); got.ID != live.ID {
		t.Fatalf("live id=%d want=%d", got.ID, live.ID)
	}
}

func TestSMSEventReplayDeliversMultipartCompletion(t *testing.T) {
	identity := db.SMSIdentity{ICCID: "ICC-REPLAY-MULTI", IMSI: "IMSI-REPLAY-MULTI"}
	router, token := newSMSEventTestRouter(t, "sms-replay-multi.db", identity)
	input := db.ReceivedMultipartSMS{
		IMSI: identity.IMSI, Sender: "10086", Content: "part-1",
		FragmentSessionKey: "replay-session", Timestamp: time.Now(), Incomplete: true,
	}
	created, err := db.SaveReceivedMultipartSMS(input)
	if err != nil {
		t.Fatal(err)
	}
	reader := openSMSReplayTestStream(t, router, token)
	if got := readSMSReceivedEvent(t, reader); got.ID != created.SMSID || !got.Incomplete {
		t.Fatalf("replayed SMS=%+v", got)
	}
	input.Content, input.Incomplete = "part-1 part-2", false
	if result, err := db.SaveReceivedMultipartSMS(input); err != nil || !result.Reconciled {
		t.Fatalf("reconcile=%+v err=%v", result, err)
	}
	if got := readSMSReceivedEvent(t, reader); got.ID != created.SMSID || got.Incomplete || got.Content != input.Content {
		t.Fatalf("completed SMS=%+v", got)
	}
}

func TestSMSEventReplayKeepsFixedBoundary(t *testing.T) {
	identity := db.SMSIdentity{ICCID: "ICC-BOUNDARY", IMSI: "IMSI-BOUNDARY"}
	newSMSEventTestRouter(t, "sms-boundary.db", identity)
	rows := make([]db.SMS, 257)
	for i := range rows {
		rows[i] = db.SMS{Type: 1, Content: fmt.Sprintf("old-%d", i)}
	}
	if err := db.DB.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	afterID := uint(0)
	replay, err := loadSMSReplay(context.Background(), &afterID)
	if err != nil {
		t.Fatal(err)
	}
	live := saveIncomingTestSMS(t, identity, "outside-replay")
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if err := replay.write(c); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	for _, want := range rows {
		if got := readSMSReceivedEvent(t, reader); got.ID != want.ID {
			t.Fatalf("replayed id=%d want=%d", got.ID, want.ID)
		}
	}
	if _, err := reader.Peek(1); err == nil {
		t.Fatal("replay included a post-boundary SMS")
	}
	if replay.duplicate(live) {
		t.Fatal("post-boundary live SMS was suppressed")
	}
}

func TestSMSEventReplayDeduplicatesSnapshotsNotCompletion(t *testing.T) {
	replay := &smsReplay{incomplete: map[uint]bool{1: true, 2: false}}
	for _, sample := range []struct {
		sms       db.SMS
		duplicate bool
	}{
		{db.SMS{ID: 1, Incomplete: true}, true},
		{db.SMS{ID: 1, Content: "complete"}, false},
		{db.SMS{ID: 1, Content: "complete"}, true},
		{db.SMS{ID: 1, Incomplete: true}, true},
		{db.SMS{ID: 2, Incomplete: true}, true},
		{db.SMS{ID: 2}, true},
		{db.SMS{ID: 3}, false},
	} {
		if got := replay.duplicate(sample.sms); got != sample.duplicate {
			t.Fatalf("sms=%+v duplicate=%t want=%t", sample.sms, got, sample.duplicate)
		}
	}
}

func openSMSReplayTestStream(t *testing.T, router http.Handler, token string) *bufio.Reader {
	t.Helper()
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/sms/events?after_id=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	if got := readSSEComment(t, reader); got != ": connected" {
		t.Fatalf("prelude=%q", got)
	}
	return reader
}
