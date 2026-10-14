package db

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	legacySMSInboundFragmentIndex = "idx_sms_inbound_fragment_scope"
	smsInboundFragmentICCIDIndex  = "idx_sms_inbound_fragment_iccid_scope"
)

// MigrateSMSInboundFragmentIdentity makes persisted multipart ownership follow
// the physical SIM instead of the device slot where the first part arrived.
func MigrateSMSInboundFragmentIdentity(database *gorm.DB) error {
	if database == nil || !database.Migrator().HasTable(&SMSInboundFragment{}) {
		return nil
	}
	return database.Transaction(func(tx *gorm.DB) error {
		if err := backfillSMSInboundFragmentICCID(tx); err != nil {
			return err
		}
		if err := reconcileSMSInboundFragmentDuplicates(tx); err != nil {
			return err
		}
		if tx.Migrator().HasIndex(&SMSInboundFragment{}, legacySMSInboundFragmentIndex) {
			if err := tx.Migrator().DropIndex(&SMSInboundFragment{}, legacySMSInboundFragmentIndex); err != nil {
				return err
			}
		}
		return tx.Exec(
			"CREATE UNIQUE INDEX IF NOT EXISTS " + smsInboundFragmentICCIDIndex +
				" ON sms_inbound_fragment(iccid, imsi, session_key, sequence)",
		).Error
	})
}

func backfillSMSInboundFragmentICCID(tx *gorm.DB) error {
	fromDevice := `UPDATE sms_inbound_fragment
		SET iccid = (SELECT MIN(d.iccid) FROM devices d
			WHERE d.alias = sms_inbound_fragment.device_id
			AND COALESCE(TRIM(d.iccid), '') <> '')
		WHERE COALESCE(TRIM(iccid), '') = ''
		AND (SELECT COUNT(DISTINCT d.iccid) FROM devices d
			WHERE d.alias = sms_inbound_fragment.device_id
			AND COALESCE(TRIM(d.iccid), '') <> '') = 1`
	if err := tx.Exec(fromDevice).Error; err != nil {
		return err
	}
	fromUniqueIMSI := `UPDATE sms_inbound_fragment
		SET iccid = (SELECT MIN(s.iccid) FROM sim_cards s
			WHERE s.imsi = sms_inbound_fragment.imsi)
		WHERE COALESCE(TRIM(iccid), '') = ''
		AND (SELECT COUNT(DISTINCT s.iccid) FROM sim_cards s
			WHERE s.imsi = sms_inbound_fragment.imsi) = 1`
	if err := tx.Exec(fromUniqueIMSI).Error; err != nil {
		return err
	}
	return tx.Exec(`UPDATE sms_inbound_fragment
		SET iccid = 'legacy:' || device_id || ':' || imsi
		WHERE COALESCE(TRIM(iccid), '') = ''`).Error
}

type inboundFragmentMigrationKey struct {
	iccid, imsi, session string
	sequence             int
}

func reconcileSMSInboundFragmentDuplicates(tx *gorm.DB) error {
	var rows []SMSInboundFragment
	if err := tx.Order("id ASC").Find(&rows).Error; err != nil {
		return err
	}
	seen := make(map[inboundFragmentMigrationKey]SMSInboundFragment, len(rows))
	for _, row := range rows {
		key := inboundFragmentMigrationKey{row.ICCID, row.IMSI, row.SessionKey, row.Sequence}
		kept, exists := seen[key]
		if !exists {
			seen[key] = row
			continue
		}
		if !sameInboundFragmentPayload(kept, row) {
			return fmt.Errorf("%w during ICCID migration: session=%s sequence=%d", ErrSMSInboundFragmentCollision, row.SessionKey, row.Sequence)
		}
		merged := mergeInboundFragmentLifecycle(kept, row)
		if err := tx.Model(&SMSInboundFragment{}).Where("id = ?", kept.ID).Updates(merged).Error; err != nil {
			return err
		}
		if err := tx.Delete(&SMSInboundFragment{}, row.ID).Error; err != nil {
			return err
		}
		kept.AckSent, kept.AckSentAt = merged.AckSent, merged.AckSentAt
		kept.DegradedAt, kept.ArrivedAt = merged.DegradedAt, merged.ArrivedAt
		seen[key] = kept
	}
	return nil
}

func sameInboundFragmentPayload(a, b SMSInboundFragment) bool {
	return a.Total == b.Total && strings.TrimSpace(a.Content) == strings.TrimSpace(b.Content)
}

type inboundFragmentLifecycle struct {
	AckSent    bool      `gorm:"column:ack_sent"`
	AckSentAt  time.Time `gorm:"column:ack_sent_at"`
	DegradedAt time.Time `gorm:"column:degraded_at"`
	ArrivedAt  time.Time `gorm:"column:arrived_at"`
}

func mergeInboundFragmentLifecycle(a, b SMSInboundFragment) inboundFragmentLifecycle {
	return inboundFragmentLifecycle{
		AckSent:    a.AckSent || b.AckSent,
		AckSentAt:  firstNonZeroTime(a.AckSentAt, b.AckSentAt),
		DegradedAt: firstNonZeroTime(a.DegradedAt, b.DegradedAt),
		ArrivedAt:  firstNonZeroTime(a.ArrivedAt, b.ArrivedAt),
	}
}

func firstNonZeroTime(a, b time.Time) time.Time {
	if a.IsZero() || (!b.IsZero() && b.Before(a)) {
		return b
	}
	return a
}
