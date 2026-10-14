package device

import (
	"strings"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost/eventhost"
	"github.com/yibaiba/hideck/internal/db"
	"github.com/yibaiba/hideck/internal/smsnotify"
)

const vowifiReceivedSMSDuplicateWindow = 30 * time.Minute

type vowifiSMSHistoryRecorder struct {
	pool *Pool
}

type vowifiSMSRecordResult struct {
	Stored     bool
	Duplicate  bool
	Suppressed bool
	Reconciled bool
}

type receivedMultipartContext struct {
	Identity   SMSIdentity
	LocalPhone string
	Timestamp  time.Time
}

type VoWiFiSMSSendFailure struct {
	DeviceID     string
	Target       string
	Content      string
	Identity     SMSIdentity
	IdentityRead bool
	At           time.Time
}

func RecordVoWiFiSMSSendFailure(p *Pool, failure VoWiFiSMSSendFailure) error {
	return vowifiSMSHistoryRecorder{pool: p}.RecordSendFailure(failure)
}

func (r vowifiSMSHistoryRecorder) resolveIMSI(devID, fallbackIMSI string) string {
	imsi := strings.TrimSpace(fallbackIMSI)
	if imsi != "" {
		return imsi
	}
	if r.pool == nil {
		return ""
	}
	w := r.pool.GetWorker(strings.TrimSpace(devID))
	if w == nil {
		return ""
	}
	if cached := strings.TrimSpace(w.GetCachedIMSI()); cached != "" {
		return cached
	}
	return strings.TrimSpace(w.GetIMSI())
}

func (r vowifiSMSHistoryRecorder) resolveICCID(devID string) string {
	if r.pool == nil {
		return ""
	}
	w := r.pool.GetWorker(strings.TrimSpace(devID))
	if w == nil {
		return ""
	}
	if iccid := strings.TrimSpace(w.GetCachedDeviceStatus().ICCID); iccid != "" {
		return iccid
	}
	iccid, err := db.ResolveICCIDForIMSI(r.resolveIMSI(devID, ""))
	if err != nil || strings.HasPrefix(iccid, "imsi:") {
		return ""
	}
	return strings.TrimSpace(iccid)
}

func (r vowifiSMSHistoryRecorder) resolveReceivingIdentity(devID string) SMSIdentity {
	if r.pool == nil {
		return SMSIdentity{}
	}
	w := r.pool.GetWorker(strings.TrimSpace(devID))
	if w == nil {
		return SMSIdentity{}
	}
	status := w.GetCachedDeviceStatus()
	identity := normalizeSMSIdentity(SMSIdentity{ICCID: status.ICCID, IMSI: status.IMSI})
	if identity.ICCID != "" || identity.IMSI != "" {
		return identity
	}
	// Workers without an initialized identity cache still need the historical
	// backend/DB fallback. Read the live IMSI once and derive its ICCID from
	// persisted identity data so a profile switch cannot produce a mixed pair.
	imsi := r.resolveIMSI(devID, "")
	iccid, err := db.ResolveICCIDForIMSI(imsi)
	if err != nil || strings.HasPrefix(iccid, "imsi:") {
		iccid = ""
	}
	return normalizeSMSIdentity(SMSIdentity{ICCID: iccid, IMSI: imsi})
}

func (r vowifiSMSHistoryRecorder) localPhoneForIdentity(identity SMSIdentity) string {
	phone, _ := db.GetPhoneNumberForSMSIdentity(db.SMSIdentity{
		ICCID: identity.ICCID,
		IMSI:  identity.IMSI,
	})
	return strings.TrimSpace(phone)
}

func (r vowifiSMSHistoryRecorder) resolveOutboundIdentity(devID string, captured SMSIdentity) (SMSIdentity, error) {
	identity := normalizeSMSIdentity(captured)
	if (identity.IMSI == "") != (identity.ICCID == "") {
		return SMSIdentity{}, db.ErrSMSIdentityUnknown
	}
	if identity.IMSI == "" {
		identity = r.resolveReceivingIdentity(devID)
	}
	if identity.ICCID == "" && identity.IMSI != "" {
		iccid, err := db.ResolveICCIDForIMSI(identity.IMSI)
		if err != nil {
			return SMSIdentity{}, err
		}
		identity.ICCID = iccid
	}
	return normalizeSMSIdentity(identity), nil
}

func (r vowifiSMSHistoryRecorder) eventTime(at time.Time) time.Time {
	if at.IsZero() {
		return time.Now()
	}
	return at
}

func (r vowifiSMSHistoryRecorder) RecordReceived(e eventhost.SMSReceived) (vowifiSMSRecordResult, error) {
	return r.recordReceived(e, r.resolveReceivingIdentity(e.DevID))
}

func (r vowifiSMSHistoryRecorder) recordReceived(
	e eventhost.SMSReceived,
	identity SMSIdentity,
) (vowifiSMSRecordResult, error) {
	if smsnotify.ShouldSuppressReceivedSMS(e.Content) {
		return vowifiSMSRecordResult{Suppressed: true}, nil
	}
	identity = normalizeSMSIdentity(identity)
	if identity.IMSI == "" {
		return vowifiSMSRecordResult{}, nil
	}
	if identity.ICCID == "" {
		identity.ICCID = db.GetICCIDForIMSI(identity.IMSI)
	}
	localPhone := r.localPhoneForIdentity(identity)
	ts := r.eventTime(e.Time)
	if strings.TrimSpace(e.FragmentSessionKey) != "" {
		return r.recordReceivedMultipart(e, receivedMultipartContext{
			Identity: identity, LocalPhone: localPhone, Timestamp: ts,
		})
	}

	dup, err := db.HasDuplicateReceivedSMSForIdentity(db.ReceivedSMSDuplicate{
		Identity:   db.SMSIdentity{ICCID: identity.ICCID, IMSI: identity.IMSI},
		LocalPhone: localPhone, Sender: e.Sender, Recipient: localPhone, Content: e.Content,
		Timestamp: ts, Window: vowifiReceivedSMSDuplicateWindow,
	})
	if err != nil {
		return vowifiSMSRecordResult{}, err
	}
	if dup {
		return vowifiSMSRecordResult{Duplicate: true}, nil
	}

	err = db.SaveSMSForIdentity(db.SMSRecord{
		Identity:   db.SMSIdentity{ICCID: identity.ICCID, IMSI: identity.IMSI},
		LocalPhone: localPhone, Sender: strings.TrimSpace(e.Sender), Recipient: localPhone,
		Content: e.Content, Type: 1, Status: 0, Timestamp: ts,
	})
	if err != nil {
		return vowifiSMSRecordResult{}, err
	}
	return vowifiSMSRecordResult{Stored: true}, nil
}

func (r vowifiSMSHistoryRecorder) recordReceivedMultipart(
	e eventhost.SMSReceived,
	ctx receivedMultipartContext,
) (vowifiSMSRecordResult, error) {
	result, err := db.SaveReceivedMultipartSMS(db.ReceivedMultipartSMS{
		ICCID: ctx.Identity.ICCID, IMSI: ctx.Identity.IMSI,
		LocalPhone: ctx.LocalPhone, Sender: strings.TrimSpace(e.Sender),
		Recipient: ctx.LocalPhone, Content: e.Content, Timestamp: ctx.Timestamp,
		FragmentSessionKey: strings.TrimSpace(e.FragmentSessionKey),
		Incomplete:         e.Incomplete,
	})
	if err != nil {
		return vowifiSMSRecordResult{}, err
	}
	return vowifiSMSRecordResult{
		Stored:    result.Created || result.Reconciled,
		Duplicate: result.Duplicate, Reconciled: result.Reconciled,
	}, nil
}

func (r vowifiSMSHistoryRecorder) RecordSent(e eventhost.SMSSent) error {
	identity, err := r.resolveOutboundIdentity(e.DevID, SMSIdentity{ICCID: e.ICCID, IMSI: e.IMSI})
	if err != nil {
		return err
	}
	if identity.ICCID == "" || identity.IMSI == "" {
		return db.ErrSMSIdentityUnknown
	}
	localPhone := r.localPhoneForIdentity(identity)
	return db.SaveSMSForIdentity(db.SMSRecord{
		Identity:   db.SMSIdentity{ICCID: identity.ICCID, IMSI: identity.IMSI},
		LocalPhone: localPhone, Sender: localPhone, Recipient: strings.TrimSpace(e.TargetURI),
		Content: e.Content, Type: 2, Status: 2, Timestamp: r.eventTime(e.Time),
	})
}

func (r vowifiSMSHistoryRecorder) RecordSendFailure(failure VoWiFiSMSSendFailure) error {
	if failure.IdentityRead && normalizeSMSIdentity(failure.Identity) == (SMSIdentity{}) {
		return db.ErrSMSIdentityUnknown
	}
	identity, err := r.resolveOutboundIdentity(failure.DeviceID, failure.Identity)
	if err != nil {
		return err
	}
	if identity.ICCID == "" || identity.IMSI == "" {
		return db.ErrSMSIdentityUnknown
	}
	localPhone := r.localPhoneForIdentity(identity)
	return db.SaveSMSForIdentity(db.SMSRecord{
		Identity:   db.SMSIdentity{ICCID: identity.ICCID, IMSI: identity.IMSI},
		LocalPhone: localPhone, Sender: localPhone, Recipient: strings.TrimSpace(failure.Target),
		Content: failure.Content, Type: 2, Status: 3, Timestamp: r.eventTime(failure.At),
	})
}

func (r vowifiSMSHistoryRecorder) RecordLocalNumberLearned(e eventhost.LocalNumberLearned) error {
	imsi := r.resolveIMSI(e.DevID, e.IMSI)
	number := strings.TrimSpace(e.Number)
	if number == "" {
		return nil
	}
	iccid := r.resolveICCID(e.DevID)
	if imsi == "" && iccid == "" {
		return nil
	}
	return db.RecordVoWiFiPhoneNumber(imsi, iccid, number)
}
