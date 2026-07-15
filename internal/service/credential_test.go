package service

import (
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
)

func TestTokenSecretIsReturnedOnlyAtCreation(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.TokenChannelPriority{}); err != nil {
		t.Fatalf("migrate token priorities: %v", err)
	}

	created, err := NewTokenService().CreateToken(42, &CreateTokenReq{
		Name:    "desktop",
		Balance: decimal.Zero,
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	plainKey, ok := created["key"].(string)
	if !ok || !strings.HasPrefix(plainKey, "sk-prism-") {
		t.Fatalf("creation key = %#v", created["key"])
	}

	var stored model.Token
	if err := db.First(&stored, created["id"]).Error; err != nil {
		t.Fatalf("load stored token: %v", err)
	}
	if db.Migrator().HasColumn(&model.Token{}, "plain_key") {
		t.Fatal("token schema still contains a plaintext key column")
	}
	wantHint := middleware.KeyHint(plainKey)
	if stored.KeyHint != wantHint {
		t.Fatalf("stored key hint = %q, want %q", stored.KeyHint, wantHint)
	}

	list, err := NewTokenService().ListTokens(42)
	if err != nil {
		t.Fatalf("list tokens: %v", err)
	}
	if len(list) != 1 || list[0]["key"] != wantHint || list[0]["key_hint"] != wantHint {
		t.Fatalf("list token key fields = %#v", list)
	}
	if list[0]["key"] == plainKey {
		t.Fatal("token list returned the plaintext key")
	}

	detail, err := NewTokenService().GetToken(42, stored.ID)
	if err != nil {
		t.Fatalf("get token: %v", err)
	}
	if detail["key"] != wantHint || detail["key_hint"] != wantHint {
		t.Fatalf("detail token key fields = %#v", detail)
	}
}

func TestMaskedUpstreamCredentialsAreNotPersistedAsUpdates(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.GwChannel{}, &model.GwChannelKey{}); err != nil {
		t.Fatalf("migrate gateway tables: %v", err)
	}

	const channelSecret = "channel-secret-1234"
	account := &model.ChannelAccount{Name: "legacy", APIKey: channelSecret}
	if err := db.Create(account).Error; err != nil {
		t.Fatalf("create channel account: %v", err)
	}
	newName := "renamed"
	if err := NewChannelService().UpdateChannelAccount(account.ID, &UpdateChannelAccountRequest{
		Name:   newName,
		APIKey: MaskCredential(channelSecret),
	}); err != nil {
		t.Fatalf("update channel account: %v", err)
	}
	var storedAccount model.ChannelAccount
	if err := db.First(&storedAccount, account.ID).Error; err != nil {
		t.Fatalf("load channel account: %v", err)
	}
	if storedAccount.APIKey != channelSecret || storedAccount.Name != newName {
		t.Fatalf("channel account after masked update = %+v", storedAccount)
	}

	channel := &model.GwChannel{Name: "gateway", Protocol: "openai", BaseURL: "https://example.com"}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create gateway channel: %v", err)
	}
	const gatewaySecret = "gateway-secret-5678"
	key := &model.GwChannelKey{ChannelID: channel.ID, Name: "primary", APIKey: gatewaySecret}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("create gateway key: %v", err)
	}
	if err := NewGatewayAdminService().UpdateKey(key.ID, map[string]any{
		"name":    newName,
		"api_key": MaskCredential(gatewaySecret),
	}); err != nil {
		t.Fatalf("update gateway key: %v", err)
	}
	var storedKey model.GwChannelKey
	if err := db.First(&storedKey, key.ID).Error; err != nil {
		t.Fatalf("load gateway key: %v", err)
	}
	if storedKey.APIKey != gatewaySecret || storedKey.Name != newName {
		t.Fatalf("gateway key after masked update = %+v", storedKey)
	}
}
