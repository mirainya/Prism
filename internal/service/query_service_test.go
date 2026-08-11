package service

import (
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/datatypes"
)

func TestListAvailableCapabilitiesGroupsOperationsAndRequiresBoundKey(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Model{}, &model.Channel{}, &model.Endpoint{}); err != nil {
		t.Fatalf("migrate capability query tables: %v", err)
	}

	imageModel := &model.Model{Code: "gpt_image2", Name: "GPT Image 2", Type: model.ModelTypeImage, Status: 1}
	orphanModel := &model.Model{Code: "orphan_image", Name: "Orphan Image", Type: model.ModelTypeImage, Status: 1}
	if err := db.Create([]*model.Model{imageModel, orphanModel}).Error; err != nil {
		t.Fatalf("create models: %v", err)
	}
	channel := &model.Channel{Type: "sub2", Name: "Sub2", Status: 1}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	account := &model.ChannelAccount{ChannelID: channel.ID, Name: "key-1", APIKey: "sk-test", Status: 1}
	if err := db.Create(account).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}

	endpoints := []*model.Endpoint{
		{ModelCode: imageModel.Code, RouteOperation: RouteOperationImagesGenerate, SupportedOperations: datatypes.JSON(`["images.generate","images.edit"]`), ChannelID: channel.ID, VendorModel: "gpt-image-2", SupportsStream: true, Status: 1},
		{ModelCode: imageModel.Code, RouteOperation: RouteOperationImagesGenerate, ChannelID: channel.ID, VendorModel: "gpt-image-2", Status: 1},
		{ModelCode: orphanModel.Code, RouteOperation: RouteOperationImagesGenerate, ChannelID: channel.ID, VendorModel: "orphan-image", Status: 1},
	}
	for _, endpoint := range endpoints {
		if err := db.Create(endpoint).Error; err != nil {
			t.Fatalf("create endpoint: %v", err)
		}
	}
	for _, endpoint := range endpoints[:2] {
		if err := db.Create(&model.EndpointAccount{EndpointID: endpoint.ID, AccountID: account.ID, Status: 1, Weight: 10}).Error; err != nil {
			t.Fatalf("bind endpoint account: %v", err)
		}
	}

	items, err := NewQueryService().ListAvailableCapabilities("", string(model.ModelTypeImage))
	if err != nil {
		t.Fatalf("list available capabilities: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("capability count = %d, want 1: %#v", len(items), items)
	}
	if items[0].ID != imageModel.Code {
		t.Fatalf("model id = %q, want %q", items[0].ID, imageModel.Code)
	}
	if len(items[0].Operations) != 2 {
		t.Fatalf("operation count = %d, want 2: %#v", len(items[0].Operations), items[0].Operations)
	}
	if items[0].Operations[0].ID != RouteOperationImagesGenerate || items[0].Operations[1].ID != RouteOperationImagesEdit {
		t.Fatalf("unexpected operations: %#v", items[0].Operations)
	}
	if !items[0].Operations[0].SupportsStream {
		t.Fatal("generate operation should aggregate stream support")
	}
	if !items[0].Operations[1].SupportsStream {
		t.Fatal("edit operation should inherit stream support from the shared endpoint")
	}
}

func TestListAvailableCapabilitiesIncludesRoutableChatModels(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(
		&model.GwChannel{}, &model.GwChannelKey{}, &model.GwAbility{},
		&model.GwAbilityTransport{}, &model.GwModelMeta{}, &model.Model{},
		&model.Channel{}, &model.Endpoint{},
	); err != nil {
		t.Fatalf("migrate gateway query tables: %v", err)
	}

	channel := &model.GwChannel{Name: "OpenAI", Protocol: model.ProtocolOpenAI, BaseURL: "https://example.com", Status: 1}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create gateway channel: %v", err)
	}
	key := &model.GwChannelKey{ChannelID: channel.ID, Name: "key-1", APIKey: "sk-test", Status: 1}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("create gateway key: %v", err)
	}
	ability := &model.GwAbility{ModelName: "gpt-4.1", ChannelID: channel.ID, KeyID: key.ID, VendorModel: "gpt-4.1", Status: 1}
	if err := db.Create(ability).Error; err != nil {
		t.Fatalf("create gateway ability: %v", err)
	}
	if err := db.Create(&model.GwAbilityTransport{AbilityID: ability.ID, Transport: model.UpstreamTransportOpenAIChat, Status: 1}).Error; err != nil {
		t.Fatalf("create gateway transport: %v", err)
	}

	items, err := NewQueryService().ListAvailableCapabilities("", string(model.ModelTypeChat))
	if err != nil {
		t.Fatalf("list chat capabilities: %v", err)
	}
	if len(items) != 1 || items[0].ID != "gpt-4.1" {
		t.Fatalf("unexpected chat capabilities: %#v", items)
	}
	if len(items[0].Operations) != 3 || items[0].Operations[0].ID != "chat.completions" {
		t.Fatalf("unexpected chat operations: %#v", items[0].Operations)
	}
}
