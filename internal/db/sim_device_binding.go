package db

import (
	"strings"
	"time"

	"gorm.io/gorm"
)

type simDeviceBinding struct {
	ICCID string
	IMEI  string
	Now   time.Time
}

func normalizedIMEIPointer(value *string) *string {
	if value == nil {
		return nil
	}
	normalized := strings.TrimSpace(*value)
	if normalized == "" {
		return nil
	}
	return &normalized
}

func reconcileSIMDeviceBindingTx(tx *gorm.DB, binding simDeviceBinding) error {
	binding.ICCID = CanonicalICCID(binding.ICCID)
	binding.IMEI = strings.TrimSpace(binding.IMEI)
	if binding.Now.IsZero() {
		binding.Now = time.Now()
	}
	if binding.ICCID == "" {
		return detachDeviceSIMTx(tx, binding.IMEI, binding.Now)
	}
	if binding.IMEI == "" {
		return detachSIMFromDevicesTx(tx, binding.ICCID, binding.Now)
	}
	if err := clearPreviousSIMOwnersTx(tx, binding); err != nil {
		return err
	}
	if err := clearPreviousDeviceSIMTx(tx, binding); err != nil {
		return err
	}
	if err := tx.Model(&SIMCard{}).Where("iccid = ?", binding.ICCID).
		Updates(map[string]any{"current_imei": binding.IMEI, "updated_at": binding.Now}).Error; err != nil {
		return err
	}
	return tx.Model(&Device{}).Where("imei = ?", binding.IMEI).
		Updates(map[string]any{"iccid": binding.ICCID, "last_seen": binding.Now}).Error
}

func clearPreviousSIMOwnersTx(tx *gorm.DB, binding simDeviceBinding) error {
	return tx.Model(&Device{}).
		Where("iccid = ? AND imei <> ?", binding.ICCID, binding.IMEI).
		Updates(map[string]any{"iccid": nil, "updated_at": binding.Now}).Error
}

func clearPreviousDeviceSIMTx(tx *gorm.DB, binding simDeviceBinding) error {
	return tx.Model(&SIMCard{}).
		Where("current_imei = ? AND iccid <> ?", binding.IMEI, binding.ICCID).
		Updates(map[string]any{"current_imei": nil, "updated_at": binding.Now}).Error
}

func detachSIMFromDevicesTx(tx *gorm.DB, iccid string, now time.Time) error {
	if err := tx.Model(&Device{}).Where("iccid = ?", iccid).
		Updates(map[string]any{"iccid": nil, "updated_at": now}).Error; err != nil {
		return err
	}
	return tx.Model(&SIMCard{}).Where("iccid = ?", iccid).
		Updates(map[string]any{"current_imei": nil, "updated_at": now}).Error
}

func detachDeviceSIMTx(tx *gorm.DB, imei string, now time.Time) error {
	if imei == "" {
		return nil
	}
	if err := tx.Model(&SIMCard{}).Where("current_imei = ?", imei).
		Updates(map[string]any{"current_imei": nil, "updated_at": now}).Error; err != nil {
		return err
	}
	return tx.Model(&Device{}).Where("imei = ?", imei).
		Updates(map[string]any{"iccid": nil, "updated_at": now}).Error
}

// DetachDeviceAlias clears current SIM ownership without deleting device or SMS history.
func DetachDeviceAlias(alias string) error {
	alias = strings.TrimSpace(alias)
	if DB == nil || alias == "" {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var devices []Device
		if err := tx.Select("imei").Where("alias = ?", alias).Find(&devices).Error; err != nil {
			return err
		}
		for _, device := range devices {
			if err := detachDeviceSIMTx(tx, device.IMEI, time.Now()); err != nil {
				return err
			}
		}
		return nil
	})
}
