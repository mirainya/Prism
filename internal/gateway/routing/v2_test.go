package routing

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestSelectTransportPrefersNativeResponses(t *testing.T) {
	db := newTransportRoutingDB(t)
	channel := createRouteFixture(t, db, "openai", model.ProtocolOpenAI, 1, datatypes.JSON(`{"chat":true,"responses":true}`))
	ability := routeFixtureAbility(t, db, channel.ID)
	for _, row := range []model.GwAbilityTransport{
		{AbilityID: ability.ID, Transport: model.UpstreamTransportOpenAIChat, Status: 1},
		{AbilityID: ability.ID, Transport: model.UpstreamTransportOpenAIResponses, Status: 1, Config: datatypes.JSON(`{"header":"value"}`)},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("create transport: %v", err)
		}
	}

	route, err := NewRouter().SelectTransport("test-model", RouteRequirements{CapabilityResponses: true}, RouteOptions{
		AllowedTransports:   []model.UpstreamTransport{model.UpstreamTransportOpenAIChat, model.UpstreamTransportOpenAIResponses},
		PreferredTransports: []model.UpstreamTransport{model.UpstreamTransportOpenAIResponses},
		ResponsesRequest:    true,
	})
	if err != nil {
		t.Fatalf("select transport: %v", err)
	}
	if route.Transport != model.UpstreamTransportOpenAIResponses || route.ExecutionMode != ExecutionModeResponsesNative {
		t.Fatalf("selected transport=%q mode=%q", route.Transport, route.ExecutionMode)
	}
	if !route.Capabilities[CapabilityResponses] || route.TransportConfig["header"] != "value" {
		t.Fatalf("route semantics were not preserved: %#v", route)
	}
}

func TestSelectTransportSkipsTransportCircuitState(t *testing.T) {
	db := newTransportRoutingDB(t)
	channel := createRouteFixture(t, db, "openai", model.ProtocolOpenAI, 1, datatypes.JSON(`{"chat":true,"responses":true}`))
	ability := routeFixtureAbility(t, db, channel.ID)
	for _, transport := range []model.UpstreamTransport{model.UpstreamTransportOpenAIChat, model.UpstreamTransportOpenAIResponses} {
		if err := db.Create(&model.GwAbilityTransport{AbilityID: ability.ID, Transport: transport, Status: 1}).Error; err != nil {
			t.Fatalf("create transport: %v", err)
		}
	}
	var key model.GwChannelKey
	if err := db.Where("channel_id = ?", channel.ID).First(&key).Error; err != nil {
		t.Fatalf("load key: %v", err)
	}
	if err := db.Create(&model.GwRouteState{KeyID: key.ID, ModelName: "test-model", Transport: model.UpstreamTransportOpenAIResponses, DisabledUntil: time.Now().Add(time.Minute)}).Error; err != nil {
		t.Fatalf("create state: %v", err)
	}

	route, err := NewRouter().SelectTransport("test-model", RouteRequirements{CapabilityResponses: true}, RouteOptions{
		AllowedTransports:   []model.UpstreamTransport{model.UpstreamTransportOpenAIChat, model.UpstreamTransportOpenAIResponses},
		PreferredTransports: []model.UpstreamTransport{model.UpstreamTransportOpenAIResponses},
		ResponsesRequest:    true,
	})
	if err != nil {
		t.Fatalf("select fallback: %v", err)
	}
	if route.Transport != model.UpstreamTransportOpenAIChat || route.ExecutionMode != ExecutionModeResponsesConverted {
		t.Fatalf("expected converted fallback, got transport=%q mode=%q", route.Transport, route.ExecutionMode)
	}
}

func TestSelectTransportExcludesOnlyAttemptCombination(t *testing.T) {
	db := newTransportRoutingDB(t)
	channel := createRouteFixture(t, db, "openai", model.ProtocolOpenAI, 1, datatypes.JSON(`{"chat":true,"responses":true}`))
	ability := routeFixtureAbility(t, db, channel.ID)
	for _, transportID := range []model.UpstreamTransport{model.UpstreamTransportOpenAIChat, model.UpstreamTransportOpenAIResponses} {
		if err := db.Create(&model.GwAbilityTransport{AbilityID: ability.ID, Transport: transportID, Status: 1}).Error; err != nil {
			t.Fatalf("create transport: %v", err)
		}
	}
	var key model.GwChannelKey
	if err := db.Where("channel_id = ?", channel.ID).First(&key).Error; err != nil {
		t.Fatal(err)
	}

	route, err := NewRouter().SelectTransport("test-model", RouteRequirements{CapabilityResponses: true}, RouteOptions{
		AllowedTransports:   []model.UpstreamTransport{model.UpstreamTransportOpenAIChat, model.UpstreamTransportOpenAIResponses},
		PreferredTransports: []model.UpstreamTransport{model.UpstreamTransportOpenAIResponses},
		ExcludeAttempts:     []TransportAttempt{{KeyID: key.ID, Transport: model.UpstreamTransportOpenAIResponses}},
		ResponsesRequest:    true,
	})
	if err != nil {
		t.Fatalf("select fallback transport: %v", err)
	}
	if route.KeyID != key.ID || route.Transport != model.UpstreamTransportOpenAIChat {
		t.Fatalf("excluded the whole key: key=%d transport=%q", route.KeyID, route.Transport)
	}
}

func TestSelectTransportPreferenceWinsBeforeAbilityPriority(t *testing.T) {
	db := newTransportRoutingDB(t)
	nativeChannel := createRouteFixture(t, db, "native", model.ProtocolOpenAI, 1, datatypes.JSON(`{"responses":true}`))
	nativeAbility := routeFixtureAbility(t, db, nativeChannel.ID)
	if err := db.Create(&model.GwAbilityTransport{AbilityID: nativeAbility.ID, Transport: model.UpstreamTransportOpenAIResponses, Status: 1}).Error; err != nil {
		t.Fatalf("create native transport: %v", err)
	}
	chatChannel := createRouteFixture(t, db, "chat", model.ProtocolOpenAI, 10, datatypes.JSON(`{"responses":true}`))
	chatAbility := routeFixtureAbility(t, db, chatChannel.ID)
	if err := db.Create(&model.GwAbilityTransport{AbilityID: chatAbility.ID, Transport: model.UpstreamTransportOpenAIChat, Status: 1}).Error; err != nil {
		t.Fatalf("create chat transport: %v", err)
	}

	route, err := NewRouter().SelectTransport("test-model", RouteRequirements{CapabilityResponses: true}, RouteOptions{
		AllowedTransports:   []model.UpstreamTransport{model.UpstreamTransportOpenAIChat, model.UpstreamTransportOpenAIResponses},
		PreferredTransports: []model.UpstreamTransport{model.UpstreamTransportOpenAIResponses},
		ResponsesRequest:    true,
	})
	if err != nil {
		t.Fatalf("select transport: %v", err)
	}
	if route.Transport != model.UpstreamTransportOpenAIResponses || route.ChannelID != nativeChannel.ID {
		t.Fatalf("preference did not win: transport=%q channel=%d", route.Transport, route.ChannelID)
	}
}

func TestSelectTransportRequiresExplicitTransport(t *testing.T) {
	db := newTransportRoutingDB(t)
	channel := createRouteFixture(t, db, "legacy", model.ProtocolOpenAI, 1, nil)
	_ = routeFixtureAbility(t, db, channel.ID)
	if _, err := NewRouter().SelectTransport("test-model", RouteRequirements{CapabilityChat: true}, RouteOptions{}); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("legacy ability without transport error=%v, want ErrNoRoute", err)
	}
}

func TestSelectTransportPreservesCapabilityErrors(t *testing.T) {
	db := newTransportRoutingDB(t)
	channel := createRouteFixture(t, db, "chat-only", model.ProtocolOpenAI, 1, datatypes.JSON(`["chat"]`))
	ability := routeFixtureAbility(t, db, channel.ID)
	if err := db.Create(&model.GwAbilityTransport{AbilityID: ability.ID, Transport: model.UpstreamTransportOpenAIChat, Status: 1}).Error; err != nil {
		t.Fatalf("create transport: %v", err)
	}
	if _, err := NewRouter().SelectTransport("test-model", RouteRequirements{CapabilityResponses: true}, RouteOptions{}); !errors.Is(err, ErrCapabilityUnavailable) {
		t.Fatalf("capability error=%v, want ErrCapabilityUnavailable", err)
	}
}

func TestSelectTransportTreatsLegacyCapabilitiesAsEmpty(t *testing.T) {
	db := newTransportRoutingDB(t)
	channel := createRouteFixture(t, db, "legacy", model.ProtocolOpenAI, 1, nil)
	ability := routeFixtureAbility(t, db, channel.ID)
	if err := db.Create(&model.GwAbilityTransport{AbilityID: ability.ID, Transport: model.UpstreamTransportOpenAIChat, Status: 1}).Error; err != nil {
		t.Fatalf("create transport: %v", err)
	}
	if _, err := NewRouter().SelectTransport("test-model", RouteRequirements{CapabilityChat: true}, RouteOptions{}); !errors.Is(err, ErrCapabilityUnavailable) {
		t.Fatalf("legacy capability error=%v, want ErrCapabilityUnavailable", err)
	}
}

func newTransportRoutingDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.AutoMigrate(&model.GwChannel{}, &model.GwChannelKey{}, &model.GwAbility{}, &model.GwAbilityTransport{}, &model.GwRouteState{}); err != nil {
		t.Fatalf("migrate routing tables: %v", err)
	}
	model.SetDB(db)
	return db
}

func routeFixtureAbility(t *testing.T, db *gorm.DB, channelID uint) *model.GwAbility {
	t.Helper()
	var ability model.GwAbility
	if err := db.Where("channel_id = ?", channelID).First(&ability).Error; err != nil {
		t.Fatalf("load ability: %v", err)
	}
	return &ability
}

func createRouteFixture(t *testing.T, db *gorm.DB, name string, protocol model.Protocol, priority int, capabilities datatypes.JSON) *model.GwChannel {
	t.Helper()
	channel := &model.GwChannel{Name: name, Protocol: protocol, BaseURL: "https://" + name + ".example", Status: 1}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	key := &model.GwChannelKey{ChannelID: channel.ID, Name: name + "-key", APIKey: "provider-key", Weight: 10, Status: 1}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}
	ability := &model.GwAbility{
		ModelName: "test-model", ChannelID: channel.ID, KeyID: key.ID,
		VendorModel: "vendor-" + name, Priority: priority, Capabilities: capabilities, Status: 1,
	}
	if err := db.Create(ability).Error; err != nil {
		t.Fatalf("create ability: %v", err)
	}
	return channel
}
