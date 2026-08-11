package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

func endpointDiscoveryChannelConfig(t *testing.T, path string, operations []string) []byte {
	t.Helper()
	value, err := json.Marshal(map[string]any{
		"endpoint_discovery": map[string]any{
			"enabled": true, "adapter": BuiltInOpenAIImagesAdapter,
			"discovery_path": path, "generation_path": "/images/generations", "edit_path": "/images/edits",
			"operations": operations, "auth_location": "header", "auth_key": "X-API-Key", "auth_value_prefix": "",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func setupEndpointAdapterTestDB(t *testing.T) {
	t.Helper()
	db := setupTestDB(t)
	if err := db.AutoMigrate(
		&model.Model{}, &model.Channel{}, &model.ChannelAccount{}, &model.Endpoint{},
		&model.EndpointAccount{}, &model.EndpointAdapter{}, &model.EndpointAdapterRevision{},
		&model.EndpointRouteState{}, &model.Task{},
	); err != nil {
		t.Fatal(err)
	}
}

func TestTaskEndpointSnapshotPreservesExecutionConfiguration(t *testing.T) {
	setupEndpointAdapterTestDB(t)
	channel := &model.Channel{Type: "endpoint-snapshot", Status: 1}
	if err := model.DB().Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	endpoint := &model.Endpoint{
		ModelCode: "snapshot-image", ChannelID: channel.ID, Protocol: model.ProtocolOpenAI,
		RequestPath: "/v1/images/generations", RequestMethod: "POST", ContentType: "application/json",
		VendorModel: "gpt-image-1", ParamMapping: []byte(`{"fixed_params":{"model":"gpt-image-1"}}`),
		ResponseMapping: []byte(`{"field_mapping":{"url":"data[0].url"}}`), Status: 1,
	}
	if err := model.DB().Create(endpoint).Error; err != nil {
		t.Fatal(err)
	}
	adapterID, revisionID, snapshot, err := SnapshotEndpointExecution(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if adapterID == 0 || revisionID == 0 || len(snapshot) == 0 {
		t.Fatalf("snapshot metadata = adapter %d revision %d snapshot %s", adapterID, revisionID, snapshot)
	}
	task, err := NewTaskService().CreateTask(&CreateTaskRequest{
		ModelCode: "snapshot-image", ChannelID: channel.ID, EndpointID: endpoint.ID,
		AdapterID: adapterID, AdapterRevisionID: revisionID, EndpointSnapshot: snapshot,
		RequestParams: map[string]any{"prompt": "test"}, MappedParams: map[string]any{"prompt": "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB().Model(endpoint).Updates(map[string]any{
		"request_path": "/v2/images/generations", "param_mapping": []byte(`{"fixed_params":{"model":"changed"}}`),
	}).Error; err != nil {
		t.Fatal(err)
	}
	var current model.Endpoint
	if err := model.DB().First(&current, endpoint.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.RequestPath == "/v1/images/generations" {
		t.Fatal("test endpoint was not changed")
	}
	if err := ApplyTaskEndpointSnapshot(task, &current); err != nil {
		t.Fatal(err)
	}
	if current.RequestPath != "/v1/images/generations" || string(current.ParamMapping) != `{"fixed_params":{"model":"gpt-image-1"}}` {
		t.Fatalf("applied endpoint = path %q mapping %s", current.RequestPath, current.ParamMapping)
	}
}

func TestDiscoverEndpointModelsUsesEndpointCredentialsAndRevision(t *testing.T) {
	setupEndpointAdapterTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/catalog/models" || r.Header.Get("Authorization") != "Bearer endpoint-secret" {
			t.Fatalf("discovery request = %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-image-2"},{"id":"gpt-image-1"},{"id":"gpt-image-2"}]}`))
	}))
	defer server.Close()
	previousClient := endpointDiscoveryHTTPClient
	endpointDiscoveryHTTPClient = server.Client()
	t.Cleanup(func() { endpointDiscoveryHTTPClient = previousClient })

	channel := &model.Channel{Type: "endpoint-openai", BaseURL: server.URL, Status: 1}
	if err := model.DB().Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	account := &model.ChannelAccount{ChannelID: channel.ID, APIKey: "endpoint-secret", Status: 1}
	if err := model.DB().Create(account).Error; err != nil {
		t.Fatal(err)
	}
	templateModel := &model.Model{Code: "image-template", Name: "Image template", Type: model.ModelTypeImage, Status: 1}
	if err := model.DB().Create(templateModel).Error; err != nil {
		t.Fatal(err)
	}
	endpoint := &model.Endpoint{
		ModelCode: "image-template", ChannelID: channel.ID, AccountID: account.ID,
		Protocol: model.ProtocolOpenAI, RouteOperation: RouteOperationImagesGenerate,
		RequestPath: "/custom/image/create", ExtraConfig: []byte(`{"adapter":"openai.images"}`), Status: 1,
	}
	if err := model.DB().Create(endpoint).Error; err != nil {
		t.Fatal(err)
	}
	adapter, revision, _, err := ensureAdapterTx(model.DB(), endpoint, BuiltInOpenAIImagesAdapter, EndpointAdapterConfig{
		DiscoveryPath: "/catalog/models", GenerationPath: "/custom/image/create",
		EditPath: "/custom/image/edit", Operations: []string{RouteOperationImagesGenerate, RouteOperationImagesEdit},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}

	result, err := NewEndpointAdapterService().DiscoverEndpointModels(context.Background(), endpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Adapter != BuiltInOpenAIImagesAdapter || len(result.Models) != 2 ||
		result.Models[0].ID != "gpt-image-1" || result.Models[1].ID != "gpt-image-2" {
		t.Fatalf("discovery result = %#v", result)
	}
	var storedAdapter model.EndpointAdapter
	if err := model.DB().Where("endpoint_id = ?", endpoint.ID).First(&storedAdapter).Error; err != nil {
		t.Fatal(err)
	}
	if storedAdapter.ID != adapter.ID || storedAdapter.ActiveRevisionID != revision.ID || result.RevisionID != revision.ID {
		t.Fatalf("adapter = %#v, result revision = %d", storedAdapter, result.RevisionID)
	}
	snapshotAdapterID, snapshotRevisionID, _, err := SnapshotEndpointExecution(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotAdapterID != adapter.ID || snapshotRevisionID != revision.ID {
		t.Fatalf("snapshot adapter = %d revision = %d", snapshotAdapterID, snapshotRevisionID)
	}
	var revisionCount int64
	if err := model.DB().Model(&model.EndpointAdapterRevision{}).Where("adapter_id = ?", adapter.ID).Count(&revisionCount).Error; err != nil {
		t.Fatal(err)
	}
	if revisionCount != 1 {
		t.Fatalf("revision count = %d, want 1", revisionCount)
	}
	var state model.EndpointRouteState
	if err := model.DB().Where("endpoint_id = ? AND account_id = ? AND route_operation = ?", endpoint.ID, account.ID, RouteOperationImagesGenerate).First(&state).Error; err != nil {
		t.Fatal(err)
	}
	if state.StatusCode != http.StatusOK || state.LastError != "" || len(state.Discovered) == 0 {
		t.Fatalf("route state = %#v", state)
	}
}

func TestDiscoverEndpointModelsSupportsQueryCredentials(t *testing.T) {
	setupEndpointAdapterTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "query-secret" {
			t.Fatalf("discovery query = %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-image-query"}]}`))
	}))
	defer server.Close()
	previousClient := endpointDiscoveryHTTPClient
	endpointDiscoveryHTTPClient = server.Client()
	t.Cleanup(func() { endpointDiscoveryHTTPClient = previousClient })

	channel := &model.Channel{Type: "endpoint-query", BaseURL: server.URL, Status: 1}
	if err := model.DB().Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	account := &model.ChannelAccount{ChannelID: channel.ID, APIKey: "query-secret", Status: 1}
	if err := model.DB().Create(account).Error; err != nil {
		t.Fatal(err)
	}
	endpoint := &model.Endpoint{
		ModelCode: "image-query", ChannelID: channel.ID, AccountID: account.ID,
		Protocol: model.ProtocolOpenAI, RequestPath: "/v1/images/generations",
		AuthLocation: "query", AuthKey: "api_key", Status: 1,
	}
	if err := model.DB().Create(endpoint).Error; err != nil {
		t.Fatal(err)
	}
	result, err := NewEndpointAdapterService().DiscoverEndpointModels(context.Background(), endpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Models) != 1 || result.Models[0].ID != "gpt-image-query" {
		t.Fatalf("discovery result = %#v", result)
	}
}

func TestImportEndpointModelsCreatesIndependentImageRoutes(t *testing.T) {
	setupEndpointAdapterTestDB(t)
	channel := &model.Channel{Type: "endpoint-import", BaseURL: "https://images.example", Status: 1}
	if err := model.DB().Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	account := &model.ChannelAccount{ChannelID: channel.ID, APIKey: "secret", Status: 1}
	if err := model.DB().Create(account).Error; err != nil {
		t.Fatal(err)
	}
	templateModel := &model.Model{Code: "image-template-import", Name: "Image template", Type: model.ModelTypeImage, Status: 1}
	if err := model.DB().Create(templateModel).Error; err != nil {
		t.Fatal(err)
	}
	template := &model.Endpoint{
		ModelCode: "image-template-import", ChannelID: channel.ID, AccountID: account.ID,
		Protocol: model.ProtocolOpenAI, RequestPath: "/v1/images/generations", InteractionMode: model.ModeSync,
		Status: 1,
	}
	if err := model.DB().Create(template).Error; err != nil {
		t.Fatal(err)
	}

	result, err := NewEndpointAdapterService().ImportEndpointModels(template.ID, &EndpointModelImportRequest{
		Models: []EndpointModelImportItem{{ID: "gpt-image-1", Name: "GPT Image 1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ModelsCreated != 1 || result.EndpointsCreated != 2 || result.BindingsAdded != 2 || len(result.Endpoints) != 2 {
		t.Fatalf("import result = %#v", result)
	}
	var endpoints []model.Endpoint
	if err := model.DB().Where("model_code = ?", "gpt-image-1").Order("request_path").Find(&endpoints).Error; err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 2 || endpoints[0].RequestPath != "/v1/images/edits" || endpoints[1].RequestPath != "/v1/images/generations" {
		t.Fatalf("imported endpoints = %#v", endpoints)
	}
	if endpoints[0].RouteOperation != RouteOperationImagesEdit || endpoints[1].RouteOperation != RouteOperationImagesGenerate {
		t.Fatalf("route operations = %q, %q", endpoints[0].RouteOperation, endpoints[1].RouteOperation)
	}
	for _, endpoint := range endpoints {
		if endpoint.OriginType != model.EndpointOriginEndpointImport || endpoint.OriginAccountID != account.ID || endpoint.DiscoveredAt == nil {
			t.Fatalf("imported endpoint origin = type %q account %d discovered %v", endpoint.OriginType, endpoint.OriginAccountID, endpoint.DiscoveredAt)
		}
	}
	var adapters []model.EndpointAdapter
	if err := model.DB().Where("endpoint_id IN ?", []uint{endpoints[0].ID, endpoints[1].ID}).Find(&adapters).Error; err != nil {
		t.Fatal(err)
	}
	adapterIDs := make([]uint, 0, len(adapters))
	for _, adapter := range adapters {
		adapterIDs = append(adapterIDs, adapter.ID)
	}
	var revisionCount, bindingCount int64
	model.DB().Model(&model.EndpointAdapterRevision{}).Where("adapter_id IN ?", adapterIDs).Count(&revisionCount)
	model.DB().Model(&model.EndpointAccount{}).Where("endpoint_id IN ?", []uint{endpoints[0].ID, endpoints[1].ID}).Count(&bindingCount)
	if len(adapters) != 2 || revisionCount != 2 || bindingCount != 2 {
		t.Fatalf("adapter=%d revisions=%d bindings=%d", len(adapters), revisionCount, bindingCount)
	}
	if len(endpoints[0].ExtraConfig) == 0 {
		t.Fatalf("edit endpoint extra config = %s", endpoints[0].ExtraConfig)
	}
}

func TestDiscoverAccountModelsRequiresExplicitChannelConfiguration(t *testing.T) {
	setupEndpointAdapterTestDB(t)
	channel := &model.Channel{Type: "account-discovery-disabled", BaseURL: "https://images.example", Status: 1}
	if err := model.DB().Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	account := &model.ChannelAccount{ChannelID: channel.ID, APIKey: "secret", Status: 1}
	if err := model.DB().Create(account).Error; err != nil {
		t.Fatal(err)
	}
	_, err := NewEndpointAdapterService().DiscoverAccountEndpointModels(context.Background(), account.ID)
	if !errors.Is(err, ErrEndpointDiscoveryDisabled) {
		t.Fatalf("error = %v, want discovery disabled", err)
	}
}

func TestDiscoverAccountModelsRejectsDisabledAccount(t *testing.T) {
	setupEndpointAdapterTestDB(t)
	channel := &model.Channel{Type: "account-discovery-disabled-key", BaseURL: "https://images.example", Status: 1, Config: endpointDiscoveryChannelConfig(t, "/v1/models", []string{RouteOperationImagesGenerate})}
	if err := model.DB().Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	account := &model.ChannelAccount{ChannelID: channel.ID, APIKey: "secret", Status: 1}
	if err := model.DB().Create(account).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB().Model(account).Update("status", 0).Error; err != nil {
		t.Fatal(err)
	}
	_, err := NewEndpointAdapterService().DiscoverAccountEndpointModels(context.Background(), account.ID)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("error = %v, want account not found", err)
	}
}

func TestAccountDiscoveryImportReusesRoutesAndBindsSecondKey(t *testing.T) {
	setupEndpointAdapterTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/catalog/models" || r.Header.Get("X-API-Key") == "" {
			t.Fatalf("discovery request = path %s, key %q", r.URL.Path, r.Header.Get("X-API-Key"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"vendor-image"}]}`))
	}))
	defer server.Close()
	previousClient := endpointDiscoveryHTTPClient
	endpointDiscoveryHTTPClient = server.Client()
	t.Cleanup(func() { endpointDiscoveryHTTPClient = previousClient })
	config := endpointDiscoveryChannelConfig(t, "/catalog/models", []string{RouteOperationImagesGenerate, RouteOperationImagesEdit})
	channel := &model.Channel{Type: "account-discovery-import", BaseURL: server.URL, Config: config, Status: 1}
	if err := model.DB().Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	accounts := []*model.ChannelAccount{
		{ChannelID: channel.ID, Name: "key-1", APIKey: "secret-1", Weight: 5, Status: 1},
		{ChannelID: channel.ID, Name: "key-2", APIKey: "secret-2", Weight: 7, Status: 1},
	}
	for _, account := range accounts {
		if err := model.DB().Create(account).Error; err != nil {
			t.Fatal(err)
		}
	}
	service := NewEndpointAdapterService()
	discovered, err := service.DiscoverAccountEndpointModels(context.Background(), accounts[0].ID)
	if err != nil || len(discovered.Models) != 1 || discovered.Models[0].ID != "vendor-image" {
		t.Fatalf("discovered = %#v, err = %v", discovered, err)
	}
	first, err := service.ImportAccountEndpointModels(accounts[0].ID, &EndpointModelImportRequest{Models: []EndpointModelImportItem{{ID: "vendor-image", Operations: []string{RouteOperationImagesGenerate}}}})
	if err != nil {
		t.Fatal(err)
	}
	if first.ModelsCreated != 1 || first.EndpointsCreated != 1 || first.BindingsAdded != 1 {
		t.Fatalf("first import = %#v", first)
	}
	second, err := service.ImportAccountEndpointModels(accounts[1].ID, &EndpointModelImportRequest{Models: []EndpointModelImportItem{{ID: "vendor-image", Operations: []string{RouteOperationImagesGenerate, RouteOperationImagesEdit}}}})
	if err != nil {
		t.Fatal(err)
	}
	if second.ModelsCreated != 0 || second.EndpointsCreated != 1 || second.BindingsAdded != 2 {
		t.Fatalf("second import = %#v", second)
	}
	var endpoints []model.Endpoint
	if err := model.DB().Where("channel_id = ? AND vendor_model = ?", channel.ID, "vendor-image").Find(&endpoints).Error; err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 2 {
		t.Fatalf("endpoints = %#v", endpoints)
	}
	for _, endpoint := range endpoints {
		wantAccountID := accounts[0].ID
		if endpoint.RouteOperation == RouteOperationImagesEdit {
			wantAccountID = accounts[1].ID
		}
		if endpoint.OriginType != model.EndpointOriginKeyDiscovery || endpoint.OriginAccountID != wantAccountID || endpoint.DiscoveredAt == nil {
			t.Fatalf("discovered endpoint origin = type %q account %d discovered %v, want account %d", endpoint.OriginType, endpoint.OriginAccountID, endpoint.DiscoveredAt, wantAccountID)
		}
	}
	var bindings int64
	if err := model.DB().Model(&model.EndpointAccount{}).Where("account_id IN ?", []uint{accounts[0].ID, accounts[1].ID}).Count(&bindings).Error; err != nil {
		t.Fatal(err)
	}
	if bindings != 3 {
		t.Fatalf("bindings = %d, want 3", bindings)
	}
}
