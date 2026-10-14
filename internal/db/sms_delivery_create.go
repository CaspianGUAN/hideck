package db

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm/clause"
)

type SMSDeliveryCreate struct {
	MessageID  string
	ICCID      string
	IMSI       string
	DeviceID   string
	Peer       string
	Content    string
	PartsTotal int
	At         time.Time
}

func CreateSMSDelivery(input SMSDeliveryCreate) error {
	if DB == nil {
		return nil
	}
	messageID := strings.TrimSpace(input.MessageID)
	if messageID == "" {
		return errors.New("message_id 不能为空")
	}
	if input.At.IsZero() {
		input.At = time.Now()
	}
	identity, err := resolveSMSDeliveryIdentity(input)
	if err != nil {
		return err
	}
	row := SMSDelivery{
		MessageID: messageID, IMSI: identity.IMSI, ICCID: identity.ICCID,
		DeviceID: strings.TrimSpace(input.DeviceID), Peer: strings.TrimSpace(input.Peer),
		Content: input.Content, PartsTotal: input.PartsTotal, State: SMSDeliveryStatePending,
		CreatedAt: input.At, UpdatedAt: input.At,
	}
	return DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "message_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"imsi", "iccid", "device_id", "peer", "content", "parts_total", "state", "last_error", "updated_at"}),
	}).Create(&row).Error
}

func resolveSMSDeliveryIdentity(input SMSDeliveryCreate) (SMSIdentity, error) {
	captured := SMSIdentity{ICCID: CanonicalICCID(input.ICCID), IMSI: strings.TrimSpace(input.IMSI)}
	if captured.ICCID != "" {
		if captured.IMSI == "" {
			return SMSIdentity{}, ErrSMSIdentityUnknown
		}
		if err := validateSMSIdentityMapping(DB, captured); err != nil {
			return SMSIdentity{}, err
		}
		return captured, nil
	}
	identity, found, err := LookupDeviceSMSIdentity(strings.TrimSpace(input.DeviceID))
	if err != nil {
		return SMSIdentity{}, err
	}
	if found {
		return mergeDeliveryIdentity(identity, captured.IMSI)
	}
	iccid, err := ResolveICCIDForIMSI(captured.IMSI)
	if err != nil {
		return SMSIdentity{}, err
	}
	return SMSIdentity{ICCID: iccid, IMSI: captured.IMSI}, nil
}

func mergeDeliveryIdentity(identity SMSIdentity, runtimeIMSI string) (SMSIdentity, error) {
	if identity.IMSI != "" && runtimeIMSI != "" && identity.IMSI != runtimeIMSI {
		return SMSIdentity{}, fmt.Errorf("%w: device binding IMSI does not match IMS runtime", ErrSMSIdentityConflict)
	}
	if identity.IMSI == "" {
		identity.IMSI = runtimeIMSI
	}
	return identity, nil
}
