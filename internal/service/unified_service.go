package service

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UnifiedService 统一执行引擎(image/video 能力 + 账号选路)。chat 已切到 gateway pipeline。
type UnifiedService struct {
	billingService *BillingService
	circuitService *AccountCircuitService
}

func NewUnifiedService() *UnifiedService {
	return &UnifiedService{
		billingService: NewBillingService(),
		circuitService: NewAccountCircuitService(),
	}
}

// NewCapabilityService returns UnifiedService (backward compat alias)
func NewCapabilityService() *UnifiedService {
	return NewUnifiedService()
}

type ModelInfo struct {
	ID                     string        `json:"id"`
	Object                 string        `json:"object"`
	Created                int64         `json:"created"`
	OwnedBy                string        `json:"owned_by"`
	SupportsStream         bool          `json:"supports_stream"`
	DefaultStream          bool          `json:"default_stream"`
	SupportsTools          bool          `json:"supports_tools"`
	SupportsResponseFormat bool          `json:"supports_response_format"`
	SupportsMultimodal     bool          `json:"supports_multimodal"`
	Group                  string        `json:"group,omitempty"` // 分组名(手动组名/源渠道/未分组),与对话模型页同频
	Thinking               *ThinkingInfo `json:"thinking,omitempty"`
}

// ThinkingInfo 暴露给前端的思考档位信息(不含 body)
type ThinkingInfo struct {
	Default string              `json:"default"`
	Locked  bool                `json:"locked"`
	Options []ThinkingLevelInfo `json:"options"`
}

type ThinkingLevelInfo struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// selectAccount 在渠道内选择一个可用账号
// modelCode: 用于按账号 supported_models 白名单过滤 + 排除对该模型熔断中的账号
// excludeAccountIDs: 本轮重试已尝试过的账号,避免重复命中
// requireExplicit: true 时只选「白名单显式包含该 model」的账号(NULL/空白名单不参与)。
//
//	用于 chat 去端点化的虚拟端点路径,避免通配 key 被误选发往其不支持的模型
func (s *UnifiedService) selectAccount(channelID uint, modelCode string, excludeAccountIDs []uint, requireExplicit bool) (*model.ChannelAccount, error) {
	var accounts []model.ChannelAccount

	err := model.DB().Transaction(func(tx *gorm.DB) error {
		q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("channel_id = ? AND status = 1", channelID).
			Where("max_tasks = 0 OR current_tasks < max_tasks")

		// 白名单: supported_models 为空(NULL/[]/null)=支持所有; 非空则必须包含 modelCode
		if modelCode != "" {
			if requireExplicit {
				// 收紧: 必须显式列出该 model(NULL/空白名单不参与)
				q = q.Where("JSON_LENGTH(supported_models) > 0 AND JSON_CONTAINS(supported_models, ?)",
					fmt.Sprintf("%q", modelCode))
			} else {
				q = q.Where(
					"supported_models IS NULL OR JSON_LENGTH(supported_models) = 0 OR JSON_CONTAINS(supported_models, ?)",
					fmt.Sprintf("%q", modelCode),
				)
			}
			// 排除对该模型熔断中的账号
			q = q.Where(
				"id NOT IN (SELECT account_id FROM account_model_states WHERE model_code = ? AND disabled_until > ?)",
				modelCode, time.Now(),
			)
		}
		// 排除本轮已尝试的账号
		if len(excludeAccountIDs) > 0 {
			q = q.Where("id NOT IN ?", excludeAccountIDs)
		}

		if err := q.Find(&accounts).Error; err != nil {
			return err
		}
		if len(accounts) == 0 {
			return gorm.ErrRecordNotFound
		}

		// 加权随机选择
		selected := &accounts[0]
		totalWeight := 0
		for i := range accounts {
			w := accounts[i].Weight
			if w <= 0 {
				w = 1
			}
			totalWeight += w
		}
		r := rand.Intn(totalWeight)
		cumulative := 0
		for i := range accounts {
			w := accounts[i].Weight
			if w <= 0 {
				w = 1
			}
			cumulative += w
			if r < cumulative {
				selected = &accounts[i]
				break
			}
		}

		accounts[0] = *selected
		return tx.Model(selected).UpdateColumn("current_tasks", gorm.Expr("current_tasks + 1")).Error
	})

	if err != nil {
		return nil, err
	}
	return &accounts[0], nil
}

// selectAccountForEndpoint 为端点选账号:
//
//	端点绑了 key(AccountID!=0)→直取该 key(selectBoundAccount);否则回退渠道账号池(selectAccount)。
//	绑定端点只有 1 个 key,端点内重试自然只 1 次(exclude 后无候选),fallback 靠跨端点遍历。
func (s *UnifiedService) selectAccountForEndpoint(ep *model.Endpoint, excludeAccountIDs []uint) (*model.ChannelAccount, error) {
	if ep.AccountID != 0 {
		return s.selectBoundAccount(ep.AccountID, ep.ModelCode, excludeAccountIDs)
	}
	return s.selectAccount(ep.ChannelID, ep.ModelCode, excludeAccountIDs, false)
}

// selectBoundAccount 直取端点绑定的指定 key:
//
//	校验 status=1 + 不在 exclude + 该 model 未熔断 + 并发未满,原子 current_tasks+1。
//	与 selectAccount 同用行锁保证并发下计数准确,但不做加权随机(只此一个候选)。
func (s *UnifiedService) selectBoundAccount(accountID uint, modelCode string, excludeAccountIDs []uint) (*model.ChannelAccount, error) {
	for _, id := range excludeAccountIDs {
		if id == accountID {
			return nil, gorm.ErrRecordNotFound // 已试过该 key,无其它候选
		}
	}

	var account model.ChannelAccount
	err := model.DB().Transaction(func(tx *gorm.DB) error {
		q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND status = 1", accountID).
			Where("max_tasks = 0 OR current_tasks < max_tasks")
		// 排除对该模型熔断中的 key
		if modelCode != "" {
			q = q.Where(
				"id NOT IN (SELECT account_id FROM account_model_states WHERE model_code = ? AND disabled_until > ?)",
				modelCode, time.Now(),
			)
		}
		if err := q.First(&account).Error; err != nil {
			return err // 不存在/禁用/并发满/熔断中
		}
		return tx.Model(&account).UpdateColumn("current_tasks", gorm.Expr("current_tasks + 1")).Error
	})
	if err != nil {
		return nil, err
	}
	return &account, nil
}

func (s *UnifiedService) decrementAccountTasks(accountID uint) {
	model.DB().Model(&model.ChannelAccount{}).
		Where("id = ? AND current_tasks > 0", accountID).
		UpdateColumn("current_tasks", gorm.Expr("current_tasks - 1"))
}
