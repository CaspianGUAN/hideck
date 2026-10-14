package db

import (
	"strings"

	"github.com/yibaiba/hideck/pkg/logger"
	"gorm.io/gorm"
)

type iccidIdentityRow struct {
	ICCID string `gorm:"column:iccid"`
	IMSI  string `gorm:"column:imsi"`
}

type iccidCandidateKey struct{ legacy, imsi string }
type iccidCandidates map[iccidCandidateKey]map[string]struct{}

// legacyDecimalICCID reproduces the old decoder only for identifying historical
// rows. It must never be used to normalize a live SIM identity.
func legacyDecimalICCID(value string) string {
	const minICCIDLength, maxICCIDLength = 18, 20
	value = CanonicalICCID(value)
	if len(value) < minICCIDLength || len(value) > maxICCIDLength || !strings.HasPrefix(value, "89") {
		return ""
	}
	var digits strings.Builder
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r >= 'A' && r <= 'F':
		default:
			return ""
		}
	}
	if digits.Len() == len(value) {
		return ""
	}
	return digits.String()
}

func loadICCIDIdentityAliases(tx *gorm.DB) (map[string]string, error) {
	var rows []iccidIdentityRow
	for _, table := range []string{"sim_cards", "sms", "sms_contacts", "sms_delivery"} {
		var part []iccidIdentityRow
		if err := tx.Table(table).Distinct("iccid", "imsi").Find(&part).Error; err != nil {
			return nil, err
		}
		rows = append(rows, part...)
	}
	var subscriptions []iccidIdentityRow
	if err := tx.Model(&SIMSubscription{}).Select("current_iccid AS iccid, imsi").Find(&subscriptions).Error; err != nil {
		return nil, err
	}
	rows = append(rows, subscriptions...)
	aliases := buildICCIDIdentityAliases(rows)
	return filterConflictingICCIDOwningModems(tx, aliases)
}

func filterConflictingICCIDOwningModems(tx *gorm.DB, aliases map[string]string) (map[string]string, error) {
	var cards []SIMCard
	if err := tx.Select("iccid", "current_imei").Find(&cards).Error; err != nil {
		return nil, err
	}
	owners := make(map[string]map[string]struct{})
	for _, card := range cards {
		if card.CurrentIMEI == nil || strings.TrimSpace(*card.CurrentIMEI) == "" {
			continue
		}
		key := resolveICCIDAlias(card.ICCID, aliases)
		if owners[key] == nil {
			owners[key] = make(map[string]struct{})
		}
		owners[key][strings.TrimSpace(*card.CurrentIMEI)] = struct{}{}
	}
	filtered := make(map[string]string, len(aliases))
	for raw, target := range aliases {
		if legacyDecimalICCID(target) == CanonicalICCID(raw) && len(owners[target]) > 1 {
			logger.Sugar.Warn("历史 ICCID 关联到不同模组，保留原记录，请核实 SIM 归属")
			continue
		}
		filtered[raw] = target
	}
	return filtered, nil
}

func historicalICCIDCandidates(rows []iccidIdentityRow) iccidCandidates {
	candidates := make(iccidCandidates)
	for _, row := range rows {
		legacy := legacyDecimalICCID(row.ICCID)
		imsi := strings.TrimSpace(row.IMSI)
		if legacy == "" || imsi == "" {
			continue
		}
		key := iccidCandidateKey{legacy, imsi}
		if candidates[key] == nil {
			candidates[key] = make(map[string]struct{})
		}
		candidates[key][CanonicalICCID(row.ICCID)] = struct{}{}
	}
	return candidates
}

func historicalICCIDTargets(rows []iccidIdentityRow, candidates iccidCandidates) map[string]map[string]struct{} {
	// Every record sharing an old key must independently resolve to the same
	// identity. IMSI equality alone or a matching prefix cannot prove an alias.
	targets := make(map[string]map[string]struct{})
	for _, row := range rows {
		canonical := CanonicalICCID(row.ICCID)
		if canonical == "" {
			continue
		}
		target := canonical
		matches := candidates[iccidCandidateKey{canonical, strings.TrimSpace(row.IMSI)}]
		if len(matches) > 1 {
			logger.Sugar.Warn("历史 ICCID 对应多个完整标识，保留原记录，不自动合并")
		}
		if len(matches) == 1 {
			for match := range matches {
				target = match
			}
		}
		if targets[row.ICCID] == nil {
			targets[row.ICCID] = make(map[string]struct{})
		}
		targets[row.ICCID][target] = struct{}{}
	}
	return targets
}

func buildICCIDIdentityAliases(rows []iccidIdentityRow) map[string]string {
	aliases := make(map[string]string)
	targets := historicalICCIDTargets(rows, historicalICCIDCandidates(rows))
	for raw, choices := range targets {
		if len(choices) != 1 {
			logger.Sugar.Warn("历史 ICCID 存在不同 SIM 身份证据，保留原记录，不自动合并")
			continue
		}
		for target := range choices {
			if raw != target {
				aliases[raw] = target
			}
		}
	}
	return aliases
}

func resolveICCIDAlias(raw string, aliases map[string]string) string {
	if target, ok := aliases[raw]; ok {
		return target
	}
	return CanonicalICCID(raw)
}
