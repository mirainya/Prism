package service

import (
	"errors"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/datatypes"
)

func endpointBindingStatus(value int8) *int8 {
	return &value
}

func TestEndpointAdminServicePersistsManyToManyAccountBindings(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Channel{}, &model.Model{}, &model.Endpoint{}); err != nil {
		t.Fatal(err)
	}
	channel := &model.Channel{Type: "endpoint-admin-bindings", Status: 1}
	if err := db.Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	capability := &model.Model{Code: "endpoint-admin-image", Name: "Endpoint admin image", Type: model.ModelTypeImage, Status: 1}
	if err := db.Create(capability).Error; err != nil {
		t.Fatal(err)
	}
	first := &model.ChannelAccount{ChannelID: channel.ID, Name: "first", Status: 1}
	second := &model.ChannelAccount{ChannelID: channel.ID, Name: "second", Status: 1}
	if err := db.Create(first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(second).Error; err != nil {
		t.Fatal(err)
	}

	service := NewEndpointAdminService()
	endpoint, err := service.CreateEndpoint(&CreateEndpointRequest{
		ModelCode:           capability.Code,
		ChannelID:           channel.ID,
		Status:              1,
		PollParamMapping:    datatypes.JSON(`{"field_mapping":{"task_id":"id"}}`),
		PollResponseMapping: datatypes.JSON(`{"field_mapping":{"status":"state"}}`),
		AccountBindings: []EndpointAccountBindingInput{
			{AccountID: first.ID, Status: endpointBindingStatus(1), Priority: 100, Weight: 20},
			{AccountID: second.ID, Status: endpointBindingStatus(0), Priority: 50, Weight: 5},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoint.AccountBindings) != 2 {
		t.Fatalf("account bindings = %d, want 2", len(endpoint.AccountBindings))
	}
	if endpoint.OriginType != model.EndpointOriginManual || endpoint.OriginAccountID != 0 || len(endpoint.OriginSnapshot) == 0 {
		t.Fatalf("manual endpoint origin = type %q account %d snapshot %s", endpoint.OriginType, endpoint.OriginAccountID, endpoint.OriginSnapshot)
	}
	if string(endpoint.PollParamMapping) != `{"field_mapping":{"task_id":"id"}}` ||
		string(endpoint.PollResponseMapping) != `{"field_mapping":{"status":"state"}}` {
		t.Fatalf("poll mappings = params %s response %s", endpoint.PollParamMapping, endpoint.PollResponseMapping)
	}
	for _, binding := range endpoint.AccountBindings {
		if binding.Account == nil || binding.Account.ChannelID != channel.ID {
			t.Fatalf("binding account was not preloaded: %#v", binding)
		}
		if binding.AccountID == second.ID && binding.Status != 0 {
			t.Fatalf("disabled binding status = %d, want 0", binding.Status)
		}
	}

	replacement := []EndpointAccountBindingInput{
		{AccountID: first.ID, Status: endpointBindingStatus(1), Priority: 7, Weight: 3},
	}
	attemptedDiscoveredAt := time.Now()
	updated, err := service.UpdateEndpoint(endpoint.ID, map[string]any{
		"priority":          9,
		"origin_type":       model.EndpointOriginLegacyUnknown,
		"origin_account_id": second.ID,
		"origin_snapshot":   datatypes.JSON(`{"changed":true}`),
		"discovered_at":     &attemptedDiscoveredAt,
	}, &replacement)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Priority != 9 || len(updated.AccountBindings) != 1 {
		t.Fatalf("updated endpoint = priority %d bindings %#v", updated.Priority, updated.AccountBindings)
	}
	if updated.OriginType != model.EndpointOriginManual || updated.OriginAccountID != 0 ||
		string(updated.OriginSnapshot) != string(endpoint.OriginSnapshot) || updated.DiscoveredAt != nil {
		t.Fatalf("endpoint origin changed during update: type %q account %d snapshot %s discovered %v",
			updated.OriginType, updated.OriginAccountID, updated.OriginSnapshot, updated.DiscoveredAt)
	}
	binding := updated.AccountBindings[0]
	if binding.AccountID != first.ID || binding.Priority != 7 || binding.Weight != 3 {
		t.Fatalf("updated binding = %#v", binding)
	}

	otherChannel := &model.Channel{Type: "endpoint-admin-bindings-other", Status: 1}
	if err := db.Create(otherChannel).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateEndpoint(endpoint.ID, map[string]any{"channel_id": float64(otherChannel.ID)}); !errors.Is(err, ErrEndpointAccountMismatch) {
		t.Fatalf("channel change error = %v, want %v", err, ErrEndpointAccountMismatch)
	}
	reloaded, err := service.GetEndpoint(endpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ChannelID != channel.ID {
		t.Fatalf("channel after rolled-back update = %d, want %d", reloaded.ChannelID, channel.ID)
	}
}

func TestEndpointAdminServiceRejectsCrossChannelBinding(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Channel{}, &model.Model{}, &model.Endpoint{}); err != nil {
		t.Fatal(err)
	}
	channel := &model.Channel{Type: "endpoint-admin-owner", Status: 1}
	otherChannel := &model.Channel{Type: "endpoint-admin-other", Status: 1}
	if err := db.Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(otherChannel).Error; err != nil {
		t.Fatal(err)
	}
	capability := &model.Model{Code: "endpoint-admin-video", Name: "Endpoint admin video", Type: model.ModelTypeVideo, Status: 1}
	if err := db.Create(capability).Error; err != nil {
		t.Fatal(err)
	}
	account := &model.ChannelAccount{ChannelID: otherChannel.ID, Status: 1}
	if err := db.Create(account).Error; err != nil {
		t.Fatal(err)
	}

	_, err := NewEndpointAdminService().CreateEndpoint(&CreateEndpointRequest{
		ModelCode: capability.Code,
		ChannelID: channel.ID,
		Status:    1,
		AccountBindings: []EndpointAccountBindingInput{
			{AccountID: account.ID, Status: endpointBindingStatus(1), Weight: 10},
		},
	})
	if !errors.Is(err, ErrEndpointAccountMismatch) {
		t.Fatalf("cross-channel binding error = %v, want %v", err, ErrEndpointAccountMismatch)
	}
	var endpointCount int64
	if err := db.Model(&model.Endpoint{}).Count(&endpointCount).Error; err != nil {
		t.Fatal(err)
	}
	if endpointCount != 0 {
		t.Fatalf("rolled-back endpoint count = %d, want 0", endpointCount)
	}
}
