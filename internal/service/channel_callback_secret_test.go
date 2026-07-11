package service

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"sync"
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

func setupChannelTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Channel{}); err != nil {
		t.Fatalf("migrate channels: %v", err)
	}
	return db
}

func TestChannelServiceCreateChannelGeneratesCallbackSecret(t *testing.T) {
	setupChannelTestDB(t)

	channel, err := NewChannelService().CreateChannel(&CreateChannelRequest{
		Type:    "callback-create",
		Name:    "Callback Create",
		BaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	assertCallbackSecret(t, channel.CallbackSecret)

	var saved model.Channel
	if err := model.DB().First(&saved, channel.ID).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if saved.CallbackSecret != channel.CallbackSecret {
		t.Fatalf("saved callback secret = %q, want %q", saved.CallbackSecret, channel.CallbackSecret)
	}
}

func TestChannelServiceEnsureCallbackSecretBackfillsEmptyValue(t *testing.T) {
	setupChannelTestDB(t)
	channel := createLegacyChannel(t, "callback-backfill")

	secret, err := NewChannelService().EnsureCallbackSecret(channel.ID)
	if err != nil {
		t.Fatalf("EnsureCallbackSecret: %v", err)
	}
	assertCallbackSecret(t, secret)

	var saved model.Channel
	if err := model.DB().First(&saved, channel.ID).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if saved.CallbackSecret != secret {
		t.Fatalf("saved callback secret = %q, want %q", saved.CallbackSecret, secret)
	}
}

func TestChannelServiceEnsureCallbackSecretBackfillsNullValue(t *testing.T) {
	db := setupChannelTestDB(t)
	channel := createLegacyChannel(t, "callback-null")
	if err := db.Exec("UPDATE channels SET callback_secret = NULL WHERE id = ?", channel.ID).Error; err != nil {
		t.Fatalf("set callback secret to NULL: %v", err)
	}

	secret, err := NewChannelService().EnsureCallbackSecret(channel.ID)
	if err != nil {
		t.Fatalf("EnsureCallbackSecret: %v", err)
	}
	assertCallbackSecret(t, secret)
}

func TestChannelServiceEnsureCallbackSecretIsStable(t *testing.T) {
	setupChannelTestDB(t)
	channel := createLegacyChannel(t, "callback-stable")
	service := NewChannelService()

	first, err := service.EnsureCallbackSecret(channel.ID)
	if err != nil {
		t.Fatalf("first EnsureCallbackSecret: %v", err)
	}
	second, err := service.EnsureCallbackSecret(channel.ID)
	if err != nil {
		t.Fatalf("second EnsureCallbackSecret: %v", err)
	}
	if second != first {
		t.Fatalf("callback secret changed from %q to %q", first, second)
	}
}

func TestChannelServiceEnsureCallbackSecretConcurrent(t *testing.T) {
	setupChannelTestDB(t)
	channel := createLegacyChannel(t, "callback-concurrent")
	service := NewChannelService()

	const workers = 16
	start := make(chan struct{})
	results := make(chan string, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			secret, err := service.EnsureCallbackSecret(channel.ID)
			if err != nil {
				errs <- err
				return
			}
			results <- secret
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Errorf("EnsureCallbackSecret: %v", err)
	}

	unique := make(map[string]struct{})
	for secret := range results {
		assertCallbackSecret(t, secret)
		unique[secret] = struct{}{}
	}
	if len(unique) != 1 {
		t.Fatalf("concurrent calls returned %d callback secrets, want 1", len(unique))
	}

	var saved model.Channel
	if err := model.DB().First(&saved, channel.ID).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if _, ok := unique[saved.CallbackSecret]; !ok {
		t.Fatalf("saved callback secret %q was not returned by callers", saved.CallbackSecret)
	}
}

func TestChannelJSONOmitsCallbackSecret(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	payload, err := json.Marshal(model.Channel{
		Type:           "callback-json",
		Name:           "Callback JSON",
		CallbackSecret: secret,
	})
	if err != nil {
		t.Fatalf("marshal channel: %v", err)
	}
	if bytes.Contains(payload, []byte("callback_secret")) {
		t.Fatalf("channel JSON contains callback_secret: %s", payload)
	}
	if bytes.Contains(payload, []byte(secret)) {
		t.Fatalf("channel JSON contains callback secret value: %s", payload)
	}
}

func createLegacyChannel(t *testing.T, channelType string) *model.Channel {
	t.Helper()
	channel := &model.Channel{
		Type:           channelType,
		Name:           channelType,
		BaseURL:        "https://example.com",
		CallbackSecret: "",
		Status:         1,
	}
	if err := model.DB().Create(channel).Error; err != nil {
		t.Fatalf("create legacy channel: %v", err)
	}
	return channel
}

func assertCallbackSecret(t *testing.T, secret string) {
	t.Helper()
	if len(secret) != 64 {
		t.Fatalf("callback secret length = %d, want 64", len(secret))
	}
	decoded, err := hex.DecodeString(secret)
	if err != nil {
		t.Fatalf("callback secret is not hex: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("decoded callback secret length = %d, want 32", len(decoded))
	}
}
