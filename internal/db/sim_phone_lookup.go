package db

import (
	"errors"
	"strings"

	"gorm.io/gorm"
)

// GetPhoneNumberForSMSIdentity prefers the captured ICCID over an IMSI that
// may be reused across profiles. It falls back to IMSI only without an exact hit.
func GetPhoneNumberForSMSIdentity(identity SMSIdentity) (string, error) {
	iccid := CanonicalICCID(identity.ICCID)
	if phone, err := getSIMSubscriptionPhoneNumberByICCID(iccid); err != nil || phone != "" {
		return phone, err
	}
	if phone, err := getPendingPhoneNumberByICCID(iccid); err != nil || phone != "" {
		return phone, err
	}
	return getMatchingIMSIPhoneNumber(strings.TrimSpace(identity.IMSI), iccid)
}

func getMatchingIMSIPhoneNumber(imsi, capturedICCID string) (string, error) {
	if DB == nil || imsi == "" {
		return "", nil
	}
	var subscription SIMSubscription
	err := DB.Select("phone_number", "current_iccid").Where(
		"imsi = ? AND COALESCE(phone_number, '') <> ''", imsi,
	).Limit(1).Take(&subscription).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	boundICCID := CanonicalICCID(subscription.CurrentICCID)
	if capturedICCID != "" && boundICCID != "" && boundICCID != capturedICCID {
		return "", nil
	}
	return strings.TrimSpace(subscription.PhoneNumber), nil
}

func getPendingPhoneNumberByICCID(iccid string) (string, error) {
	if DB == nil || iccid == "" {
		return "", nil
	}
	var pending PendingPhoneNumber
	err := DB.Select("phone_number").
		Where("iccid = ? AND COALESCE(phone_number, '') <> ''", iccid).
		Limit(1).Take(&pending).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	return strings.TrimSpace(pending.PhoneNumber), err
}
