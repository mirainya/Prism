package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/tidwall/gjson"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GatewayAdminService 网关 v2 路由表(gw_*)的后台管理。
// gw_abilities 是「某 key 能跑某 model」的唯一记录(路由索引,无软删);
// 增删渠道/key 时同步重建关联 abilities。gw_model_meta 是元数据面,永不参与路由。
type GatewayAdminService struct{}

func NewGatewayAdminService() *GatewayAdminService {
	return &GatewayAdminService{}
}

var (
	ErrGwChannelNotFound = errors.New("gateway channel not found")
	ErrGwKeyNotFound     = errors.New("gateway channel key not found")
	ErrGwNoBaseURL       = errors.New("channel base_url is empty")
	ErrGwNoKey           = errors.New("channel key api_key is empty")
)

// ---------- 渠道 GwChannel ----------

// ListChannels 列出所有渠道(按 sort, id)。
func (s *GatewayAdminService) ListChannels() ([]model.GwChannel, error) {
	rows := make([]model.GwChannel, 0)
	err := model.DB().Order("sort, id").Find(&rows).Error
	return rows, err
}

// GetChannel 取单个渠道。
func (s *GatewayAdminService) GetChannel(id uint) (*model.GwChannel, error) {
	var ch model.GwChannel
	if err := model.DB().First(&ch, id).Error; err != nil {
		return nil, ErrGwChannelNotFound
	}
	return &ch, nil
}

// CreateChannel 新建渠道。
func (s *GatewayAdminService) CreateChannel(ch *model.GwChannel) error {
	ch.BaseURL = strings.TrimRight(strings.TrimSpace(ch.BaseURL), "/")
	if ch.Protocol == "" {
		ch.Protocol = model.ProtocolOpenAI
	}
	return model.DB().Create(ch).Error
}

// UpdateChannel 更新渠道可编辑字段(name/protocol/base_url/extra_headers/config/status/sort)。
func (s *GatewayAdminService) UpdateChannel(id uint, updates map[string]any) error {
	if v, ok := updates["base_url"].(string); ok {
		updates["base_url"] = strings.TrimRight(strings.TrimSpace(v), "/")
	}
	allowed := map[string]struct{}{
		"name": {}, "protocol": {}, "base_url": {},
		"extra_headers": {}, "config": {}, "status": {}, "sort": {},
	}
	clean := filterAllowed(updates, allowed)
	if len(clean) == 0 {
		return nil
	}
	return model.DB().Model(&model.GwChannel{}).Where("id = ?", id).Updates(clean).Error
}

// DeleteChannel 删渠道 = 软删渠道 + 软删其 keys + 硬删其 abilities(索引无软删)。
func (s *GatewayAdminService) DeleteChannel(id uint) error {
	return model.DB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("channel_id = ?", id).Delete(&model.GwAbility{}).Error; err != nil {
			return err
		}
		if err := tx.Where("channel_id = ?", id).Delete(&model.GwChannelKey{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.GwChannel{}, id).Error
	})
}

// ReorderChannels 批量更新渠道 sort(按传入顺序 0,1,2...)。
func (s *GatewayAdminService) ReorderChannels(ids []uint) error {
	return model.DB().Transaction(func(tx *gorm.DB) error {
		for i, id := range ids {
			if err := tx.Model(&model.GwChannel{}).Where("id = ?", id).
				Update("sort", i).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ---------- 渠道 key GwChannelKey ----------

// ListKeys 列出某渠道下的 keys。
func (s *GatewayAdminService) ListKeys(channelID uint) ([]model.GwChannelKey, error) {
	rows := make([]model.GwChannelKey, 0)
	err := model.DB().Where("channel_id = ?", channelID).Order("id").Find(&rows).Error
	return rows, err
}

// CreateKey 新建 key(校验渠道存在)。
func (s *GatewayAdminService) CreateKey(key *model.GwChannelKey) error {
	var cnt int64
	model.DB().Model(&model.GwChannel{}).Where("id = ?", key.ChannelID).Count(&cnt)
	if cnt == 0 {
		return ErrGwChannelNotFound
	}
	if strings.TrimSpace(key.APIKey) == "" {
		return ErrGwNoKey
	}
	return model.DB().Create(key).Error
}

// UpdateKey 更新 key 可编辑字段(name/api_key/weight/status/max_conc)。
func (s *GatewayAdminService) UpdateKey(id uint, updates map[string]any) error {
	allowed := map[string]struct{}{
		"name": {}, "api_key": {}, "weight": {}, "status": {}, "max_conc": {},
	}
	clean := filterAllowed(updates, allowed)
	if len(clean) == 0 {
		return nil
	}
	return model.DB().Model(&model.GwChannelKey{}).Where("id = ?", id).Updates(clean).Error
}

// DeleteKey 删 key = 软删 key + 硬删其 abilities。
func (s *GatewayAdminService) DeleteKey(id uint) error {
	return model.DB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("key_id = ?", id).Delete(&model.GwAbility{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.GwChannelKey{}, id).Error
	})
}

// filterAllowed 只保留白名单内的更新字段。
func filterAllowed(updates map[string]any, allowed map[string]struct{}) map[string]any {
	clean := make(map[string]any)
	for k, v := range updates {
		if _, ok := allowed[k]; ok {
			clean[k] = v
		}
	}
	return clean
}

// ---------- 能力 GwAbility(路由索引) ----------

// GwAbilityRow ability 行 + 渠道/key 展示信息。
type GwAbilityRow struct {
	model.GwAbility
	ChannelName string `json:"channel_name"`
	Protocol    string `json:"protocol"`
	KeyName     string `json:"key_name"`
}

// AbilityFilter abilities 列表过滤条件(零值表示不过滤)。
type AbilityFilter struct {
	ModelName string
	ChannelID uint
	KeyID     uint
}

// ListAbilities 列出 abilities(可按 model/channel/key 过滤),带渠道协议与 key 名。
func (s *GatewayAdminService) ListAbilities(f AbilityFilter) ([]GwAbilityRow, error) {
	rows := make([]GwAbilityRow, 0)
	q := model.DB().Table("gw_abilities ab").
		Select("ab.*, c.name AS channel_name, c.protocol, k.name AS key_name").
		Joins("LEFT JOIN gw_channels c ON c.id = ab.channel_id").
		Joins("LEFT JOIN gw_channel_keys k ON k.id = ab.key_id")
	if f.ModelName != "" {
		q = q.Where("ab.model_name = ?", f.ModelName)
	}
	if f.ChannelID != 0 {
		q = q.Where("ab.channel_id = ?", f.ChannelID)
	}
	if f.KeyID != 0 {
		q = q.Where("ab.key_id = ?", f.KeyID)
	}
	err := q.Order("ab.model_name, ab.priority DESC, ab.id").Scan(&rows).Error
	return rows, err
}

// UpdateAbility 更新 ability 可编辑字段(vendor/优先级/价格/状态)。
func (s *GatewayAdminService) UpdateAbility(id uint, updates map[string]any) error {
	allowed := map[string]struct{}{
		"vendor_model": {}, "priority": {}, "status": {},
		"price_mode": {}, "input_price": {}, "output_price": {},
	}
	clean := filterAllowed(updates, allowed)
	if len(clean) == 0 {
		return nil
	}
	return model.DB().Model(&model.GwAbility{}).Where("id = ?", id).Updates(clean).Error
}

// DeleteAbility 硬删一条 ability(仅删路由索引)。
func (s *GatewayAdminService) DeleteAbility(id uint) error {
	return model.DB().Delete(&model.GwAbility{}, id).Error
}

// ---------- 元数据 GwModelMeta ----------

// ListModelMeta 列出所有模型元数据(按 sort)。
func (s *GatewayAdminService) ListModelMeta() ([]model.GwModelMeta, error) {
	rows := make([]model.GwModelMeta, 0)
	err := model.DB().Order("sort, model_name").Find(&rows).Error
	return rows, err
}

// UpsertModelMeta 新建或更新模型元数据(主键 model_name)。
func (s *GatewayAdminService) UpsertModelMeta(m *model.GwModelMeta) error {
	m.ModelName = strings.TrimSpace(m.ModelName)
	if m.ModelName == "" {
		return errors.New("model_name is empty")
	}
	return model.DB().Save(m).Error
}

// DeleteModelMeta 删除模型元数据(不影响路由)。
func (s *GatewayAdminService) DeleteModelMeta(modelName string) error {
	return model.DB().Delete(&model.GwModelMeta{}, "model_name = ?", modelName).Error
}

// GwModelRow 对话模型页一行:可路由模型(来自 gw_abilities) + 元数据 + 可用性统计。
type GwModelRow struct {
	ModelName      string         `json:"model_name"`
	DisplayName    string         `json:"display_name"`
	ThinkingConfig datatypes.JSON `json:"thinking_config"`
	MaxTokens      int            `json:"max_tokens"`
	Features       datatypes.JSON `json:"features"`
	GroupName      string         `json:"group_name"`     // 手动分组名(gw_model_meta.group_name)
	SourceChannel  string         `json:"source_channel"` // 最高优先级 ability 所属渠道名(前端分组兜底)
	MetaStatus     *int8          `json:"meta_status"`    // 元数据 status,无 meta 则 nil
	Sort           int            `json:"sort"`
	KeyTotal       int            `json:"key_total"`     // 有该模型 ability 的 key 数
	KeyAvailable   int            `json:"key_available"` // 其中启用且渠道/key 启用的数
}

// ListModels 列出所有「可路由 chat 模型」:distinct gw_abilities.model_name
// LEFT JOIN gw_model_meta,并统计可用 key 数(ability+key+channel 三者启用)。
// 与 /v2 路由同源,只展示至少有一条 ability 的模型。
func (s *GatewayAdminService) ListModels() ([]GwModelRow, error) {
	rows := make([]GwModelRow, 0)
	err := model.DB().Table("gw_abilities ab").
		Select(`ab.model_name,
			COALESCE(m.display_name, ab.model_name) AS display_name,
			m.thinking_config, COALESCE(m.max_tokens, 0) AS max_tokens, m.features,
			COALESCE(m.group_name, '') AS group_name,
			(SELECT c2.name FROM gw_abilities a2
				JOIN gw_channels c2 ON c2.id = a2.channel_id AND c2.deleted_at IS NULL
				WHERE a2.model_name = ab.model_name
				ORDER BY a2.priority DESC, a2.id LIMIT 1) AS source_channel,
			m.status AS meta_status, COALESCE(m.sort, 0) AS sort,
			COUNT(DISTINCT ab.key_id) AS key_total,
			COUNT(DISTINCT CASE WHEN ab.status=1 AND k.status=1 AND c.status=1 THEN ab.key_id END) AS key_available`).
		Joins("LEFT JOIN gw_channel_keys k ON k.id = ab.key_id AND k.deleted_at IS NULL").
		Joins("LEFT JOIN gw_channels c ON c.id = ab.channel_id AND c.deleted_at IS NULL").
		Joins("LEFT JOIN gw_model_meta m ON m.model_name = ab.model_name").
		Group("ab.model_name, m.display_name, m.thinking_config, m.max_tokens, m.features, m.group_name, m.status, m.sort").
		Order("sort, ab.model_name").
		Scan(&rows).Error
	return rows, err
}

// ReorderModels 按传入 model_name 顺序把 gw_model_meta.sort 设为下标(升序,对齐 ListModels 的 Order sort)。
// 只改 sort 不整行覆盖:缺 meta 的模型建行只填 name+sort(display_name 空 → 前端 fallback model_name)。
func (s *GatewayAdminService) ReorderModels(names []string) error {
	return model.DB().Transaction(func(tx *gorm.DB) error {
		for i, name := range names {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "model_name"}},
				DoUpdates: clause.AssignmentColumns([]string{"sort"}),
			}).Select("model_name", "sort", "status").Create(&model.GwModelMeta{ModelName: name, Sort: i, Status: 1}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ListPlaygroundModels 在线试用的可用模型列表,与 /v2 路由同源(gw_abilities+gw_model_meta)。
// 只列 key_available>0 的可路由模型,复用 ModelInfo 结构使前端零改动。
// chat 全走合成流式,故 supports_stream/default_stream 恒 true(与老无端点分支一致)。
func (s *GatewayAdminService) ListPlaygroundModels() ([]ModelInfo, error) {
	rows, err := s.ListModels()
	if err != nil {
		return nil, err
	}
	result := make([]ModelInfo, 0, len(rows))
	for _, m := range rows {
		if m.KeyAvailable <= 0 {
			continue
		}
		mi := ModelInfo{
			ID:             m.ModelName,
			Object:         "model",
			OwnedBy:        "prism",
			SupportsStream: true,
			DefaultStream:  true,
		}
		// 分组:手动组名优先,否则源渠道,兜底「未分组」(与对话模型页同频)
		if g := strings.TrimSpace(m.GroupName); g != "" {
			mi.Group = g
		} else if sc := strings.TrimSpace(m.SourceChannel); sc != "" {
			mi.Group = sc
		} else {
			mi.Group = "未分组"
		}
		// 能力标签
		if len(m.Features) > 0 {
			var features []string
			if json.Unmarshal(m.Features, &features) == nil {
				for _, f := range features {
					switch f {
					case "tools":
						mi.SupportsTools = true
					case "vision":
						mi.SupportsMultimodal = true
					}
				}
			}
		}
		// 思考档位
		if cfg := parseThinkingConfig(m.ThinkingConfig); cfg != nil {
			ti := &ThinkingInfo{Default: cfg.Default, Locked: cfg.Locked}
			for _, o := range cfg.Options {
				ti.Options = append(ti.Options, ThinkingLevelInfo{Label: o.Label, Value: o.Value})
			}
			mi.Thinking = ti
		}
		result = append(result, mi)
	}
	return result, nil
}

// ---------- 拉取 / 导入(以 key 为单位,写 gw_abilities) ----------
// GwUpstreamModelItem 上游返回的单个模型 + 该 key 是否已导入。
type GwUpstreamModelItem struct {
	ID       string `json:"id"`       // 上游模型 id(= 建议的 model_name/vendor_model)
	Imported bool   `json:"imported"` // 该 key 是否已有对应 ability
}

// DiscoverKeyModels 用某 key 调上游 /v1/models,与该 key 已有 abilities 做 diff。
func (s *GatewayAdminService) DiscoverKeyModels(ctx context.Context, keyID uint) ([]GwUpstreamModelItem, error) {
	var key model.GwChannelKey
	if err := model.DB().First(&key, keyID).Error; err != nil {
		return nil, ErrGwKeyNotFound
	}
	if strings.TrimSpace(key.APIKey) == "" {
		return nil, ErrGwNoKey
	}
	var ch model.GwChannel
	if err := model.DB().First(&ch, key.ChannelID).Error; err != nil {
		return nil, ErrGwChannelNotFound
	}
	baseURL := strings.TrimRight(strings.TrimSpace(ch.BaseURL), "/")
	if baseURL == "" {
		return nil, ErrGwNoBaseURL
	}

	ids, err := s.fetchGwUpstreamModelIDs(ctx, baseURL, key.APIKey, ch.Protocol)
	if err != nil {
		return nil, err
	}

	// 该 key 已有 abilities 的 model_name 集合
	imported := make(map[string]struct{})
	var names []string
	model.DB().Model(&model.GwAbility{}).Where("key_id = ?", keyID).Pluck("model_name", &names)
	for _, n := range names {
		imported[n] = struct{}{}
	}

	items := make([]GwUpstreamModelItem, 0, len(ids))
	for _, id := range ids {
		_, ok := imported[id]
		items = append(items, GwUpstreamModelItem{ID: id, Imported: ok})
	}
	return items, nil
}

// fetchGwUpstreamModelIDs 调上游 /v1/models,鉴权头按协议区分(anthropic 用 x-api-key,其余 Bearer)。
func (s *GatewayAdminService) fetchGwUpstreamModelIDs(ctx context.Context, baseURL, apiKey string, protocol model.Protocol) ([]string, error) {
	reqURL := baseURL + "/v1/models"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	if protocol == model.ProtocolAnthropic {
		httpReq.Header.Set("x-api-key", apiKey)
		httpReq.Header.Set("anthropic-version", "2023-06-01")
	} else {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := discoveryHTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("请求上游失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("上游返回 %d: %s", resp.StatusCode, truncate(string(body), 300))
	}

	root := gjson.ParseBytes(body)
	arr := root.Get("data")
	if !arr.Exists() {
		arr = root
	}
	if !arr.IsArray() {
		return nil, fmt.Errorf("上游响应格式异常: %s", truncate(string(body), 300))
	}
	seen := make(map[string]struct{})
	var ids []string
	for _, item := range arr.Array() {
		id := strings.TrimSpace(item.Get("id").String())
		if id == "" {
			id = strings.TrimSpace(item.String())
		}
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

// GwImportItem 导入的单个模型(写 gw_abilities + gw_model_meta)。
type GwImportItem struct {
	ModelName   string `json:"model_name"`   // = 上游 id,路由键
	VendorModel string `json:"vendor_model"` // 上游模型名,空则 = model_name
	DisplayName string `json:"display_name"` // 元数据显示名,空则 = model_name
}

// GwImportRequest 把选中模型导入某 key。
type GwImportRequest struct {
	KeyID  uint           `json:"key_id" binding:"required"`
	Models []GwImportItem `json:"models" binding:"required"`
}

// GwImportResult 导入结果。
type GwImportResult struct {
	AbilitiesAdded int `json:"abilities_added"`
	MetaAdded      int `json:"meta_added"`
}

// ImportKeyModels 把选中模型导入某 key:写 gw_abilities(缺失时,价0) + upsert gw_model_meta(仅补显示名)。
func (s *GatewayAdminService) ImportKeyModels(req *GwImportRequest) (*GwImportResult, error) {
	var key model.GwChannelKey
	if err := model.DB().First(&key, req.KeyID).Error; err != nil {
		return nil, ErrGwKeyNotFound
	}
	result := &GwImportResult{}

	for _, item := range req.Models {
		name := strings.TrimSpace(item.ModelName)
		if name == "" {
			continue
		}
		vendor := strings.TrimSpace(item.VendorModel)
		if vendor == "" {
			vendor = name
		}

		// 1. ability(该 key 缺失时建,价0)
		var cnt int64
		model.DB().Model(&model.GwAbility{}).
			Where("key_id = ? AND model_name = ?", req.KeyID, name).Count(&cnt)
		if cnt == 0 {
			ab := &model.GwAbility{
				ModelName:   name,
				ChannelID:   key.ChannelID,
				KeyID:       req.KeyID,
				VendorModel: vendor,
				Priority:    0,
				PriceMode:   "token",
				Status:      1,
			}
			if err := model.DB().Create(ab).Error; err == nil {
				result.AbilitiesAdded++
			}
		}

		// 2. model_meta(缺失时补显示名,不覆盖已有配置)
		var mcnt int64
		model.DB().Model(&model.GwModelMeta{}).Where("model_name = ?", name).Count(&mcnt)
		if mcnt == 0 {
			display := strings.TrimSpace(item.DisplayName)
			if display == "" {
				display = name
			}
			meta := &model.GwModelMeta{ModelName: name, DisplayName: display, Status: 1}
			if err := model.DB().Create(meta).Error; err == nil {
				result.MetaAdded++
			}
		}
	}
	return result, nil
}

// discoveryHTTPClient 拉取上游 /v1/models 的专用 client(轻量 GET,20s 足够)。
var discoveryHTTPClient = &http.Client{Timeout: 20 * time.Second}

// truncate 截断字符串到 n 字符(错误信息展示用)。
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}
