package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

func TestImportKeyModelsCreatesConservativeTransportAndCleansRelations(t *testing.T) {
	db := gatewayTransportTestDB(t)
	channel := model.GwChannel{Name: "OpenAI", Protocol: model.ProtocolOpenAI, BaseURL: "https://example.test", Status: 1}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	key := model.GwChannelKey{ChannelID: channel.ID, Name: "key", APIKey: "secret", Status: 1}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	service := NewGatewayAdminService()
	request := &GwImportRequest{KeyID: key.ID, Models: []GwImportItem{{ModelName: "gpt-test"}}}
	result, err := service.ImportKeyModels(request)
	if err != nil {
		t.Fatal(err)
	}
	if result.AbilitiesAdded != 1 || result.TransportsAdded != 1 || result.MetaAdded != 1 {
		t.Fatalf("import result=%#v", result)
	}
	result, err = service.ImportKeyModels(request)
	if err != nil {
		t.Fatal(err)
	}
	if result.AbilitiesAdded != 0 || result.TransportsAdded != 0 || result.MetaAdded != 0 {
		t.Fatalf("duplicate import result=%#v", result)
	}
	var ability model.GwAbility
	if err := db.Where("key_id = ? AND model_name = ?", key.ID, "gpt-test").First(&ability).Error; err != nil {
		t.Fatal(err)
	}
	var capabilities map[string]bool
	if err := json.Unmarshal(ability.Capabilities, &capabilities); err != nil {
		t.Fatal(err)
	}
	for _, endpointFlag := range []string{"chat", "responses", "native_responses", "volcengine_responses"} {
		if capabilities[endpointFlag] {
			t.Fatalf("endpoint flag %q leaked into semantic capabilities", endpointFlag)
		}
	}
	transports, err := service.ListAbilityTransports(ability.ID)
	if err != nil || len(transports) != 1 || transports[0].Transport != model.UpstreamTransportOpenAIChat {
		t.Fatalf("transports=%#v err=%v", transports, err)
	}
	if _, err := service.UpsertAbilityTransport(ability.ID, model.UpstreamTransportOpenAIResponses, 1, nil); err != nil {
		t.Fatal(err)
	}
	state := model.GwRouteState{KeyID: key.ID, ModelName: ability.ModelName, Transport: model.UpstreamTransportOpenAIChat}
	if err := db.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteKey(key.ID); err != nil {
		t.Fatal(err)
	}
	var transportCount, stateCount int64
	_ = db.Model(&model.GwAbilityTransport{}).Count(&transportCount).Error
	_ = db.Model(&model.GwRouteState{}).Count(&stateCount).Error
	if transportCount != 0 || stateCount != 0 {
		t.Fatalf("orphan rows transports=%d states=%d", transportCount, stateCount)
	}
}

func TestProbeAbilityTransportPersistsResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path=%s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"chatcmpl_probe","object":"chat.completion","created":1,"model":"vendor","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()
	db := gatewayTransportTestDB(t)
	channel := model.GwChannel{Name: "OpenAI", Protocol: model.ProtocolOpenAI, BaseURL: server.URL, Status: 1}
	_ = db.Create(&channel).Error
	key := model.GwChannelKey{ChannelID: channel.ID, APIKey: "secret", Status: 1}
	_ = db.Create(&key).Error
	ability := model.GwAbility{ModelName: "public", ChannelID: channel.ID, KeyID: key.ID, VendorModel: "vendor", Status: 1}
	_ = db.Create(&ability).Error
	row := model.GwAbilityTransport{AbilityID: ability.ID, Transport: model.UpstreamTransportOpenAIChat, Status: 1}
	_ = db.Create(&row).Error

	result, err := NewGatewayAdminService().ProbeAbilityTransport(context.Background(), ability.ID, row.Transport)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Error != "" {
		t.Fatalf("probe=%#v", result)
	}
	var stored model.GwAbilityTransport
	if err := db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.CheckedAt == nil || stored.LastError != "" {
		t.Fatalf("stored=%#v", stored)
	}
}

func gatewayTransportTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.GwChannel{}, &model.GwChannelKey{}, &model.GwAbility{}, &model.GwAbilityTransport{}, &model.GwRouteState{}, &model.GwModelMeta{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	return db
}
