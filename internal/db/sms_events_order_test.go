package db

import (
	"fmt"
	"testing"
	"time"
)

func TestSMSCommitAndPublicationStayOrdered(t *testing.T) {
	for _, firstMultipart := range []bool{false, true} {
		for _, secondMultipart := range []bool{false, true} {
			t.Run(fmt.Sprintf("multipart_%t_%t", firstMultipart, secondMultipart), func(t *testing.T) {
				testSMSPublicationOrder(t, firstMultipart, secondMultipart)
			})
		}
	}
}

func testSMSPublicationOrder(t *testing.T, firstMultipart, secondMultipart bool) {
	openTestDB(t)
	if err := DB.Create(&SIMCard{ICCID: "ICC-ORDER", IMSI: "IMSI-ORDER"}).Error; err != nil {
		t.Fatal(err)
	}
	stream, cancel := SubscribeIncomingSMS()
	defer cancel()
	// Block publication after the first writer commits its row, leaving the DB
	// connection free for another writer. A hub lock alone cannot preserve IDs.
	incomingSMS.mu.Lock()
	locked := true
	defer func() {
		if locked {
			incomingSMS.mu.Unlock()
		}
	}()
	firstDone := make(chan error, 1)
	go func() { firstDone <- saveOrderedTestSMS(firstMultipart, "first") }()
	waitCommittedSMS(t)
	if smsPublicationMu.TryLock() {
		smsPublicationMu.Unlock()
		t.Fatal("committed SMS released writer ordering before event publication")
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- saveOrderedTestSMS(secondMultipart, "second") }()
	incomingSMS.mu.Unlock()
	locked = false
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	first, second := <-stream, <-stream
	if first.Content != "first" || second.Content != "second" || first.ID >= second.ID {
		t.Fatalf("out-of-order publication: first=%+v second=%+v", first, second)
	}
}

func saveOrderedTestSMS(multipart bool, content string) error {
	if multipart {
		_, err := SaveReceivedMultipartSMS(ReceivedMultipartSMS{
			IMSI: "IMSI-ORDER", Sender: "10086", Content: content,
			FragmentSessionKey: content, Timestamp: time.Now(),
		})
		return err
	}
	return SaveSMSForIdentity(SMSRecord{
		Identity: SMSIdentity{ICCID: "ICC-ORDER", IMSI: "IMSI-ORDER"}, Sender: "10086",
		Content: content, Type: 1, Timestamp: time.Now(),
	})
}

func waitCommittedSMS(t *testing.T) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		var count int64
		if err := DB.Model(&SMS{}).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("first SMS did not commit")
		case <-tick.C:
		}
	}
}
