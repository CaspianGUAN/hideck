package db

import (
	"strings"
	"time"
)

type ReceivedSMSDuplicate struct {
	Identity   SMSIdentity
	LocalPhone string
	Sender     string
	Recipient  string
	Content    string
	Timestamp  time.Time
	Window     time.Duration
}

// HasDuplicateReceivedSMS checks for a matching inbound message around the
// supplied timestamp. Callers with an ICCID snapshot should use the identity form.
func HasDuplicateReceivedSMS(
	imsi, localPhone, sender, recipient, content string,
	timestamp time.Time,
	window time.Duration,
) (bool, error) {
	return HasDuplicateReceivedSMSForIdentity(ReceivedSMSDuplicate{
		Identity:   SMSIdentity{IMSI: imsi, ICCID: GetICCIDForIMSI(imsi)},
		LocalPhone: localPhone, Sender: sender, Recipient: recipient, Content: content,
		Timestamp: timestamp, Window: window,
	})
}

// HasDuplicateReceivedSMSForIdentity keeps de-duplication pinned to the SIM
// snapshot that accepted the message, even if the modem switches profiles.
func HasDuplicateReceivedSMSForIdentity(input ReceivedSMSDuplicate) (bool, error) {
	if DB == nil {
		return false, nil
	}
	identity := normalizeDuplicateIdentity(input.Identity)
	sender := strings.TrimSpace(input.Sender)
	content := strings.TrimSpace(input.Content)
	if identity.ICCID == "" || identity.IMSI == "" || sender == "" || content == "" {
		return false, nil
	}
	window := input.Window
	if window <= 0 {
		window = 5 * time.Minute
	}
	ts := input.Timestamp.Truncate(time.Second)
	var count int64
	err := DB.Model(&SMS{}).Where(
		"iccid = ? AND imsi = ? AND type = ? AND sender = ? AND recipient = ? AND content = ? AND timestamp BETWEEN ? AND ?",
		identity.ICCID, identity.IMSI, 1, sender, strings.TrimSpace(input.Recipient), content,
		ts.Add(-window), ts.Add(window),
	).Count(&count).Error
	return count > 0, err
}

func normalizeDuplicateIdentity(identity SMSIdentity) SMSIdentity {
	identity.ICCID = CanonicalICCID(identity.ICCID)
	identity.IMSI = strings.TrimSpace(identity.IMSI)
	return identity
}
