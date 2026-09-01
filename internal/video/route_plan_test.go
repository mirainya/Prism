package video

import (
	"encoding/json"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestLoadVideoTaskRouteUsesChannelSnapshotAndLiveCredential(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&VideoChannel{}, &VideoChannelKey{}); err != nil {
		t.Fatal(err)
	}
	channel := VideoChannel{
		Name: "original", AdapterType: "generic", BaseURL: "https://old.example.com",
		Models:        datatypes.JSON(`[{"model_name":"video-fast","vendor_model":"seedance-2.0-fast"}]`),
		AssetResolver: "direct_url", ExtraConfig: datatypes.JSON(`{"adapter":{"submit":{"path":"/old"}}}`),
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	key := VideoChannelKey{ChannelID: channel.ID, APIKey: "secret", Status: "active"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	plan, err := BuildVideoRoutePlan(&channel, &key, "video-fast", "seedance-2.0-fast")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&channel).Updates(map[string]any{"name": "changed", "base_url": "https://new.example.com"}).Error; err != nil {
		t.Fatal(err)
	}
	loadedChannel, loadedKey, vendorModel, err := LoadVideoTaskRoute(db, &VideoTask{
		Model: "video-fast", VendorModel: "seedance-2.0-fast", ChannelID: channel.ID, KeyID: key.ID, RoutePlan: plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	if loadedChannel.Name != "original" || loadedChannel.BaseURL != "https://old.example.com" {
		t.Fatalf("route used mutable channel: %#v", loadedChannel)
	}
	if loadedKey.APIKey != "secret" || vendorModel != "seedance-2.0-fast" {
		t.Fatalf("key=%#v vendor_model=%q", loadedKey, vendorModel)
	}
}

func TestVideoRoutePlanPreservesFormalChannelSettings(t *testing.T) {
	channel := &VideoChannel{
		ID: 7, Name: "channel", AdapterType: AdapterTypeGeneric, AdapterProfile: "json_task_v1",
		BaseURL: "https://provider.example", RequestTimeoutSeconds: 60,
		SupportsFirstFrame: boolPointer(true), SupportsLastFrame: boolPointer(false),
		SupportsAudio: boolPointer(true), SupportsWebSearch: boolPointer(false),
		CancelMode: CancelModeLocalOnly, PricingMode: PricingModeUpstreamEstimate,
		FixedPrice: decimal.RequireFromString("1.25"), MarkupRatio: decimal.RequireFromString("1.30"),
		AssetResolver: "direct_url", ResultStorageEnabled: boolPointer(true),
		ExtraConfig: datatypes.JSON(`{"adapter":{"submit":{"path":"/tasks"}}}`),
	}
	encoded, err := BuildVideoRoutePlan(channel, &VideoChannelKey{ID: 9}, "video-model", "vendor-model")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := DecodeVideoRoutePlan(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Channel.AdapterProfile != channel.AdapterProfile ||
		plan.Channel.RequestTimeoutSeconds != channel.RequestTimeoutSeconds ||
		plan.Channel.CancelMode != channel.CancelMode ||
		plan.Channel.PricingMode != channel.PricingMode ||
		!plan.Channel.FixedPrice.Equal(channel.FixedPrice) ||
		!plan.Channel.MarkupRatio.Equal(channel.MarkupRatio) ||
		plan.Channel.ResultStorageEnabled == nil || !*plan.Channel.ResultStorageEnabled {
		t.Fatalf("formal settings were not preserved: %#v", plan.Channel)
	}
}

func TestDecodeVideoRoutePlanAcceptsLegacySnapshot(t *testing.T) {
	legacy := map[string]any{
		"version": float64(VideoRoutePlanVersion), "public_model": "video-model", "vendor_model": "vendor-model", "key_id": float64(9),
		"channel": map[string]any{
			"id": float64(7), "name": "legacy", "adapter_type": AdapterTypeGeneric,
			"base_url": "https://provider.example", "asset_resolver": "direct_url",
			"extra_config": map[string]any{"adapter": map[string]any{"timeout_seconds": float64(80)}},
		},
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := DecodeVideoRoutePlan(encoded)
	if err != nil {
		t.Fatal(err)
	}
	channel := VideoChannel{
		AdapterType:           plan.Channel.AdapterType,
		RequestTimeoutSeconds: plan.Channel.RequestTimeoutSeconds,
		ExtraConfig:           plan.Channel.ExtraConfig,
	}
	if got := channel.EffectiveRequestTimeoutSeconds(); got != 80 {
		t.Fatalf("legacy snapshot timeout=%d", got)
	}
}
