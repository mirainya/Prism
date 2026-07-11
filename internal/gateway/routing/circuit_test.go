package routing

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/logger"
	"gorm.io/gorm"
)

func TestMarkTransportUnavailableUsesTransportScopedState(t *testing.T) {
	_ = logger.Init()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.GwRouteState{}, &model.AccountModelState{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	circuit := NewCircuit()
	circuit.MarkTransportUnavailable(9, "model", model.UpstreamTransportOpenAIResponses, &domain.UpstreamError{StatusCode: 429, Body: "limited"})
	circuit.MarkTransportUnavailable(9, "model", model.UpstreamTransportOpenAIResponses, &domain.UpstreamError{StatusCode: 429, Body: "limited again"})

	var state model.GwRouteState
	if err := db.Where("key_id = ? AND model_name = ? AND transport = ?", 9, "model", model.UpstreamTransportOpenAIResponses).First(&state).Error; err != nil {
		t.Fatal(err)
	}
	if state.FailCount != 2 || state.StatusCode != 429 || !state.DisabledUntil.After(state.CreatedAt) {
		t.Fatalf("state = %#v", state)
	}
	var legacyCount int64
	if err := db.Model(&model.AccountModelState{}).Count(&legacyCount).Error; err != nil {
		t.Fatal(err)
	}
	if legacyCount != 0 {
		t.Fatalf("transport circuit wrote %d legacy states", legacyCount)
	}
}

func TestMarkTransportUnavailableIgnoresNonCircuitErrors(t *testing.T) {
	_ = logger.Init()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.GwRouteState{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	NewCircuit().MarkTransportUnavailable(9, "model", model.UpstreamTransportOpenAIChat, &domain.UpstreamError{StatusCode: 500})
	var count int64
	if err := db.Model(&model.GwRouteState{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("non-circuit error created %d states", count)
	}
}
