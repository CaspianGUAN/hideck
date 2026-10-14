package db

import (
	"context"
	"errors"
	"sync"
)

const (
	incomingSMSSubscriberBuffer = 32
	incomingSMSBacklogLimit     = 256
)

type incomingSMSHub struct {
	mu          sync.Mutex
	nextSubID   uint64
	subscribers map[uint64]chan SMS
}

var incomingSMS = newIncomingSMSHub()

// Keep ID allocation, commit and event publication in the same order across
// single-part and multipart writers. SSE reconnects use the persisted SMS ID.
var smsPublicationMu sync.Mutex

func newIncomingSMSHub() *incomingSMSHub {
	return &incomingSMSHub{subscribers: make(map[uint64]chan SMS)}
}

func SubscribeIncomingSMS() (<-chan SMS, func()) {
	return incomingSMS.subscribe()
}

func ListIncomingUnreadSMSAfter(ctx context.Context, afterID uint, limit int) ([]SMS, error) {
	return ListIncomingUnreadSMS(ctx, IncomingSMSBacklogQuery{AfterID: afterID, Limit: limit})
}

type IncomingSMSBacklogQuery struct {
	AfterID   uint
	ThroughID *uint
	Limit     int
}

// IncomingSMSCursor fixes the replay boundary before live events are delivered.
func IncomingSMSCursor(ctx context.Context) (uint, error) {
	if DB == nil {
		return 0, errors.New("database is not initialized")
	}
	var cursor uint
	err := DB.WithContext(ctx).Model(&SMS{}).Where("type = ?", smsTypeIncoming).
		Select("COALESCE(MAX(id), 0)").Scan(&cursor).Error
	return cursor, err
}

func ListIncomingUnreadSMS(ctx context.Context, query IncomingSMSBacklogQuery) ([]SMS, error) {
	if DB == nil {
		return nil, errors.New("database is not initialized")
	}
	limit := query.Limit
	if limit <= 0 || limit > incomingSMSBacklogLimit {
		limit = incomingSMSBacklogLimit
	}
	var rows []SMS
	statement := DB.WithContext(ctx).
		Where("type = ? AND status = ? AND id > ?", smsTypeIncoming, smsStatusUnread, query.AfterID)
	if query.ThroughID != nil {
		statement = statement.Where("id <= ?", *query.ThroughID)
	}
	err := statement.
		Order("id ASC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

func publishIncomingSMS(sms SMS) {
	if sms.Type != smsTypeIncoming || sms.ID == 0 {
		return
	}
	incomingSMS.publish(sms)
}

func publishIncomingSMSByID(id uint) {
	if DB == nil || id == 0 {
		return
	}
	var sms SMS
	if err := DB.First(&sms, id).Error; err != nil {
		return
	}
	publishIncomingSMS(sms)
}

func (hub *incomingSMSHub) publish(sms SMS) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for id, subscriber := range hub.subscribers {
		select {
		case subscriber <- sms:
		default:
			delete(hub.subscribers, id)
			close(subscriber)
		}
	}
}

func (hub *incomingSMSHub) subscribe() (<-chan SMS, func()) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.nextSubID++
	id := hub.nextSubID
	stream := make(chan SMS, incomingSMSSubscriberBuffer)
	hub.subscribers[id] = stream
	return stream, func() {
		hub.mu.Lock()
		if subscriber, ok := hub.subscribers[id]; ok {
			delete(hub.subscribers, id)
			close(subscriber)
		}
		hub.mu.Unlock()
	}
}
