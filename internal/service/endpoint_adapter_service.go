package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const BuiltInOpenAIImagesAdapter = "openai.images"

var (
	ErrEndpointAdapterNotFound   = errors.New("endpoint adapter not found")
	ErrEndpointDiscovery         = errors.New("endpoint model discovery failed")
	ErrEndpointDiscoveryDisabled = errors.New("endpoint discovery is not enabled for this channel")
	ErrEndpointDiscoveryConfig   = errors.New("invalid endpoint discovery configuration")
	endpointDiscoveryHTTPClient  = &http.Client{Timeout: 20 * time.Second}
)

type EndpointAdapterConfig struct {
	DiscoveryPath  string   `json:"discovery_path"`
	GenerationPath string   `json:"generation_path"`
	EditPath       string   `json:"edit_path"`
	Operations     []string `json:"operations"`
}

// ChannelEndpointDiscoveryConfig describes an optional channel-level model
// source. It is stored under channels.config.endpoint_discovery and is never
// inferred from the channel type or from an existing Endpoint.
type ChannelEndpointDiscoveryConfig struct {
	Enabled         bool              `json:"enabled"`
	Adapter         string            `json:"adapter"`
	DiscoveryPath   string            `json:"discovery_path"`
	GenerationPath  string            `json:"generation_path"`
	EditPath        string            `json:"edit_path"`
	Operations      []string          `json:"operations"`
	AuthLocation    string            `json:"auth_location"`
	AuthKey         string            `json:"auth_key"`
	AuthValuePrefix string            `json:"auth_value_prefix"`
	ExtraHeaders    map[string]string `json:"extra_headers"`
	SupportsStream  bool              `json:"supports_stream"`
	DefaultStream   bool              `json:"default_stream"`
}

type EndpointModelDiscoveryItem struct {
	ID        string `json:"id"`
	Object    string `json:"object,omitempty"`
	OwnedBy   string `json:"owned_by,omitempty"`
	ModelCode string `json:"model_code"`
	Imported  bool   `json:"imported"`
}

type EndpointModelDiscoveryResult struct {
	EndpointID uint                         `json:"endpoint_id"`
	Adapter    string                       `json:"adapter"`
	RevisionID uint                         `json:"revision_id"`
	Models     []EndpointModelDiscoveryItem `json:"models"`
	CheckedAt  time.Time                    `json:"checked_at"`
}

type AccountEndpointModelDiscoveryResult struct {
	ChannelID  uint                         `json:"channel_id"`
	AccountID  uint                         `json:"account_id"`
	Adapter    string                       `json:"adapter"`
	Operations []string                     `json:"operations"`
	Models     []EndpointModelDiscoveryItem `json:"models"`
	CheckedAt  time.Time                    `json:"checked_at"`
}

type EndpointModelImportItem struct {
	ID         string   `json:"id" binding:"required"`
	ModelCode  string   `json:"model_code"`
	Name       string   `json:"name"`
	Operations []string `json:"operations"`
}

type EndpointModelImportRequest struct {
	Models []EndpointModelImportItem `json:"models" binding:"required"`
}

type EndpointModelImportResult struct {
	ModelsCreated    int              `json:"models_created"`
	EndpointsCreated int              `json:"endpoints_created"`
	BindingsAdded    int              `json:"bindings_added"`
	Endpoints        []model.Endpoint `json:"endpoints"`
}

type EndpointAdapterService struct{}

func NewEndpointAdapterService() *EndpointAdapterService { return &EndpointAdapterService{} }

// SnapshotEndpointExecution captures the exact Endpoint behavior selected for
// a task and records the adapter revision used to interpret it.
func SnapshotEndpointExecution(endpoint *model.Endpoint) (uint, uint, datatypes.JSON, error) {
	return snapshotEndpointExecutionTx(model.DB(), endpoint)
}

func snapshotEndpointExecutionTx(tx *gorm.DB, endpoint *model.Endpoint) (uint, uint, datatypes.JSON, error) {
	if tx == nil || endpoint == nil || endpoint.ID == 0 {
		return 0, 0, nil, errors.New("endpoint is required")
	}
	var adapterID, revisionID uint
	adapter, revision, _, err := resolveEndpointAdapterTx(tx, endpoint, 0)
	if err == nil {
		adapterID = adapter.ID
		revisionID = revision.ID
	} else if !errors.Is(err, ErrEndpointAdapterNotFound) {
		return 0, 0, nil, err
	}

	snapshot := *endpoint
	snapshot.Model = nil
	snapshot.Channel = nil
	snapshot.AccountBindings = nil
	encoded, err := json.Marshal(&snapshot)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("marshal endpoint execution snapshot: %w", err)
	}
	return adapterID, revisionID, datatypes.JSON(encoded), nil
}

// ApplyTaskEndpointSnapshot replaces mutable Endpoint configuration with the
// version captured when the task selected its execution route.
func ApplyTaskEndpointSnapshot(task *model.Task, endpoint *model.Endpoint) error {
	if task == nil || endpoint == nil {
		return errors.New("task and endpoint are required")
	}
	if len(task.EndpointSnapshot) == 0 {
		return nil
	}
	var snapshot model.Endpoint
	if err := json.Unmarshal(task.EndpointSnapshot, &snapshot); err != nil {
		return fmt.Errorf("decode endpoint execution snapshot: %w", err)
	}
	if snapshot.ID == 0 || snapshot.ID != task.EndpointID || snapshot.ChannelID != task.ChannelID {
		return errors.New("endpoint execution snapshot does not match task route")
	}
	*endpoint = snapshot
	return nil
}

func defaultOpenAIImagesAdapterConfig() EndpointAdapterConfig {
	return EndpointAdapterConfig{
		DiscoveryPath:  "/v1/models",
		GenerationPath: "/v1/images/generations",
		EditPath:       "/v1/images/edits",
		Operations:     []string{RouteOperationImagesGenerate, RouteOperationImagesEdit},
	}
}

func channelEndpointDiscoveryConfig(channel *model.Channel) (ChannelEndpointDiscoveryConfig, error) {
	if channel == nil || len(channel.Config) == 0 {
		return ChannelEndpointDiscoveryConfig{}, ErrEndpointDiscoveryDisabled
	}
	var root struct {
		EndpointDiscovery *ChannelEndpointDiscoveryConfig `json:"endpoint_discovery"`
	}
	if err := json.Unmarshal(channel.Config, &root); err != nil {
		return ChannelEndpointDiscoveryConfig{}, fmt.Errorf("%w: %v", ErrEndpointDiscoveryConfig, err)
	}
	if root.EndpointDiscovery == nil || !root.EndpointDiscovery.Enabled {
		return ChannelEndpointDiscoveryConfig{}, ErrEndpointDiscoveryDisabled
	}
	config := *root.EndpointDiscovery
	config.Adapter = strings.TrimSpace(config.Adapter)
	if config.Adapter != BuiltInOpenAIImagesAdapter {
		return ChannelEndpointDiscoveryConfig{}, fmt.Errorf("%w: unsupported adapter %q", ErrEndpointDiscoveryConfig, config.Adapter)
	}
	defaults := defaultOpenAIImagesAdapterConfig()
	if strings.TrimSpace(config.DiscoveryPath) == "" {
		config.DiscoveryPath = defaults.DiscoveryPath
	}
	if strings.TrimSpace(config.GenerationPath) == "" {
		config.GenerationPath = defaults.GenerationPath
	}
	if strings.TrimSpace(config.EditPath) == "" {
		config.EditPath = defaults.EditPath
	}
	if len(config.Operations) == 0 {
		return ChannelEndpointDiscoveryConfig{}, fmt.Errorf("%w: at least one operation is required", ErrEndpointDiscoveryConfig)
	}
	config.Operations = normalizeEndpointDiscoveryOperations(config.Operations, nil)
	if len(config.Operations) == 0 {
		return ChannelEndpointDiscoveryConfig{}, fmt.Errorf("%w: no supported operations", ErrEndpointDiscoveryConfig)
	}
	for _, path := range []string{config.DiscoveryPath, config.GenerationPath, config.EditPath} {
		if !strings.HasPrefix(strings.TrimSpace(path), "/") || strings.HasPrefix(strings.TrimSpace(path), "//") {
			return ChannelEndpointDiscoveryConfig{}, fmt.Errorf("%w: paths must be absolute URL paths", ErrEndpointDiscoveryConfig)
		}
	}
	config.AuthLocation = strings.ToLower(strings.TrimSpace(config.AuthLocation))
	if config.AuthLocation == "" {
		config.AuthLocation = "header"
	}
	if config.AuthLocation != "header" && config.AuthLocation != "query" {
		return ChannelEndpointDiscoveryConfig{}, fmt.Errorf("%w: auth_location must be header or query", ErrEndpointDiscoveryConfig)
	}
	config.AuthKey = strings.TrimSpace(config.AuthKey)
	if config.AuthKey == "" {
		config.AuthKey = "Authorization"
	}
	if config.AuthLocation == "header" && config.AuthValuePrefix == "" {
		config.AuthValuePrefix = "Bearer "
	}
	return config, nil
}

func normalizeEndpointDiscoveryOperations(requested, allowed []string) []string {
	allowedSet := map[string]bool{
		RouteOperationImagesGenerate: true,
		RouteOperationImagesEdit:     true,
	}
	if len(allowed) > 0 {
		allowedSet = make(map[string]bool, len(allowed))
		for _, operation := range allowed {
			allowedSet[strings.TrimSpace(operation)] = true
		}
	}
	seen := make(map[string]bool, len(requested))
	operations := make([]string, 0, len(requested))
	for _, operation := range requested {
		operation = strings.TrimSpace(operation)
		if allowedSet[operation] && !seen[operation] {
			seen[operation] = true
			operations = append(operations, operation)
		}
	}
	return operations
}

func (s *EndpointAdapterService) DiscoverAccountEndpointModels(ctx context.Context, accountID uint) (*AccountEndpointModelDiscoveryResult, error) {
	account, channel, config, err := loadEndpointDiscoveryAccount(accountID)
	if err != nil {
		return nil, err
	}
	items, err := fetchConfiguredEndpointModels(ctx, channel, account, config)
	if err != nil {
		return nil, err
	}
	return &AccountEndpointModelDiscoveryResult{
		ChannelID: channel.ID, AccountID: account.ID, Adapter: config.Adapter,
		Operations: config.Operations, Models: items, CheckedAt: time.Now(),
	}, nil
}

func (s *EndpointAdapterService) ImportAccountEndpointModels(accountID uint, req *EndpointModelImportRequest) (*EndpointModelImportResult, error) {
	if req == nil || len(req.Models) == 0 {
		return nil, errors.New("models are required")
	}
	account, channel, discovery, err := loadEndpointDiscoveryAccount(accountID)
	if err != nil {
		return nil, err
	}
	adapterConfig := EndpointAdapterConfig{
		DiscoveryPath: discovery.DiscoveryPath, GenerationPath: discovery.GenerationPath,
		EditPath: discovery.EditPath, Operations: discovery.Operations,
	}
	result := &EndpointModelImportResult{Endpoints: make([]model.Endpoint, 0, len(req.Models)*len(discovery.Operations))}
	err = model.DB().Transaction(func(tx *gorm.DB) error {
		var lockedChannel model.Channel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedChannel, channel.ID).Error; err != nil {
			return err
		}
		for _, item := range req.Models {
			vendorModel := strings.TrimSpace(item.ID)
			if vendorModel == "" {
				return errors.New("model id is required")
			}
			modelCode := strings.TrimSpace(item.ModelCode)
			if modelCode == "" {
				modelCode = normalizeDiscoveredModelCode(vendorModel)
			}
			name := strings.TrimSpace(item.Name)
			if name == "" {
				name = vendorModel
			}
			operations := normalizeEndpointDiscoveryOperations(item.Operations, discovery.Operations)
			if len(operations) == 0 {
				return fmt.Errorf("model %s has no supported operations", vendorModel)
			}
			if err := ensureDiscoveredImageModel(tx, channel, modelCode, name, operations, result); err != nil {
				return err
			}
			for _, operation := range operations {
				endpoint, created, err := ensureDiscoveredImageEndpoint(tx, channel, account, discovery, adapterConfig, modelCode, vendorModel, operation)
				if err != nil {
					return err
				}
				if created {
					result.EndpointsCreated++
				}
				added, err := ensureEndpointAccountBinding(tx, endpoint.ID, account)
				if err != nil {
					return err
				}
				if added {
					result.BindingsAdded++
				}
				result.Endpoints = append(result.Endpoints, *endpoint)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func loadEndpointDiscoveryAccount(accountID uint) (*model.ChannelAccount, *model.Channel, ChannelEndpointDiscoveryConfig, error) {
	if accountID == 0 {
		return nil, nil, ChannelEndpointDiscoveryConfig{}, gorm.ErrRecordNotFound
	}
	var account model.ChannelAccount
	if err := model.DB().Where("id = ? AND status = 1", accountID).First(&account).Error; err != nil {
		return nil, nil, ChannelEndpointDiscoveryConfig{}, err
	}
	if strings.TrimSpace(account.APIKey) == "" {
		return nil, nil, ChannelEndpointDiscoveryConfig{}, fmt.Errorf("%w: account API key is empty", ErrEndpointDiscoveryConfig)
	}
	var channel model.Channel
	if err := model.DB().Where("id = ? AND status = 1", account.ChannelID).First(&channel).Error; err != nil {
		return nil, nil, ChannelEndpointDiscoveryConfig{}, err
	}
	config, err := channelEndpointDiscoveryConfig(&channel)
	if err != nil {
		return nil, nil, ChannelEndpointDiscoveryConfig{}, err
	}
	return &account, &channel, config, nil
}

func fetchConfiguredEndpointModels(ctx context.Context, channel *model.Channel, account *model.ChannelAccount, config ChannelEndpointDiscoveryConfig) ([]EndpointModelDiscoveryItem, error) {
	items, err := fetchEndpointModelList(ctx, channel, account, config.DiscoveryPath, config.AuthLocation, config.AuthKey, config.AuthValuePrefix, config.ExtraHeaders)
	if err != nil {
		return nil, err
	}
	for index := range items {
		var operationCount int64
		if err := model.DB().Model(&model.Endpoint{}).
			Distinct("endpoints.route_operation").
			Joins("JOIN endpoint_accounts ON endpoint_accounts.endpoint_id = endpoints.id").
			Where("endpoints.channel_id = ? AND endpoints.vendor_model = ? AND endpoints.route_operation IN ? AND endpoint_accounts.account_id = ?",
				channel.ID, items[index].ID, config.Operations, account.ID).
			Count(&operationCount).Error; err != nil {
			return nil, err
		}
		items[index].Imported = operationCount == int64(len(config.Operations))
	}
	return items, nil
}

func ensureDiscoveredImageModel(tx *gorm.DB, channel *model.Channel, modelCode, name string, operations []string, result *EndpointModelImportResult) error {
	var capability model.Model
	lookup := tx.Unscoped().Where("code = ?", modelCode).First(&capability)
	if errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
		features := make(map[string]bool, len(operations))
		for _, operation := range operations {
			features[operation] = true
		}
		encoded, err := json.Marshal(features)
		if err != nil {
			return err
		}
		capability = model.Model{
			Code: modelCode, Name: name, Type: model.ModelTypeImage, Provider: channel.Type,
			Protocol: model.ProtocolOpenAI, Features: datatypes.JSON(encoded), Status: 1,
		}
		if err := tx.Create(&capability).Error; err != nil {
			return err
		}
		result.ModelsCreated++
		return nil
	}
	if lookup.Error != nil {
		return lookup.Error
	}
	if capability.Type != model.ModelTypeImage {
		return fmt.Errorf("model code %s already exists with type %s", modelCode, capability.Type)
	}
	features := map[string]any{}
	if len(capability.Features) > 0 {
		_ = json.Unmarshal(capability.Features, &features)
	}
	changed := false
	for _, operation := range operations {
		if enabled, ok := features[operation].(bool); !ok || !enabled {
			features[operation] = true
			changed = true
		}
	}
	updates := map[string]any{}
	if changed {
		encoded, err := json.Marshal(features)
		if err != nil {
			return err
		}
		updates["features"] = datatypes.JSON(encoded)
	}
	if capability.DeletedAt.Valid {
		updates["deleted_at"] = nil
		updates["status"] = 1
	}
	if len(updates) == 0 {
		return nil
	}
	return tx.Unscoped().Model(&capability).Updates(updates).Error
}

func ensureDiscoveredImageEndpoint(
	tx *gorm.DB,
	channel *model.Channel,
	account *model.ChannelAccount,
	discovery ChannelEndpointDiscoveryConfig,
	adapterConfig EndpointAdapterConfig,
	modelCode, vendorModel, operation string,
) (*model.Endpoint, bool, error) {
	var endpoint model.Endpoint
	lookup := tx.Where(
		"channel_id = ? AND model_code = ? AND vendor_model = ? AND route_operation = ?",
		channel.ID, modelCode, vendorModel, operation,
	).First(&endpoint)
	created := false
	if errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
		endpoint = discoveredImageEndpoint(channel, account, discovery, modelCode, vendorModel, operation)
		if err := tx.Create(&endpoint).Error; err != nil {
			return nil, false, err
		}
		created = true
	} else if lookup.Error != nil {
		return nil, false, lookup.Error
	}
	if _, _, _, err := ensureAdapterTx(tx, &endpoint, discovery.Adapter, adapterConfig, 0); err != nil {
		return nil, false, err
	}
	return &endpoint, created, nil
}

func discoveredImageEndpoint(channel *model.Channel, account *model.ChannelAccount, config ChannelEndpointDiscoveryConfig, modelCode, vendorModel, operation string) model.Endpoint {
	path := config.GenerationPath
	contentType := "application/json"
	extraConfig := datatypes.JSON(`{"adapter":"openai.images"}`)
	if operation == RouteOperationImagesEdit {
		path = config.EditPath
		contentType = "multipart/form-data"
		encoded, _ := json.Marshal(map[string]any{
			"adapter": BuiltInOpenAIImagesAdapter,
			"image_edit": map[string]any{
				"enabled": true, "input_mode": model.ImageInputModeMultipart,
				"edit_path": config.EditPath, "file_field": "image",
			},
		})
		extraConfig = datatypes.JSON(encoded)
	}
	extraHeaders, _ := json.Marshal(config.ExtraHeaders)
	interactionMode := model.ModeSync
	if config.DefaultStream {
		interactionMode = model.ModeStream
	}
	responseMapping := datatypes.JSON(`{"field_mapping":{"url":"data[0].url","urls":"data[].url","b64_json":"data[0].b64_json","revised_prompt":"data[0].revised_prompt"},"success_condition":{"field":"data","operator":"exists"}}`)
	paramSchema := defaultImageEndpointParamSchema()
	discoveredAt := time.Now()
	supportedOperations, _ := json.Marshal([]string{operation})
	return model.Endpoint{
		ModelCode: modelCode, RouteOperation: operation, SupportedOperations: datatypes.JSON(supportedOperations), ChannelID: channel.ID,
		OriginType: model.EndpointOriginKeyDiscovery, OriginAccountID: account.ID,
		OriginSnapshot: buildEndpointOriginSnapshot(channel, account, vendorModel, config.Adapter, 0), DiscoveredAt: &discoveredAt,
		Protocol: model.ProtocolOpenAI, RequestPath: path, RequestMethod: http.MethodPost, ContentType: contentType,
		AuthLocation: config.AuthLocation, AuthKey: config.AuthKey, AuthValuePrefix: config.AuthValuePrefix,
		VendorModel: vendorModel, InteractionMode: interactionMode, SupportsStream: config.SupportsStream,
		DefaultStream: config.DefaultStream, PriceMode: model.PriceModeRequest,
		ParamSchema: paramSchema, ParamMapping: datatypes.JSON(fmt.Sprintf(`{"fixed_params":{"model":%q}}`, vendorModel)),
		ResponseMapping: responseMapping, ExtraHeaders: datatypes.JSON(extraHeaders), ExtraConfig: extraConfig,
		Timeout: DefaultSyncWaitMaxSeconds, Status: 1,
	}
}

func ensureEndpointAccountBinding(tx *gorm.DB, endpointID uint, account *model.ChannelAccount) (bool, error) {
	var binding model.EndpointAccount
	lookup := tx.Where("endpoint_id = ? AND account_id = ?", endpointID, account.ID).First(&binding)
	if lookup.Error == nil {
		return false, nil
	}
	if !errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
		return false, lookup.Error
	}
	weight := account.Weight
	if weight <= 0 {
		weight = 10
	}
	binding = model.EndpointAccount{EndpointID: endpointID, AccountID: account.ID, Status: 1, Weight: weight}
	if err := tx.Create(&binding).Error; err != nil {
		return false, err
	}
	return true, nil
}

func (s *EndpointAdapterService) DiscoverEndpointModels(ctx context.Context, endpointID uint) (*EndpointModelDiscoveryResult, error) {
	var endpoint model.Endpoint
	if err := model.DB().First(&endpoint, endpointID).Error; err != nil {
		return nil, err
	}
	adapter, revision, config, err := resolveEndpointAdapterTx(model.DB(), &endpoint, 0)
	if err != nil {
		return nil, err
	}

	var channel model.Channel
	if err := model.DB().First(&channel, endpoint.ChannelID).Error; err != nil {
		return nil, err
	}
	account, err := selectDiscoveryAccount(&endpoint)
	if err != nil {
		return nil, err
	}
	items, err := fetchEndpointModels(ctx, &channel, account, &endpoint, config)
	checkedAt := time.Now()
	if stateErr := s.saveDiscoveryState(endpoint.ID, account.ID, adapter.ID, revision.ID, items, checkedAt, err); stateErr != nil && err == nil {
		return nil, stateErr
	}
	if err != nil {
		return nil, err
	}
	return &EndpointModelDiscoveryResult{
		EndpointID: endpoint.ID,
		Adapter:    adapter.Code,
		RevisionID: revision.ID,
		Models:     items,
		CheckedAt:  checkedAt,
	}, nil
}

func (s *EndpointAdapterService) ImportEndpointModels(endpointID uint, req *EndpointModelImportRequest) (*EndpointModelImportResult, error) {
	if req == nil || len(req.Models) == 0 {
		return nil, errors.New("models are required")
	}
	var template model.Endpoint
	if err := model.DB().First(&template, endpointID).Error; err != nil {
		return nil, err
	}
	var channel model.Channel
	if err := model.DB().First(&channel, template.ChannelID).Error; err != nil {
		return nil, err
	}
	adapter, _, config, err := resolveEndpointAdapterTx(model.DB(), &template, 0)
	if err != nil {
		return nil, err
	}
	code := adapter.Code
	account, err := selectDiscoveryAccount(&template)
	if err != nil {
		return nil, err
	}
	result := &EndpointModelImportResult{Endpoints: make([]model.Endpoint, 0, len(req.Models)*len(config.Operations))}
	err = model.DB().Transaction(func(tx *gorm.DB) error {
		for _, item := range req.Models {
			vendorModel := strings.TrimSpace(item.ID)
			if vendorModel == "" {
				return errors.New("model id is required")
			}
			modelCode := strings.TrimSpace(item.ModelCode)
			if modelCode == "" {
				modelCode = normalizeDiscoveredModelCode(vendorModel)
			}
			name := strings.TrimSpace(item.Name)
			if name == "" {
				name = vendorModel
			}
			requestedOperations := item.Operations
			if len(requestedOperations) == 0 {
				requestedOperations = config.Operations
			}
			operations := normalizeEndpointDiscoveryOperations(requestedOperations, config.Operations)
			if len(operations) == 0 {
				return fmt.Errorf("model %s has no supported operations", vendorModel)
			}
			if err := ensureDiscoveredImageModel(tx, &channel, modelCode, name, operations, result); err != nil {
				return err
			}

			for _, operation := range operations {
				path := config.GenerationPath
				contentType := "application/json"
				extraConfig := datatypes.JSON(`{"adapter":"openai.images"}`)
				if operation == RouteOperationImagesEdit {
					path = config.EditPath
					contentType = "multipart/form-data"
					editConfig, _ := json.Marshal(map[string]any{
						"adapter": BuiltInOpenAIImagesAdapter,
						"image_edit": map[string]any{
							"enabled": true, "input_mode": "multipart", "edit_path": config.EditPath, "file_field": "image",
						},
					})
					extraConfig = datatypes.JSON(editConfig)
				}
				var endpoint model.Endpoint
				find := tx.Where(
					"channel_id = ? AND model_code = ? AND vendor_model = ? AND route_operation = ?",
					template.ChannelID, modelCode, vendorModel, operation,
				).First(&endpoint)
				if errors.Is(find.Error, gorm.ErrRecordNotFound) {
					legacy := tx.Where(
						"channel_id = ? AND model_code = ? AND vendor_model = ? AND request_path = ? AND route_operation = ''",
						template.ChannelID, modelCode, vendorModel, path,
					).First(&endpoint)
					if legacy.Error == nil {
						if err := tx.Model(&endpoint).Update("route_operation", operation).Error; err != nil {
							return err
						}
						endpoint.RouteOperation = operation
					} else if errors.Is(legacy.Error, gorm.ErrRecordNotFound) {
						endpoint = importedImageEndpoint(&template, &channel, account, code, modelCode, vendorModel, operation, path, contentType, extraConfig)
						if err := tx.Create(&endpoint).Error; err != nil {
							return err
						}
						result.EndpointsCreated++
					} else {
						return legacy.Error
					}
				} else if find.Error != nil {
					return find.Error
				}
				added, err := ensureEndpointAccountBinding(tx, endpoint.ID, account)
				if err != nil {
					return err
				}
				if added {
					result.BindingsAdded++
				}
				if _, _, _, err := ensureAdapterTx(tx, &endpoint, code, config, 0); err != nil {
					return err
				}
				result.Endpoints = append(result.Endpoints, endpoint)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func resolveEndpointAdapterTx(tx *gorm.DB, endpoint *model.Endpoint, actorID uint) (*model.EndpointAdapter, *model.EndpointAdapterRevision, EndpointAdapterConfig, error) {
	if tx == nil || endpoint == nil || endpoint.ID == 0 {
		return nil, nil, EndpointAdapterConfig{}, errors.New("endpoint is required")
	}
	code := adapterCodeForEndpoint(endpoint)
	if code == "" {
		return nil, nil, EndpointAdapterConfig{}, ErrEndpointAdapterNotFound
	}
	var adapter model.EndpointAdapter
	lookup := tx.Where("endpoint_id = ? AND code = ? AND status = 1", endpoint.ID, code).First(&adapter)
	if lookup.Error == nil {
		if adapter.ActiveRevisionID == 0 {
			return nil, nil, EndpointAdapterConfig{}, errors.New("endpoint adapter has no active revision")
		}
		var revision model.EndpointAdapterRevision
		if err := tx.Where("id = ? AND adapter_id = ?", adapter.ActiveRevisionID, adapter.ID).First(&revision).Error; err != nil {
			return nil, nil, EndpointAdapterConfig{}, err
		}
		var config EndpointAdapterConfig
		if err := json.Unmarshal(revision.Config, &config); err != nil {
			return nil, nil, EndpointAdapterConfig{}, fmt.Errorf("decode endpoint adapter revision: %w", err)
		}
		return &adapter, &revision, normalizeAdapterConfig(adapter.Code, config), nil
	}
	if !errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
		return nil, nil, EndpointAdapterConfig{}, lookup.Error
	}
	return ensureAdapterTx(tx, endpoint, code, adapterConfigForEndpoint(code), actorID)
}

func ensureAdapterTx(tx *gorm.DB, endpoint *model.Endpoint, code string, config EndpointAdapterConfig, actorID uint) (*model.EndpointAdapter, *model.EndpointAdapterRevision, EndpointAdapterConfig, error) {
	config = normalizeAdapterConfig(code, config)
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, nil, config, err
	}
	digestBytes := sha256.Sum256(encoded)
	digest := hex.EncodeToString(digestBytes[:])
	var adapter model.EndpointAdapter
	lookup := tx.Where("endpoint_id = ? AND code = ?", endpoint.ID, code).First(&adapter)
	if errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
		adapter = model.EndpointAdapter{EndpointID: endpoint.ID, Code: code, Status: 1, Config: encoded}
		if err := tx.Create(&adapter).Error; err != nil {
			return nil, nil, config, err
		}
	} else if lookup.Error != nil {
		return nil, nil, config, lookup.Error
	}
	var revision model.EndpointAdapterRevision
	if adapter.ActiveRevisionID > 0 {
		if err := tx.Where("id = ? AND adapter_id = ?", adapter.ActiveRevisionID, adapter.ID).First(&revision).Error; err != nil {
			return nil, nil, config, err
		}
		if revision.Digest == digest {
			return &adapter, &revision, config, nil
		}
	}
	var maxVersion int
	if err := tx.Model(&model.EndpointAdapterRevision{}).Where("adapter_id = ?", adapter.ID).Select("COALESCE(MAX(version), 0)").Scan(&maxVersion).Error; err != nil {
		return nil, nil, config, err
	}
	revision = model.EndpointAdapterRevision{AdapterID: adapter.ID, Version: maxVersion + 1, Digest: digest, Config: encoded, CreatedBy: actorID}
	if err := tx.Create(&revision).Error; err != nil {
		return nil, nil, config, err
	}
	if err := tx.Model(&adapter).Updates(map[string]any{"active_revision_id": revision.ID, "config": encoded}).Error; err != nil {
		return nil, nil, config, err
	}
	adapter.ActiveRevisionID = revision.ID
	adapter.Config = encoded
	return &adapter, &revision, config, nil
}

func (s *EndpointAdapterService) saveDiscoveryState(endpointID, accountID, adapterID, revisionID uint, items []EndpointModelDiscoveryItem, checkedAt time.Time, discoveryErr error) error {
	encoded, _ := json.Marshal(items)
	updates := map[string]any{
		"adapter_id": adapterID, "revision_id": revisionID, "discovered_models": datatypes.JSON(encoded),
		"last_discovery_at": checkedAt, "updated_at": checkedAt,
	}
	if discoveryErr != nil {
		updates["last_error"] = truncate(discoveryErr.Error(), 1000)
		updates["status_code"] = 502
	} else {
		updates["last_error"] = ""
		updates["status_code"] = 200
		updates["last_success_at"] = checkedAt
	}
	state := model.EndpointRouteState{
		EndpointID: endpointID, AccountID: accountID, RouteOperation: RouteOperationImagesGenerate,
		AdapterID: adapterID, RevisionID: revisionID, Status: 1, Discovered: datatypes.JSON(encoded),
	}
	db := model.DB().Where("endpoint_id = ? AND account_id = ? AND route_operation = ?", endpointID, accountID, RouteOperationImagesGenerate).FirstOrCreate(&state)
	if db.Error != nil {
		return db.Error
	}
	return model.DB().Model(&state).Updates(updates).Error
}

func fetchEndpointModels(ctx context.Context, channel *model.Channel, account *model.ChannelAccount, endpoint *model.Endpoint, config EndpointAdapterConfig) ([]EndpointModelDiscoveryItem, error) {
	items, err := fetchEndpointModelList(ctx, channel, account, config.DiscoveryPath, endpoint.AuthLocation, endpoint.AuthKey, endpoint.AuthValuePrefix, nil)
	if err != nil {
		return nil, err
	}
	for index := range items {
		var count int64
		if err := model.DB().Model(&model.Endpoint{}).
			Where("channel_id = ? AND vendor_model = ?", endpoint.ChannelID, items[index].ID).
			Count(&count).Error; err != nil {
			return nil, err
		}
		items[index].Imported = count > 0
	}
	return items, nil
}

func fetchEndpointModelList(
	ctx context.Context,
	channel *model.Channel,
	account *model.ChannelAccount,
	discoveryPath, authLocation, authKey, authValuePrefix string,
	extraHeaders map[string]string,
) ([]EndpointModelDiscoveryItem, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(channel.BaseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("%w: channel base URL is empty", ErrEndpointDiscovery)
	}
	path := strings.TrimSpace(discoveryPath)
	if path == "" {
		path = "/v1/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEndpointDiscovery, err)
	}
	authLocation = strings.ToLower(strings.TrimSpace(authLocation))
	authKey = strings.TrimSpace(authKey)
	if authKey == "" {
		authKey = "Authorization"
	}
	prefix := authValuePrefix
	if prefix == "" && authLocation != "query" {
		prefix = "Bearer "
	}
	if authLocation == "" || authLocation == "header" {
		req.Header.Set(authKey, prefix+account.APIKey)
	} else if authLocation == "query" {
		query := req.URL.Query()
		query.Set(authKey, account.APIKey)
		req.URL.RawQuery = query.Encode()
	}
	for key, value := range extraHeaders {
		req.Header.Set(key, value)
	}
	resp, err := endpointDiscoveryHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: request upstream models: %v", ErrEndpointDiscovery, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: read upstream models: %v", ErrEndpointDiscovery, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: upstream returned %d: %s", ErrEndpointDiscovery, resp.StatusCode, truncate(string(body), 300))
	}
	var envelope struct {
		Data []EndpointModelDiscoveryItem `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Data == nil {
		var raw []EndpointModelDiscoveryItem
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("%w: invalid model list response", ErrEndpointDiscovery)
		}
		envelope.Data = raw
	}
	seen := map[string]struct{}{}
	items := make([]EndpointModelDiscoveryItem, 0, len(envelope.Data))
	for _, item := range envelope.Data {
		item.ID = strings.TrimSpace(item.ID)
		if item.ID == "" {
			continue
		}
		if _, ok := seen[item.ID]; ok {
			continue
		}
		seen[item.ID] = struct{}{}
		item.ModelCode = normalizeDiscoveredModelCode(item.ID)
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func selectDiscoveryAccount(endpoint *model.Endpoint) (*model.ChannelAccount, error) {
	if endpoint.AccountID > 0 {
		var account model.ChannelAccount
		if err := model.DB().Where("id = ? AND channel_id = ? AND status = 1", endpoint.AccountID, endpoint.ChannelID).First(&account).Error; err == nil {
			return &account, nil
		}
	}
	var binding model.EndpointAccount
	if err := model.DB().Where("endpoint_id = ? AND status = 1", endpoint.ID).Order("priority DESC, id ASC").First(&binding).Error; err == nil {
		var account model.ChannelAccount
		if err := model.DB().Where("id = ? AND channel_id = ? AND status = 1", binding.AccountID, endpoint.ChannelID).First(&account).Error; err == nil {
			return &account, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func adapterCodeForEndpoint(endpoint *model.Endpoint) string {
	if endpoint == nil {
		return ""
	}
	if len(endpoint.ExtraConfig) > 0 {
		var config struct {
			Adapter string `json:"adapter"`
		}
		if json.Unmarshal(endpoint.ExtraConfig, &config) == nil && config.Adapter == BuiltInOpenAIImagesAdapter {
			return BuiltInOpenAIImagesAdapter
		}
	}
	if strings.EqualFold(string(endpoint.Protocol), string(model.ProtocolOpenAI)) {
		switch strings.TrimSpace(endpoint.RouteOperation) {
		case RouteOperationImagesGenerate, RouteOperationImagesEdit:
			return BuiltInOpenAIImagesAdapter
		}
	}
	if strings.EqualFold(string(endpoint.Protocol), string(model.ProtocolOpenAI)) &&
		(strings.Contains(strings.ToLower(endpoint.RequestPath), "/images/generations") || strings.Contains(strings.ToLower(endpoint.RequestPath), "/images/edits")) {
		return BuiltInOpenAIImagesAdapter
	}
	return ""
}

func adapterConfigForEndpoint(code string) EndpointAdapterConfig {
	if code == BuiltInOpenAIImagesAdapter {
		return defaultOpenAIImagesAdapterConfig()
	}
	return EndpointAdapterConfig{}
}

func normalizeAdapterConfig(code string, config EndpointAdapterConfig) EndpointAdapterConfig {
	if code == BuiltInOpenAIImagesAdapter {
		defaults := defaultOpenAIImagesAdapterConfig()
		if config.DiscoveryPath == "" {
			config.DiscoveryPath = defaults.DiscoveryPath
		}
		if config.GenerationPath == "" {
			config.GenerationPath = defaults.GenerationPath
		}
		if config.EditPath == "" {
			config.EditPath = defaults.EditPath
		}
		if len(config.Operations) == 0 {
			config.Operations = defaults.Operations
		}
	}
	return config
}

var discoveredModelCodePattern = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func normalizeDiscoveredModelCode(id string) string {
	value := strings.TrimSpace(id)
	value = discoveredModelCodePattern.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-._")
	if value == "" {
		value = "image-model"
	}
	if len(value) > 80 {
		digest := sha256.Sum256([]byte(id))
		value = value[:72] + "-" + hex.EncodeToString(digest[:])[:7]
	}
	return value
}

func importedImageEndpoint(template *model.Endpoint, channel *model.Channel, account *model.ChannelAccount, adapter, modelCode, vendorModel, operation, path, contentType string, extraConfig datatypes.JSON) model.Endpoint {
	responseMapping := datatypes.JSON(`{"field_mapping":{"url":"data[0].url","urls":"data[].url","b64_json":"data[0].b64_json","revised_prompt":"data[0].revised_prompt"},"success_condition":{"field":"data","operator":"exists"}}`)
	paramSchema := defaultImageEndpointParamSchema()
	discoveredAt := time.Now()
	supportedOperations, _ := json.Marshal([]string{operation})
	return model.Endpoint{
		ModelCode: modelCode, RouteOperation: operation, SupportedOperations: datatypes.JSON(supportedOperations), ChannelID: template.ChannelID, AccountID: template.AccountID,
		OriginType: model.EndpointOriginEndpointImport, OriginAccountID: account.ID,
		OriginSnapshot: buildEndpointOriginSnapshot(channel, account, vendorModel, adapter, template.ID), DiscoveredAt: &discoveredAt,
		Protocol: model.ProtocolOpenAI, RequestPath: path, RequestMethod: "POST", ContentType: contentType,
		AuthLocation: template.AuthLocation, AuthKey: template.AuthKey, AuthValuePrefix: template.AuthValuePrefix,
		VendorModel: vendorModel, InteractionMode: template.InteractionMode, SupportsStream: template.SupportsStream,
		DefaultStream: template.DefaultStream, PriceMode: template.PriceMode, InputPrice: template.InputPrice,
		OutputPrice: template.OutputPrice, ParamSchema: paramSchema,
		ParamMapping:    datatypes.JSON(fmt.Sprintf(`{"fixed_params":{"model":%q}}`, vendorModel)),
		ResponseMapping: responseMapping, ExtraHeaders: template.ExtraHeaders, ExtraConfig: extraConfig,
		Timeout: template.Timeout, Priority: template.Priority, Status: 1,
	}
}
