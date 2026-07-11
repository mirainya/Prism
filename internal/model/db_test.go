package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestDBReturnsUnpollutedSession(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:model_session?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&GwChannelKey{}, &AIResponse{}); err != nil {
		t.Fatal(err)
	}
	SetDB(database.Model(&GwChannelKey{}))

	var responses []AIResponse
	if err := DB().Where("status = ?", "queued").Find(&responses).Error; err != nil {
		t.Fatalf("fresh session inherited another model: %v", err)
	}
}
