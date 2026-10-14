package device

import (
	"testing"
	"time"
)

type receiptNotifierStub struct {
	plainCalls int
	receipts   []InboundSMS
	sources    []string
}

func (n *receiptNotifierStub) NotifySMS(string, string, string, time.Time)           { n.plainCalls++ }
func (n *receiptNotifierStub) NotifyIPRotated(string, string, string, time.Duration) {}
func (n *receiptNotifierStub) NotifyRaw(string)                                      {}
func (n *receiptNotifierStub) NotifySMSReceipt(sms InboundSMS, source string) {
	n.receipts = append(n.receipts, sms)
	n.sources = append(n.sources, source)
}

func TestNotifySMSPassesTheReceivingSIM(t *testing.T) {
	n := &receiptNotifierStub{}
	sms := InboundSMS{DeviceID: "wwan0", ICCID: "8944000000000001234", IMSI: "234870000000001", Sender: "MontSMS"}
	notifySMS(n, sms, "VoWiFi")
	if n.plainCalls != 0 || len(n.receipts) != 1 || n.receipts[0] != sms || n.sources[0] != "VoWiFi" {
		t.Fatalf("plain=%d receipts=%+v sources=%v", n.plainCalls, n.receipts, n.sources)
	}
}
