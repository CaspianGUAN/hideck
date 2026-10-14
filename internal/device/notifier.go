package device

import "time"

// Notifier 定义了设备池需要的事件通知接口，
// 用于解耦具体的通知实现（如 Telegram 或 Webhook）
type Notifier interface {
	NotifySMS(deviceID, sender, content string, timestamp time.Time)
	NotifyIPRotated(deviceID, oldIP, newIP string, duration time.Duration)
	NotifyRaw(msg string)
}

type SMSSourceNotifier interface {
	NotifySMSWithSource(deviceID, sender, content, source string, timestamp time.Time)
}

// SMSReceiptNotifier also receives the SIM that got the message, so a
// notification can name the local number when one device switches profiles.
type SMSReceiptNotifier interface {
	NotifySMSReceipt(sms InboundSMS, source string)
}

// notifySMS uses the richest interface the notifier implements. An empty
// source means cellular.
func notifySMS(notifier Notifier, sms InboundSMS, source string) {
	if receipt, ok := notifier.(SMSReceiptNotifier); ok {
		receipt.NotifySMSReceipt(sms, source)
		return
	}
	if withSource, ok := notifier.(SMSSourceNotifier); ok && source != "" {
		withSource.NotifySMSWithSource(sms.DeviceID, sms.Sender, sms.Content, source, sms.Time)
		return
	}
	notifier.NotifySMS(sms.DeviceID, sms.Sender, sms.Content, sms.Time)
}

// IncomingCallNotifier is implemented by host notifiers that can surface a ring.
type IncomingCallNotifier interface {
	NotifyIncomingCall(deviceID, caller, callee string)
}
