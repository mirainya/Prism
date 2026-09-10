package service

import (
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/logger"
	"gorm.io/gorm"
)

var dbCounter int64

func TestMain(m *testing.M) {
	_ = logger.Init()
	os.Exit(m.Run())
}

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:memtest%d?mode=memory&cache=shared", atomic.AddInt64(&dbCounter, 1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.Exec("PRAGMA busy_timeout = 5000")
	if err := db.AutoMigrate(
		&model.User{},
		&model.Token{},
		&model.Task{},
		&model.BillingLog{},
		&model.BalanceEntry{},
		&model.ChannelAccount{},
		&model.Model{},
		&model.EndpointAccount{},
		&model.AccountModelState{},
		&model.APICall{},
		&model.APICallAttempt{},
		&model.ConversationProjectionOutbox{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	model.SetDB(db)
	return db
}
