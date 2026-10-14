package db

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/yibaiba/hideck/internal/outbound"
	"gorm.io/gorm"
)

func openUsageDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.AutoMigrate(&OutboundUsage{}); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestOutboundUsageSurvivesReopenAndCanonicalICCID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.db")
	now := time.Now()
	newLimiter := func(database *gorm.DB) *outbound.Limiter {
		l, err := outbound.New(NewOutboundUsageStore(database), outbound.Config{}, func() time.Time { return now })
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	l := newLimiter(openUsageDB(t, path))
	request := outbound.Request{ICCID: "\"123F\"", Kind: outbound.SMS, Units: 1}
	if err := l.Consume(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	reopened := newLimiter(openUsageDB(t, path))
	request.ICCID = "123"
	var limited *outbound.LimitedError
	if err := reopened.Consume(context.Background(), request); !errors.As(err, &limited) {
		t.Fatalf("reopen lost budget: %v", err)
	}
	now = now.Add(5 * time.Second)
	if err := reopened.Consume(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.ICCID = "456"
	if err := reopened.Consume(context.Background(), request); err != nil {
		t.Fatal(err)
	}
}

func TestOutboundUsageSerializesIndependentConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.db")
	left, right := openUsageDB(t, path), openUsageDB(t, path)
	var accepted atomic.Int32
	var group sync.WaitGroup
	for _, database := range []*gorm.DB{left, right} {
		limiter, err := outbound.New(NewOutboundUsageStore(database), outbound.Config{}, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 10; i++ {
			group.Add(1)
			go func() {
				defer group.Done()
				err := limiter.Consume(context.Background(), outbound.Request{ICCID: "123", Kind: outbound.Call, Units: 1})
				if err == nil {
					accepted.Add(1)
					return
				}
				var limited *outbound.LimitedError
				if !errors.As(err, &limited) {
					t.Errorf("unexpected storage error: %v", err)
				}
			}()
		}
	}
	group.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("accepted=%d", accepted.Load())
	}
	var row OutboundUsage
	if err := left.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if len(row.Events) != 1 {
		t.Fatalf("history=%v", row.Events)
	}
}
