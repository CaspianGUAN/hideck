package db

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// MigrateSIMICCIDIdentities repairs canonical formatting and proven aliases left
// by the decimal-only UIM decoder. The complete repair is atomic and idempotent.
func MigrateSIMICCIDIdentities(database *gorm.DB) error {
	if database == nil {
		return nil
	}
	return database.Transaction(migrateSIMICCIDIdentitiesTx)
}

// If no intact historical ICCID existed at startup, the first corrected live
// read supplies the missing evidence. Later polls avoid rescanning SMS history.
func reconcileLiveICCIDTx(tx *gorm.DB, identity SMSIdentity) error {
	legacy := legacyDecimalICCID(identity.ICCID)
	if legacy == "" || identity.IMSI == "" {
		return nil
	}
	var count int64
	if err := tx.Model(&SIMCard{}).Where("iccid = ? AND imsi = ?", legacy, identity.IMSI).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	return migrateSIMICCIDIdentitiesTx(tx)
}

func migrateSIMICCIDIdentitiesTx(tx *gorm.DB) error {
	aliases, err := loadICCIDIdentityAliases(tx)
	if err != nil {
		return err
	}
	if len(aliases) == 0 {
		return nil
	}
	if err := mergeSIMICCIDRows(tx, aliases); err != nil {
		return err
	}
	for raw, target := range aliases {
		for _, field := range []struct{ table, column string }{
			{"devices", "iccid"}, {"sim_subscriptions", "current_iccid"},
		} {
			if err := tx.Table(field.table).Where(field.column+" = ?", raw).UpdateColumn(field.column, target).Error; err != nil {
				return err
			}
		}
	}
	if err := mergeICCIDAssociatedRows(tx, aliases); err != nil {
		return err
	}
	affected, err := canonicalizeSMSMessageRows(tx, aliases)
	if err != nil {
		return err
	}
	if err := canonicalizeSMSDeliveryRows(tx, aliases); err != nil {
		return err
	}
	return mergeCanonicalSMSContacts(tx, affected, aliases)
}

func mergeSIMICCIDRows(tx *gorm.DB, aliases map[string]string) error {
	var rows []SIMCard
	if err := tx.Find(&rows).Error; err != nil {
		return err
	}
	groups := make(map[string][]SIMCard)
	for _, row := range rows {
		key := resolveICCIDAlias(row.ICCID, aliases)
		groups[key] = append(groups[key], row)
	}
	for key, group := range groups {
		if len(group) == 1 && group[0].ICCID == key {
			continue
		}
		winner, err := mergedSIMICCIDRow(group)
		if err != nil {
			return err
		}
		for _, row := range group {
			if err := tx.Where("iccid = ?", row.ICCID).Delete(&SIMCard{}).Error; err != nil {
				return err
			}
		}
		winner.ICCID = key
		if err := tx.Create(&winner).Error; err != nil {
			return err
		}
	}
	return nil
}

func mergedSIMICCIDRow(rows []SIMCard) (SIMCard, error) {
	winner := rows[0]
	imsi := strings.TrimSpace(winner.IMSI)
	for _, row := range rows[1:] {
		candidate := strings.TrimSpace(row.IMSI)
		if imsi != "" && candidate != "" && candidate != imsi {
			return SIMCard{}, fmt.Errorf("%w: canonical ICCID has conflicting IMSIs", ErrSMSIdentityConflict)
		}
		if candidate != "" {
			imsi = candidate
		}
		if row.LastSeen.After(winner.LastSeen) {
			winner = row
		}
	}
	winner.IMSI = imsi
	mergeMissingSIMCardMetadata(&winner, rows)
	return winner, nil
}

func mergeMissingSIMCardMetadata(winner *SIMCard, rows []SIMCard) {
	for _, row := range rows {
		if blankStringPointer(winner.CurrentIMEI) && !blankStringPointer(row.CurrentIMEI) {
			value := strings.TrimSpace(*row.CurrentIMEI)
			winner.CurrentIMEI = &value
		}
		fillMissingString(&winner.Operator, row.Operator)
		fillMissingString(&winner.RegStatusText, row.RegStatusText)
		fillMissingString(&winner.LAC, row.LAC)
		fillMissingString(&winner.CellID, row.CellID)
		fillMissingString(&winner.APN, row.APN)
		if winner.CreatedAt.IsZero() || (!row.CreatedAt.IsZero() && row.CreatedAt.Before(winner.CreatedAt)) {
			winner.CreatedAt = row.CreatedAt
		}
	}
}

func blankStringPointer(value *string) bool {
	return value == nil || strings.TrimSpace(*value) == ""
}

func fillMissingString(target *string, candidate string) {
	if strings.TrimSpace(*target) == "" && strings.TrimSpace(candidate) != "" {
		*target = candidate
	}
}
