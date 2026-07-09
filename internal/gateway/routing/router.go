package routing

import (
	"encoding/json"
	"math/rand"
	"sort"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Router 基于 gw_* 表选路。
type Router struct{}

func NewRouter() *Router { return &Router{} }

// ErrNoRoute 无可用候选。
var ErrNoRoute = gorm.ErrRecordNotFound

// candidate 选路查询的一行(内部)。
type candidate struct {
	AbilityID   uint
	KeyID       uint
	ChannelID   uint
	VendorModel string
	Priority    int
	Weight      int
	PriceMode   string
	InputPrice  decimal.Decimal
	OutputPrice decimal.Decimal
	APIKey      string
	Protocol    string
	BaseURL     string
	ExtraHeaders []byte
	ChannelCfg   []byte
}

// Select 选出一个可用路由目标,并原子占用其并发(current_conc+1)。
// excludeChannels/excludeKeys 为本轮重试已试过的,需排除。
// 返回的 RouteResult 需在请求结束后由调用方 Release 释放并发。
func (r *Router) Select(modelName string, excludeChannels, excludeKeys []uint) (*RouteResult, error) {
	var chosen *candidate
	err := model.DB().Transaction(func(tx *gorm.DB) error {
		q := tx.Table("gw_abilities ab").
			Select("ab.id AS ability_id, ab.key_id, ab.channel_id, ab.vendor_model, ab.priority, " +
				"ab.price_mode, ab.input_price, ab.output_price, ck.weight, ck.api_key, " +
				"gc.protocol, gc.base_url, gc.extra_headers, gc.config AS channel_cfg").
			Joins("JOIN gw_channel_keys ck ON ck.id = ab.key_id AND ck.status = 1 AND ck.deleted_at IS NULL AND (ck.max_conc = 0 OR ck.current_conc < ck.max_conc)").
			Joins("JOIN gw_channels gc ON gc.id = ab.channel_id AND gc.status = 1 AND gc.deleted_at IS NULL").
			Where("ab.model_name = ? AND ab.status = 1", modelName).
			Where("ab.key_id NOT IN (SELECT account_id FROM account_model_states WHERE model_code = ? AND disabled_until > ?)", modelName, time.Now()).
			Clauses(clause.Locking{Strength: "UPDATE"}) // 普通 FOR UPDATE(5.7 兼容,勿加 Table 别名)
		if len(excludeChannels) > 0 {
			q = q.Where("ab.channel_id NOT IN ?", excludeChannels)
		}
		if len(excludeKeys) > 0 {
			q = q.Where("ab.key_id NOT IN ?", excludeKeys)
		}

		var cands []candidate
		if err := q.Find(&cands).Error; err != nil {
			return err
		}
		if len(cands) == 0 {
			return ErrNoRoute
		}

		// priority DESC, ability_id 稳定
		sort.SliceStable(cands, func(i, j int) bool {
			if cands[i].Priority != cands[j].Priority {
				return cands[i].Priority > cands[j].Priority
			}
			return cands[i].AbilityID < cands[j].AbilityID
		})

		// 最高优先级档内按 key weight 加权随机
		topPri := cands[0].Priority
		var top []candidate
		for _, c := range cands {
			if c.Priority == topPri {
				top = append(top, c)
			}
		}
		sel := &top[0]
		totalW := 0
		for i := range top {
			w := top[i].Weight
			if w <= 0 {
				w = 1
			}
			totalW += w
		}
		rnd := rand.Intn(totalW)
		cum := 0
		for i := range top {
			w := top[i].Weight
			if w <= 0 {
				w = 1
			}
			cum += w
			if rnd < cum {
				sel = &top[i]
				break
			}
		}
		c := *sel
		chosen = &c

		// 原子占用并发
		return tx.Model(&model.GwChannelKey{}).Where("id = ?", c.KeyID).
			UpdateColumn("current_conc", gorm.Expr("current_conc + 1")).Error
	})
	if err != nil || chosen == nil {
		if err == nil {
			err = ErrNoRoute
		}
		return nil, err
	}

	proto := model.Protocol(chosen.Protocol)
	if proto == "" {
		proto = model.ProtocolOpenAI
	}
	vendor := chosen.VendorModel
	if vendor == "" {
		vendor = modelName
	}
	priceMode := chosen.PriceMode
	if priceMode == "" {
		priceMode = "token"
	}

	res := &RouteResult{
		AbilityID:     chosen.AbilityID,
		KeyID:         chosen.KeyID,
		ChannelID:     chosen.ChannelID,
		Protocol:      proto,
		BaseURL:       chosen.BaseURL,
		APIKey:        chosen.APIKey,
		VendorModel:   vendor,
		ModelName:     modelName,
		ExtraHeaders:  parseStrMap(chosen.ExtraHeaders),
		ChannelConfig: parseAnyMap(chosen.ChannelCfg),
		PriceMode:     priceMode,
		InputPrice:    chosen.InputPrice,
		OutputPrice:   chosen.OutputPrice,
	}
	return res, nil
}

// Release 释放某 key 的并发占用(current_conc-1,不低于0)。请求结束时调用。
func (r *Router) Release(keyID uint) {
	if keyID == 0 {
		return
	}
	model.DB().Model(&model.GwChannelKey{}).
		Where("id = ? AND current_conc > 0", keyID).
		UpdateColumn("current_conc", gorm.Expr("current_conc - 1"))
}

func parseStrMap(raw []byte) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]string
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

func parseAnyMap(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}
