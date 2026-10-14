package db

import (
	"context"
	"testing"
	"time"
)

func TestSaveSMSForIdentityPublishesIncomingSMS(t *testing.T) {
	openTestDB(t)
	if err := DB.Create(&SIMCard{ICCID: "ICC-EVENT", IMSI: "IMSI-EVENT"}).Error; err != nil {
		t.Fatal(err)
	}
	stream, cancel := SubscribeIncomingSMS()
	defer cancel()
	if err := SaveSMSForIdentity(SMSRecord{
		Identity: SMSIdentity{ICCID: "ICC-EVENT", IMSI: "IMSI-EVENT"},
		Sender:   "+10086", Content: "code 123456", Type: 1, Status: 0, Timestamp: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	sms := waitIncomingSMSEvent(t, stream, time.Second, func(sms SMS) bool {
		return sms.Content == "code 123456" && sms.IMSI == "IMSI-EVENT"
	})
	if sms.ID == 0 || sms.Type != smsTypeIncoming {
		t.Fatalf("published SMS=%+v", sms)
	}
}

func TestSaveSMSForIdentityDoesNotPublishOutbound(t *testing.T) {
	openTestDB(t)
	if err := DB.Create(&SIMCard{ICCID: "ICC-OUT", IMSI: "IMSI-OUT"}).Error; err != nil {
		t.Fatal(err)
	}
	stream, cancel := SubscribeIncomingSMS()
	defer cancel()
	if err := SaveSMSForIdentity(SMSRecord{
		Identity: SMSIdentity{ICCID: "ICC-OUT", IMSI: "IMSI-OUT"},
		Sender:   "+10086", Recipient: "+86138", Content: "outbound-should-not-publish", Type: 2, Status: 2, Timestamp: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(50 * time.Millisecond)
	for {
		select {
		case sms := <-stream:
			if sms.Content == "outbound-should-not-publish" && sms.IMSI == "IMSI-OUT" {
				t.Fatalf("unexpected incoming event %+v", sms)
			}
		case <-deadline:
			return
		}
	}
}

func TestListIncomingUnreadSMSAfterSkipsReadAndOlder(t *testing.T) {
	openTestDB(t)
	old := createNotificationTestSMS(t, SMS{Type: 1, Status: 0, Content: "old"})
	_ = createNotificationTestSMS(t, SMS{Type: 1, Status: 1, Content: "read"})
	unread := createNotificationTestSMS(t, SMS{Type: 1, Status: 0, Content: "new"})
	rows, err := ListIncomingUnreadSMSAfter(context.Background(), old.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != unread.ID || rows[0].Content != "new" {
		t.Fatalf("backlog=%+v", rows)
	}
}

func TestIncomingSMSReplayBoundaryAndFilters(t *testing.T) {
	openTestDB(t)
	ctx := context.Background()
	if cursor, err := IncomingSMSCursor(ctx); err != nil || cursor != 0 {
		t.Fatalf("empty cursor=%d err=%v", cursor, err)
	}
	old := createNotificationTestSMS(t, SMS{Type: 1, Content: "old"})
	_ = createNotificationTestSMS(t, SMS{Type: 1, Status: 1, Content: "read"})
	want := createNotificationTestSMS(t, SMS{Type: 1, Content: "unread"})
	_ = createNotificationTestSMS(t, SMS{Type: 2, Content: "outbound"})
	throughID, err := IncomingSMSCursor(ctx)
	if err != nil || throughID != want.ID {
		t.Fatalf("cursor=%d want=%d err=%v", throughID, want.ID, err)
	}
	_ = createNotificationTestSMS(t, SMS{Type: 1, Content: "later"})
	rows, err := ListIncomingUnreadSMS(ctx, IncomingSMSBacklogQuery{AfterID: old.ID, ThroughID: &throughID})
	if err != nil || len(rows) != 1 || rows[0].ID != want.ID {
		t.Fatalf("backlog=%+v err=%v", rows, err)
	}
	zero := uint(0)
	rows, err = ListIncomingUnreadSMS(ctx, IncomingSMSBacklogQuery{ThroughID: &zero})
	if err != nil || len(rows) != 0 {
		t.Fatalf("empty boundary backlog=%+v err=%v", rows, err)
	}
}

func TestSaveReceivedMultipartSMSPublishesOnCreateAndReconcile(t *testing.T) {
	openTestDB(t)
	if err := DB.Create(&SIMCard{ICCID: "ICC-MULTI", IMSI: "IMSI-MULTI"}).Error; err != nil {
		t.Fatal(err)
	}
	stream, cancel := SubscribeIncomingSMS()
	defer cancel()
	created, err := SaveReceivedMultipartSMS(ReceivedMultipartSMS{
		IMSI: "IMSI-MULTI", Sender: "10086", Content: "part-1",
		FragmentSessionKey: "session-1", Timestamp: time.Now(), Incomplete: true,
	})
	if err != nil || !created.Created {
		t.Fatalf("create result=%+v err=%v", created, err)
	}
	createdEvent := waitIncomingSMSEvent(t, stream, time.Second, func(sms SMS) bool {
		return sms.ID == created.SMSID
	})
	if !createdEvent.Incomplete {
		t.Fatalf("created event=%+v", createdEvent)
	}
	complete, err := SaveReceivedMultipartSMS(ReceivedMultipartSMS{
		IMSI: "IMSI-MULTI", Sender: "10086", Content: "part-1 part-2",
		FragmentSessionKey: "session-1", Timestamp: time.Now(), Incomplete: false,
	})
	if err != nil || !complete.Reconciled {
		t.Fatalf("reconcile result=%+v err=%v", complete, err)
	}
	completeEvent := waitIncomingSMSEvent(t, stream, time.Second, func(sms SMS) bool {
		return sms.ID == complete.SMSID && !sms.Incomplete && sms.Content == "part-1 part-2"
	})
	if completeEvent.ID != complete.SMSID {
		t.Fatalf("reconciled event=%+v", completeEvent)
	}
}

func waitIncomingSMSEvent(t *testing.T, stream <-chan SMS, timeout time.Duration, match func(SMS) bool) SMS {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case sms := <-stream:
			if match(sms) {
				return sms
			}
		case <-deadline:
			t.Fatal("timed out waiting for incoming SMS event")
		}
	}
}
