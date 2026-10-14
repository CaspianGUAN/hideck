package db

import (
	"sort"
	"strings"

	"github.com/yibaiba/hideck/internal/mbn"
	"gorm.io/gorm"
)

func mergeICCIDAssociatedRows(tx *gorm.DB, aliases map[string]string) error {
	for _, migrate := range []func(*gorm.DB, map[string]string) error{
		mergeAliasedCardPolicies, mergeAliasedPendingPhones, mergeAliasedOutboundUsage, migrateAliasedMBN,
	} {
		if err := migrate(tx, aliases); err != nil {
			return err
		}
	}
	for raw, target := range aliases {
		for _, table := range []string{"voice_call_records", "balance_queries"} {
			if err := tx.Table(table).Where("iccid = ?", raw).UpdateColumn("iccid", target).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func mergeAliasedCardPolicies(tx *gorm.DB, aliases map[string]string) error {
	var rows []CardPolicy
	if err := tx.Find(&rows).Error; err != nil {
		return err
	}
	groups := make(map[string][]CardPolicy)
	for _, row := range rows {
		key := resolveICCIDAlias(row.ICCID, aliases)
		groups[key] = append(groups[key], row)
	}
	for key, group := range groups {
		if len(group) == 1 && group[0].ICCID == key {
			continue
		}
		winner := group[0]
		for _, row := range group {
			if betterCardPolicyRow(row, winner) {
				winner = row
			}
			if err := tx.Where("iccid = ?", row.ICCID).Delete(&CardPolicy{}).Error; err != nil {
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

func mergeAliasedPendingPhones(tx *gorm.DB, aliases map[string]string) error {
	var rows []PendingPhoneNumber
	if err := tx.Find(&rows).Error; err != nil {
		return err
	}
	groups := make(map[string][]PendingPhoneNumber)
	for _, row := range rows {
		key := resolveICCIDAlias(row.ICCID, aliases)
		groups[key] = append(groups[key], row)
	}
	for key, group := range groups {
		if len(group) == 1 && group[0].ICCID == key {
			continue
		}
		sort.SliceStable(group, func(i, j int) bool { return group[i].UpdatedAt.After(group[j].UpdatedAt) })
		winner := group[0]
		for _, row := range group {
			if winner.PhoneNumber == "" {
				winner.PhoneNumber = row.PhoneNumber
			}
			if winner.ModemPhoneNumber == "" {
				winner.ModemPhoneNumber = row.ModemPhoneNumber
			}
			if winner.VowifiPhoneNumber == "" {
				winner.VowifiPhoneNumber = row.VowifiPhoneNumber
			}
			if err := tx.Where("iccid = ?", row.ICCID).Delete(&PendingPhoneNumber{}).Error; err != nil {
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

func mergeAliasedOutboundUsage(tx *gorm.DB, aliases map[string]string) error {
	var rows []OutboundUsage
	if err := tx.Find(&rows).Error; err != nil {
		return err
	}
	type usageKey struct{ iccid, kind string }
	groups := make(map[usageKey][]OutboundUsage)
	for _, row := range rows {
		key := usageKey{resolveICCIDAlias(row.ICCID, aliases), row.Kind}
		groups[key] = append(groups[key], row)
	}
	for key, group := range groups {
		if len(group) == 1 && group[0].ICCID == key.iccid {
			continue
		}
		winner := OutboundUsage{ICCID: key.iccid, Kind: key.kind}
		for _, row := range group {
			winner.Events = append(winner.Events, row.Events...)
			if err := tx.Where("iccid = ? AND kind = ?", row.ICCID, row.Kind).Delete(&OutboundUsage{}).Error; err != nil {
				return err
			}
		}
		sort.SliceStable(winner.Events, func(i, j int) bool { return winner.Events[i].At.Before(winner.Events[j].At) })
		if err := tx.Create(&winner).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateAliasedMBN(tx *gorm.DB, aliases map[string]string) error {
	var policies []mbn.Policy
	if err := tx.Find(&policies).Error; err != nil {
		return err
	}
	groups := make(map[string][]mbn.Policy)
	for _, row := range policies {
		if !strings.HasPrefix(row.Key, "sim:") {
			continue
		}
		key := "sim:" + resolveICCIDAlias(strings.TrimPrefix(row.Key, "sim:"), aliases)
		groups[key] = append(groups[key], row)
	}
	for key, group := range groups {
		if len(group) == 1 && group[0].Key == key {
			continue
		}
		winner := group[0]
		for _, row := range group {
			if row.UpdatedAt.After(winner.UpdatedAt) {
				winner = row
			}
			if err := tx.Where("key = ?", row.Key).Delete(&mbn.Policy{}).Error; err != nil {
				return err
			}
		}
		winner.Key = key
		if err := tx.Create(&winner).Error; err != nil {
			return err
		}
	}
	return migrateAliasedMBNOwners(tx, aliases)
}

func migrateAliasedMBNOwners(tx *gorm.DB, aliases map[string]string) error {
	var operations []mbn.Operation
	if err := tx.Find(&operations).Error; err != nil {
		return err
	}
	for _, op := range operations {
		changed := false
		for _, owner := range []*mbn.Owner{&op.ExpectedOwner, &op.Original.Owner, &op.Current.Owner, op.Request.ExpectedOwner} {
			if owner == nil {
				continue
			}
			canonical := resolveICCIDAlias(owner.ICCID, aliases)
			if canonical != owner.ICCID {
				owner.ICCID = canonical
				changed = true
			}
		}
		if changed {
			// Correct only identity spelling; do not change outcome or replay writes.
			if err := tx.Model(&op).Select("expected_owner", "original", "current", "request").UpdateColumns(op).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
