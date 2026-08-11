package service

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"golang.org/x/text/encoding/simplifiedchinese"
	"gorm.io/datatypes"
)

type QueryService struct{}

type AvailableModelOperation struct {
	ID             string          `json:"id"`
	Path           string          `json:"path"`
	SupportsStream bool            `json:"supports_stream"`
	ParamSchema    json.RawMessage `json:"param_schema,omitempty"`
}

type AvailableModelCapability struct {
	ID                     string                    `json:"id"`
	Code                   string                    `json:"code"` // Compatibility alias for existing Prism clients.
	Object                 string                    `json:"object"`
	Name                   string                    `json:"name"`
	Type                   string                    `json:"type"`
	Description            string                    `json:"description"`
	ParamSchema            json.RawMessage           `json:"param_schema,omitempty"`
	Operations             []AvailableModelOperation `json:"operations"`
	Channels               []gin.H                   `json:"channels"` // Deprecated: callers should use operations.
	SupportsStream         bool                      `json:"supports_stream,omitempty"`
	DefaultStream          bool                      `json:"default_stream,omitempty"`
	SupportsTools          bool                      `json:"supports_tools,omitempty"`
	SupportsResponseFormat bool                      `json:"supports_response_format,omitempty"`
	SupportsMultimodal     bool                      `json:"supports_multimodal,omitempty"`
	MaxTokens              int                       `json:"max_tokens,omitempty"`
	Group                  string                    `json:"group,omitempty"`
	Thinking               *ThinkingInfo             `json:"thinking,omitempty"`
	Sort                   int                       `json:"-"`
	operationIDs           map[string]struct{}
	channelIDs             map[string]struct{}
}

func NewQueryService() *QueryService {
	return &QueryService{}
}

func (s *QueryService) ListAvailableChannels() ([]string, error) {
	var channels []model.Channel
	if err := model.DB().Where("status = ?", 1).Find(&channels).Error; err != nil {
		return nil, err
	}

	result := make([]string, 0, len(channels))
	for _, ch := range channels {
		result = append(result, ch.Type)
	}
	return result, nil
}

func (s *QueryService) ListAvailableCapabilities(channelType, modelType string) ([]AvailableModelCapability, error) {
	items := make(map[string]*AvailableModelCapability)
	if modelType == "" || modelType == string(model.ModelTypeChat) {
		if err := s.addAvailableChatModels(items, channelType); err != nil {
			return nil, err
		}
	}
	if modelType != string(model.ModelTypeChat) {
		if err := s.addAvailableEndpointModels(items, channelType, modelType); err != nil {
			return nil, err
		}
	}

	result := make([]AvailableModelCapability, 0, len(items))
	for _, item := range items {
		sort.SliceStable(item.Operations, func(i, j int) bool {
			return operationRank(item.Operations[i].ID) < operationRank(item.Operations[j].ID)
		})
		item.operationIDs = nil
		item.channelIDs = nil
		result = append(result, *item)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Sort != result[j].Sort {
			return result[i].Sort < result[j].Sort
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (s *QueryService) addAvailableEndpointModels(items map[string]*AvailableModelCapability, channelType, modelType string) error {
	query := model.DB().Where("status = ?", 1).Where("type <> ?", model.ModelTypeChat).Order("sort DESC, code")
	if modelType != "" {
		query = query.Where("type = ?", modelType)
	}
	var models []model.Model
	if err := query.Find(&models).Error; err != nil {
		return err
	}
	modelByCode := make(map[string]model.Model, len(models))
	for _, item := range models {
		modelByCode[item.Code] = item
	}

	channelQuery := model.DB().Where("status = ?", 1)
	if channelType != "" {
		channelQuery = channelQuery.Where("type = ?", channelType)
	}
	var channels []model.Channel
	if err := channelQuery.Find(&channels).Error; err != nil {
		return err
	}
	channelByID := make(map[uint]model.Channel, len(channels))
	for _, channel := range channels {
		channelByID[channel.ID] = channel
	}

	var accounts []model.ChannelAccount
	if err := model.DB().Where("status = ?", 1).Find(&accounts).Error; err != nil {
		return err
	}
	accountByID := make(map[uint]model.ChannelAccount, len(accounts))
	for _, account := range accounts {
		if strings.TrimSpace(account.APIKey) != "" {
			accountByID[account.ID] = account
		}
	}

	var bindings []model.EndpointAccount
	if err := model.DB().Where("status = ?", 1).Find(&bindings).Error; err != nil {
		return err
	}
	boundAccounts := make(map[uint][]uint)
	for _, binding := range bindings {
		boundAccounts[binding.EndpointID] = append(boundAccounts[binding.EndpointID], binding.AccountID)
	}

	var endpoints []model.Endpoint
	if err := model.DB().Where("status = ?", 1).Order("priority DESC, id ASC").Find(&endpoints).Error; err != nil {
		return err
	}
	for _, endpoint := range endpoints {
		definition, ok := modelByCode[endpoint.ModelCode]
		if !ok {
			continue
		}
		channel, ok := channelByID[endpoint.ChannelID]
		if !ok || !endpointHasAvailableAccount(endpoint, boundAccounts[endpoint.ID], accountByID) {
			continue
		}

		operations := endpointAvailableRouteOperations(&endpoint, definition.Type)
		if len(operations) == 0 {
			continue
		}
		item := ensureAvailableModel(items, definition.Code, definition.Name, string(definition.Type))
		item.Description = definition.Description
		item.ParamSchema = ensureUTF8(definition.ParamSchema)
		if definition.Type == model.ModelTypeImage {
			item.ParamSchema = json.RawMessage(normalizeImageEndpointParamSchema(datatypes.JSON(item.ParamSchema)))
		}
		item.Sort = -definition.Sort
		operationSchema := ensureUTF8(endpoint.ParamSchema)
		if definition.Type == model.ModelTypeImage {
			operationSchema = json.RawMessage(normalizeImageEndpointParamSchema(datatypes.JSON(operationSchema)))
		}
		if len(operationSchema) == 0 {
			operationSchema = item.ParamSchema
		}
		for _, operationID := range operations {
			item.addOperation(AvailableModelOperation{
				ID:             operationID,
				Path:           publicOperationPath(operationID),
				SupportsStream: endpoint.SupportsStream,
				ParamSchema:    operationSchema,
			})
			item.addLegacyChannel(endpoint, channel, operationID)
		}
	}
	return nil
}

func (s *QueryService) addAvailableChatModels(items map[string]*AvailableModelCapability, channelType string) error {
	// The legacy channel filter applies to capability channels, not gateway channels.
	if channelType != "" {
		return nil
	}
	gatewayService := NewGatewayAdminService()
	rows, err := gatewayService.ListModels()
	if err != nil {
		return err
	}
	transports, err := gatewayService.ListModelTransports()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.KeyAvailable <= 0 || len(transports[row.ModelName]) == 0 {
			continue
		}
		item := ensureAvailableModel(items, row.ModelName, row.DisplayName, string(model.ModelTypeChat))
		item.Sort = row.Sort
		item.SupportsStream = true
		item.DefaultStream = true
		item.MaxTokens = row.MaxTokens
		if group := strings.TrimSpace(row.GroupName); group != "" {
			item.Group = group
		} else if source := strings.TrimSpace(row.SourceChannel); source != "" {
			item.Group = source
		} else {
			item.Group = "未分组"
		}
		if len(row.Features) > 0 {
			var features []string
			if json.Unmarshal(row.Features, &features) == nil {
				for _, feature := range features {
					switch feature {
					case "tools":
						item.SupportsTools = true
					case "vision":
						item.SupportsMultimodal = true
					case "structured_output":
						item.SupportsResponseFormat = true
					}
				}
			}
		}
		if config := parseThinkingConfig(row.ThinkingConfig); config != nil {
			thinking := &ThinkingInfo{Default: config.Default, Locked: config.Locked}
			for _, option := range config.Options {
				thinking.Options = append(thinking.Options, ThinkingLevelInfo{Label: option.Label, Value: option.Value})
			}
			item.Thinking = thinking
		}
		for _, operationID := range []string{"chat.completions", "responses.create", "messages.create"} {
			item.addOperation(AvailableModelOperation{
				ID:             operationID,
				Path:           publicOperationPath(operationID),
				SupportsStream: true,
			})
		}
	}
	return nil
}

func ensureAvailableModel(items map[string]*AvailableModelCapability, id, name, modelType string) *AvailableModelCapability {
	if item, ok := items[id]; ok {
		return item
	}
	if strings.TrimSpace(name) == "" {
		name = id
	}
	item := &AvailableModelCapability{
		ID:           id,
		Code:         id,
		Object:       "model_capability",
		Name:         name,
		Type:         modelType,
		Operations:   []AvailableModelOperation{},
		Channels:     []gin.H{},
		operationIDs: make(map[string]struct{}),
		channelIDs:   make(map[string]struct{}),
	}
	items[id] = item
	return item
}

func (item *AvailableModelCapability) addOperation(operation AvailableModelOperation) {
	if _, exists := item.operationIDs[operation.ID]; exists {
		for i := range item.Operations {
			if item.Operations[i].ID != operation.ID {
				continue
			}
			item.Operations[i].SupportsStream = item.Operations[i].SupportsStream || operation.SupportsStream
			if len(item.Operations[i].ParamSchema) == 0 {
				item.Operations[i].ParamSchema = operation.ParamSchema
			}
			return
		}
	}
	item.operationIDs[operation.ID] = struct{}{}
	item.Operations = append(item.Operations, operation)
}

func (item *AvailableModelCapability) addLegacyChannel(endpoint model.Endpoint, channel model.Channel, operationID string) {
	identity := strings.Join([]string{channel.Type, endpoint.VendorModel, string(endpoint.InteractionMode), operationID}, "::")
	if _, exists := item.channelIDs[identity]; exists {
		return
	}
	item.channelIDs[identity] = struct{}{}
	entry := gin.H{
		"channel_id":       channel.ID,
		"channel_type":     channel.Type,
		"channel_name":     channel.Name,
		"model":            endpoint.VendorModel,
		"price":            endpoint.InputPrice,
		"interaction_mode": endpoint.InteractionMode,
		"route_operation":  operationID,
	}
	if len(endpoint.ParamSchema) > 0 {
		entry["param_schema"] = ensureUTF8(endpoint.ParamSchema)
	}
	item.Channels = append(item.Channels, entry)
}

func endpointHasAvailableAccount(endpoint model.Endpoint, accountIDs []uint, accounts map[uint]model.ChannelAccount) bool {
	for _, accountID := range accountIDs {
		account, ok := accounts[accountID]
		if ok && account.ChannelID == endpoint.ChannelID {
			return true
		}
	}
	return false
}

func publicOperationPath(operationID string) string {
	switch operationID {
	case "chat.completions":
		return "/v1/chat/completions"
	case "responses.create":
		return "/v1/responses"
	case "messages.create":
		return "/v1/messages"
	case RouteOperationImagesGenerate:
		return "/v1/images/generations"
	case RouteOperationImagesEdit:
		return "/v1/images/edits"
	case RouteOperationVideosGenerate:
		return "/v1/videos/generations"
	default:
		return ""
	}
}

func operationRank(operationID string) int {
	order := map[string]int{
		"chat.completions":           10,
		"responses.create":           20,
		"messages.create":            30,
		RouteOperationImagesGenerate: 100,
		RouteOperationImagesEdit:     110,
		RouteOperationVideosGenerate: 200,
	}
	if rank, ok := order[operationID]; ok {
		return rank
	}
	return 1000
}

func (s *QueryService) ListCapabilityChannels() ([]gin.H, error) {
	var models []model.Model
	if err := model.DB().Where("status = ?", 1).Find(&models).Error; err != nil {
		return nil, err
	}

	var channels []model.Channel
	if err := model.DB().Where("status = ?", 1).Find(&channels).Error; err != nil {
		return nil, err
	}
	channelMap := make(map[uint]model.Channel)
	for _, ch := range channels {
		channelMap[ch.ID] = ch
	}

	var endpoints []model.Endpoint
	if err := model.DB().Where("status = ?", 1).Find(&endpoints).Error; err != nil {
		return nil, err
	}

	modelChannels := make(map[string][]gin.H)
	for _, ep := range endpoints {
		ch, ok := channelMap[ep.ChannelID]
		if !ok {
			continue
		}
		modelChannels[ep.ModelCode] = append(modelChannels[ep.ModelCode], gin.H{
			"channel_id":   ch.ID,
			"channel_type": ch.Type,
			"channel_name": ch.Name,
			"model":        ep.VendorModel,
			"price":        ep.InputPrice,
		})
	}

	result := make([]gin.H, 0, len(models))
	for _, m := range models {
		chs := modelChannels[m.Code]
		if len(chs) == 0 {
			chs = []gin.H{}
		}
		result = append(result, gin.H{
			"code":        m.Code,
			"name":        m.Name,
			"type":        m.Type,
			"description": m.Description,
			"channels":    chs,
		})
	}

	return result, nil
}

// ---------- PricingService ----------

type PricingService struct{}

func NewPricingService() *PricingService {
	return &PricingService{}
}

type PricingCapability struct {
	Code        string                `json:"code"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Channels    []PricingChannelModel `json:"channels"`
}

type PricingChannelModel struct {
	ChannelCode string          `json:"channel_code"`
	Model       string          `json:"model"`
	Name        string          `json:"name"`
	Price       decimal.Decimal `json:"price"`
	PriceUnit   string          `json:"price_unit"`
}

func (s *PricingService) GetPricing() ([]PricingCapability, error) {
	var models []model.Model
	if err := model.DB().Where("status = ?", 1).Order("code ASC").Find(&models).Error; err != nil {
		return nil, err
	}

	var endpoints []model.Endpoint
	if err := model.DB().
		Where("status = ?", 1).
		Preload("Channel").
		Find(&endpoints).Error; err != nil {
		return nil, err
	}

	epMap := make(map[string][]PricingChannelModel)
	for _, ep := range endpoints {
		if ep.Channel == nil || ep.Channel.Status != 1 {
			continue
		}
		epMap[ep.ModelCode] = append(epMap[ep.ModelCode], PricingChannelModel{
			ChannelCode: ep.Channel.Type,
			Model:       ep.VendorModel,
			Name:        ep.VendorModel,
			Price:       ep.InputPrice,
			PriceUnit:   string(ep.PriceMode),
		})
	}

	result := make([]PricingCapability, 0, len(models))
	for _, m := range models {
		channels := epMap[m.Code]
		if channels == nil {
			channels = []PricingChannelModel{}
		}
		result = append(result, PricingCapability{
			Code:        m.Code,
			Name:        m.Name,
			Description: m.Description,
			Channels:    channels,
		})
	}

	return result, nil
}

func ensureUTF8(data []byte) json.RawMessage {
	if len(data) == 0 {
		return nil
	}
	if utf8.Valid(data) {
		return json.RawMessage(data)
	}
	decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(data)
	if err != nil {
		return json.RawMessage(data)
	}
	return json.RawMessage(decoded)
}
