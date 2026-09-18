package routing

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

func TestSelectTransportRequiresStableIdentity(t *testing.T) {
	_, err := NewRouter().SelectTransport(context.Background(), "test-model", nil, RouteOptions{})
	if !errors.Is(err, ErrInvalidSelectionKey) {
		t.Fatalf("error = %v, want ErrInvalidSelectionKey", err)
	}
}

func TestSelectTransportNeverFallsBackToLegacyRoutes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.GwChannel{}, &model.GwChannelKey{}, &model.GwAbility{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE gw_catalog_runtime_state (id INTEGER PRIMARY KEY, active_release_id INTEGER NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO gw_catalog_runtime_state(id,active_release_id) VALUES (1,NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	channel := &model.GwChannel{Name: "legacy", Protocol: model.ProtocolOpenAI, BaseURL: "https://legacy.example", Status: 1}
	if err := db.Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	key := &model.GwChannelKey{ChannelID: channel.ID, Name: "legacy-key", APIKey: "secret", Status: 1}
	if err := db.Create(key).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.GwAbility{ModelName: "test-model", ChannelID: channel.ID, KeyID: key.ID, Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)

	_, err = NewRouter().SelectTransport(context.Background(), "test-model", nil, RouteOptions{SelectionKey: t.Name()})
	if !errors.Is(err, ErrNoRoute) {
		t.Fatalf("error = %v, want ErrNoRoute", err)
	}
	var stored model.GwChannelKey
	if err := db.First(&stored, key.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.CurrentConc != 0 {
		t.Fatalf("legacy concurrency changed to %d", stored.CurrentConc)
	}
}

func TestSemanticCapabilitiesAcceptObjectAndList(t *testing.T) {
	for _, raw := range [][]byte{[]byte(`{"chat":true,"vision":false}`), []byte(`["chat"]`)} {
		capabilities := semanticCapabilities(raw)
		if !capabilities[CapabilityChat] || capabilities[CapabilityVision] {
			t.Fatalf("unexpected capabilities: %#v", capabilities)
		}
	}
}

func TestExecutionMode(t *testing.T) {
	if got := executionMode(false, model.UpstreamTransportOpenAIResponses); got != ExecutionModeChat {
		t.Fatalf("chat mode = %q", got)
	}
	if got := executionMode(true, model.UpstreamTransportOpenAIResponses); got != ExecutionModeResponsesNative {
		t.Fatalf("native mode = %q", got)
	}
	if got := executionMode(true, model.UpstreamTransportOpenAIChat); got != ExecutionModeResponsesConverted {
		t.Fatalf("converted mode = %q", got)
	}
}
