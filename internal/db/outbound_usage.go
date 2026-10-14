package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/yibaiba/hideck/internal/outbound"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// One bounded rolling history per SIM and traffic kind; no phone numbers or
// message contents are stored. ICCID survives modem/mode changes and restarts.
type OutboundUsage struct {
	ICCID  string           `gorm:"column:iccid;primaryKey"`
	Kind   string           `gorm:"primaryKey"`
	Events []outbound.Event `gorm:"serializer:json"`
}

type OutboundUsageStore struct{ db *gorm.DB }

func NewOutboundUsageStore(database *gorm.DB) *OutboundUsageStore {
	return &OutboundUsageStore{db: database}
}

func (s *OutboundUsageStore) Update(ctx context.Context, key outbound.Key, apply func([]outbound.Event) ([]outbound.Event, error)) error {
	if s.db == nil {
		return errors.New("外发额度数据库未初始化")
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := OutboundUsage{ICCID: key.ICCID, Kind: string(key.Kind)}
		// Start with a write, acquiring SQLite's writer lock before reading.
		// Concurrent processes cannot both approve against the same old history.
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		if err := tx.Where("iccid = ? AND kind = ?", key.ICCID, key.Kind).First(&row).Error; err != nil {
			return err
		}
		events, err := apply(row.Events)
		if err != nil {
			return err
		}
		row.Events = events
		return tx.Save(&row).Error
	})
	if err != nil {
		return fmt.Errorf("外发额度检查: %w", err)
	}
	return nil
}
