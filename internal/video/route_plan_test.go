package video

import (
	"testing"

	"github.com/glebarez/sqlite"
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
		Models: datatypes.JSON(`[{"model_name":"video-fast","vendor_model":"seedance-2.0-fast"}]`),
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
