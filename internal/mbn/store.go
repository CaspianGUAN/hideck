package mbn

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

type DatabaseStore struct{ db *gorm.DB }

func NewDatabaseStore(database *gorm.DB) *DatabaseStore { return &DatabaseStore{db: database} }
func (s *DatabaseStore) SaveOperation(ctx context.Context, o *Operation) error {
	return s.db.WithContext(ctx).Save(o).Error
}
func (s *DatabaseStore) Operation(ctx context.Context, id string) (Operation, error) {
	var o Operation
	err := s.db.WithContext(ctx).First(&o, "id = ?", id).Error
	return o, translateError(err)
}
func (s *DatabaseStore) FindRequest(ctx context.Context, deviceID, requestID string) (Operation, error) {
	var o Operation
	err := s.db.WithContext(ctx).First(&o, "device_id = ? AND request_id = ?", deviceID, requestID).Error
	return o, translateError(err)
}
func (s *DatabaseStore) Operations(ctx context.Context, deviceID string) ([]Operation, error) {
	var result []Operation
	q := s.db.WithContext(ctx).Order("created_at DESC")
	if deviceID != "" {
		q = q.Where("device_id = ?", deviceID)
	}
	err := q.Find(&result).Error
	return result, err
}
func (s *DatabaseStore) Policies(ctx context.Context) ([]Policy, error) {
	var p []Policy
	err := s.db.WithContext(ctx).Find(&p).Error
	return p, err
}
func (s *DatabaseStore) SavePolicy(ctx context.Context, p Policy) error {
	return s.db.WithContext(ctx).Save(&p).Error
}
func (s *DatabaseStore) Assets(ctx context.Context) ([]Asset, error) {
	var a []Asset
	err := s.db.WithContext(ctx).Order("created_at DESC").Find(&a).Error
	return a, err
}
func (s *DatabaseStore) SaveAsset(ctx context.Context, a Asset) error {
	return s.db.WithContext(ctx).Create(&a).Error
}
func (s *DatabaseStore) DeleteAsset(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&Asset{}, "id = ?", id).Error
}
func translateError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}
