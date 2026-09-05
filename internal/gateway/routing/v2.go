package routing

import (
	"context"
	"encoding/json"
	"math/rand"
	"sort"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ExecutionMode string

const (
	ExecutionModeChat               ExecutionMode = "chat"
	ExecutionModeResponsesNative    ExecutionMode = "responses_native"
	ExecutionModeResponsesConverted ExecutionMode = "responses_converted"
)

// RouteOptions describes the endpoint dialects a request may use. Preferred
// transports win within the same route priority; allowed transports remain
// available as fallbacks.
type RouteOptions struct {
	AllowedTransports   []model.UpstreamTransport
	PreferredTransports []model.UpstreamTransport
	ExcludeChannels     []uint
	ExcludeKeys         []uint
	ExcludeAttempts     []TransportAttempt
	ResponsesRequest    bool
}

// TransportAttempt identifies one concrete key and endpoint dialect. Retrying
// another transport on the same key remains possible.
type TransportAttempt struct {
	KeyID     uint
	Transport model.UpstreamTransport
}

type transportCandidate struct {
	AbilityID       uint
	KeyID           uint
	ChannelID       uint
	VendorModel     string
	Priority        int
	Weight          int
	MaxConc         int
	CurrentConc     int
	PriceMode       string
	InputPrice      decimal.Decimal
	OutputPrice     decimal.Decimal
	APIKey          string
	Protocol        string
	BaseURL         string
	ExtraHeaders    []byte
	ChannelCfg      []byte
	Capabilities    []byte
	Transport       model.UpstreamTransport
	TransportConfig []byte
}

// SelectTransport selects only explicitly configured ability transports.
// Legacy abilities without gw_ability_transports rows are deliberately not
// inferred here; their data migration must create the safe transport rows.
func (r *Router) SelectTransport(modelName string, requirements RouteRequirements, options RouteOptions) (*RouteResult, error) {
	if r.unifiedActive(context.Background()) {
		return r.selectUnified(modelName, requirements, options)
	}
	if r.unified.configured(context.Background()) {
		// Once target catalog data exists, legacy routing is no longer a safe
		// fallback. An unpublished or unready target must fail closed.
		return nil, ErrNoRoute
	}
	var chosen *transportCandidate
	err := model.DB().Transaction(func(tx *gorm.DB) error {
		// 先区分“模型不存在”和“模型存在但当前不可用”，两者会映射成不同的公开 API 错误。
		var declarations []struct{ Status int8 }
		if err := tx.Table("gw_abilities").Select("status").Where("model_name = ?", modelName).Find(&declarations).Error; err != nil {
			return err
		}
		if len(declarations) == 0 {
			return ErrModelNotFound
		}

		// 候选行在选取期间加锁，并在同一事务中增加 key 并发数，防止多个实例同时超额选择。
		q := tx.Table("gw_abilities ab").
			Select("ab.id AS ability_id, ab.key_id, ab.channel_id, ab.vendor_model, ab.priority, "+
				"ab.price_mode, ab.input_price, ab.output_price, ck.weight, ck.max_conc, ck.current_conc, ck.api_key, "+
				"gc.protocol, gc.base_url, gc.extra_headers, gc.config AS channel_cfg, ab.capabilities, "+
				"at.transport, at.config AS transport_config").
			Joins("JOIN gw_ability_transports at ON at.ability_id = ab.id AND at.status = 1").
			Joins("JOIN gw_channel_keys ck ON ck.id = ab.key_id AND ck.status = 1 AND ck.deleted_at IS NULL").
			Joins("JOIN gw_channels gc ON gc.id = ab.channel_id AND gc.status = 1 AND gc.deleted_at IS NULL").
			Where("ab.model_name = ? AND ab.status = 1", modelName).
			Clauses(clause.Locking{Strength: "UPDATE"})
		if len(options.ExcludeChannels) > 0 {
			q = q.Where("ab.channel_id NOT IN ?", options.ExcludeChannels)
		}
		if len(options.ExcludeKeys) > 0 {
			q = q.Where("ab.key_id NOT IN ?", options.ExcludeKeys)
		}

		var all []transportCandidate
		if err := q.Find(&all).Error; err != nil {
			return err
		}
		if len(all) == 0 {
			return ErrNoRoute
		}

		// 熔断状态和本次请求已失败的尝试都以 key + transport 为粒度。
		var states []model.GwRouteState
		if err := tx.Where("model_name = ? AND disabled_until > ?", modelName, time.Now()).Find(&states).Error; err != nil {
			return err
		}
		disabled := make(map[routeStateKey]bool, len(states))
		for _, state := range states {
			disabled[routeStateKey{keyID: state.KeyID, transport: state.Transport}] = true
		}
		excludedAttempts := make(map[routeStateKey]bool, len(options.ExcludeAttempts))
		for _, attempt := range options.ExcludeAttempts {
			excludedAttempts[routeStateKey{keyID: attempt.KeyID, transport: attempt.Transport}] = true
		}

		// 分阶段记录匹配结果，便于准确区分能力不支持、协议不兼容和暂时无可用路由。
		filtered := make([]transportCandidate, 0, len(all))
		capabilityMatch := false
		transportMatch := false
		for _, candidate := range all {
			if !supportsSemanticRequirements(semanticCapabilities(candidate.Capabilities), requirements) {
				continue
			}
			capabilityMatch = true
			if !transportAllowed(candidate.Transport, options.AllowedTransports) {
				continue
			}
			transportMatch = true
			attempt := routeStateKey{keyID: candidate.KeyID, transport: candidate.Transport}
			if (candidate.MaxConc > 0 && candidate.CurrentConc >= candidate.MaxConc) || disabled[attempt] || excludedAttempts[attempt] {
				continue
			}
			filtered = append(filtered, candidate)
		}
		if !capabilityMatch {
			return ErrCapabilityUnavailable
		}
		if !transportMatch {
			return ErrNoCompatibleTransport
		}
		if len(filtered) == 0 {
			return ErrNoRoute
		}

		// 先按协议偏好和 Ability 优先级分层；权重只在最高层内部生效，不能越级抢占。
		sort.SliceStable(filtered, func(i, j int) bool {
			leftRank := transportRank(filtered[i].Transport, options.PreferredTransports)
			rightRank := transportRank(filtered[j].Transport, options.PreferredTransports)
			if leftRank != rightRank {
				return leftRank < rightRank
			}
			if filtered[i].Priority != filtered[j].Priority {
				return filtered[i].Priority > filtered[j].Priority
			}
			if filtered[i].AbilityID != filtered[j].AbilityID {
				return filtered[i].AbilityID < filtered[j].AbilityID
			}
			return filtered[i].Transport < filtered[j].Transport
		})

		topRank := transportRank(filtered[0].Transport, options.PreferredTransports)
		topPriority := filtered[0].Priority
		top := make([]transportCandidate, 0, len(filtered))
		for _, candidate := range filtered {
			if candidate.Priority != topPriority || transportRank(candidate.Transport, options.PreferredTransports) != topRank {
				break
			}
			top = append(top, candidate)
		}
		selected := weightedTransportCandidate(top)
		chosen = &selected
		// 并发占用必须与选择结果一并提交，调用结束后由 Engine 的 Release 对称释放。
		return tx.Model(&model.GwChannelKey{}).Where("id = ?", selected.KeyID).
			UpdateColumn("current_conc", gorm.Expr("current_conc + 1")).Error
	})
	if err != nil || chosen == nil {
		if err == nil {
			err = ErrNoRoute
		}
		return nil, err
	}

	protocol := model.Protocol(chosen.Protocol)
	if protocol == "" {
		protocol = model.ProtocolOpenAI
	}
	vendorModel := chosen.VendorModel
	if vendorModel == "" {
		vendorModel = modelName
	}
	priceMode := chosen.PriceMode
	if priceMode == "" {
		priceMode = "token"
	}
	return &RouteResult{
		AbilityID:       chosen.AbilityID,
		KeyID:           chosen.KeyID,
		ChannelID:       chosen.ChannelID,
		Protocol:        protocol,
		BaseURL:         chosen.BaseURL,
		APIKey:          chosen.APIKey,
		VendorModel:     vendorModel,
		ModelName:       modelName,
		ExtraHeaders:    parseStrMap(chosen.ExtraHeaders),
		ChannelConfig:   parseAnyMap(chosen.ChannelCfg),
		Capabilities:    semanticCapabilities(chosen.Capabilities),
		Transport:       chosen.Transport,
		TransportConfig: parseAnyMap(chosen.TransportConfig),
		ExecutionMode:   executionMode(options.ResponsesRequest, chosen.Transport),
		PriceMode:       priceMode,
		InputPrice:      chosen.InputPrice,
		OutputPrice:     chosen.OutputPrice,
	}, nil
}

func semanticCapabilities(raw []byte) map[Capability]bool {
	result := make(map[Capability]bool)
	if len(raw) == 0 {
		return result
	}
	var object map[string]bool
	if json.Unmarshal(raw, &object) == nil && object != nil {
		for name, enabled := range object {
			if enabled {
				result[Capability(name)] = true
			}
		}
		return result
	}
	var declared []string
	if json.Unmarshal(raw, &declared) == nil {
		for _, name := range declared {
			result[Capability(name)] = true
		}
	}
	return result
}

func supportsSemanticRequirements(capabilities map[Capability]bool, requirements RouteRequirements) bool {
	for capability, required := range requirements {
		if required && !capabilities[capability] {
			return false
		}
	}
	return true
}

type routeStateKey struct {
	keyID     uint
	transport model.UpstreamTransport
}

func transportAllowed(transport model.UpstreamTransport, allowed []model.UpstreamTransport) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, value := range allowed {
		if value == transport {
			return true
		}
	}
	return false
}

func transportRank(transport model.UpstreamTransport, preferred []model.UpstreamTransport) int {
	for index, value := range preferred {
		if value == transport {
			return index
		}
	}
	return len(preferred)
}

func weightedTransportCandidate(candidates []transportCandidate) transportCandidate {
	// 非正权重按 1 处理，使配置错误不会让候选永远无法被选中。
	totalWeight := 0
	for _, candidate := range candidates {
		weight := candidate.Weight
		if weight <= 0 {
			weight = 1
		}
		totalWeight += weight
	}
	pick := rand.Intn(totalWeight)
	current := 0
	for _, candidate := range candidates {
		weight := candidate.Weight
		if weight <= 0 {
			weight = 1
		}
		current += weight
		if pick < current {
			return candidate
		}
	}
	return candidates[len(candidates)-1]
}

func executionMode(responsesRequest bool, transport model.UpstreamTransport) ExecutionMode {
	if !responsesRequest {
		return ExecutionModeChat
	}
	if transport == model.UpstreamTransportOpenAIResponses || transport == model.UpstreamTransportVolcengineV3 {
		return ExecutionModeResponsesNative
	}
	return ExecutionModeResponsesConverted
}
