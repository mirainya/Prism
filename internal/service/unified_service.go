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
	MaxTokens              int           `json:"-"`
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

// selectAccountTx selects and acquires an account inside the caller's transaction.
func (s *UnifiedService) selectAccountTx(
	tx *gorm.DB,
	channelID uint,
	modelCode string,
	excludeAccountIDs []uint,
	requireExplicit bool,
) (*model.ChannelAccount, error) {
	var accounts []model.ChannelAccount

	q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("channel_id = ? AND status = 1", channelID).
		Where("max_tasks = 0 OR current_tasks < max_tasks")

	// 白名单: supported_models 为空(NULL/[]/null)=支持所有; 非空则必须包含 modelCode
	if modelCode != "" {
		if requireExplicit {
			// 必须显式列出该 model(NULL/空白名单不参与)
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
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, gorm.ErrRecordNotFound
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

	if err := tx.Model(selected).UpdateColumn("current_tasks", gorm.Expr("current_tasks + 1")).Error; err != nil {
		return nil, err
	}
	result := *selected
	return &result, nil
}

func (s *UnifiedService) selectAccountForEndpointTx(
	tx *gorm.DB,
	ep *model.Endpoint,
	excludeAccountIDs []uint,
) (*model.ChannelAccount, error) {
	if ep.AccountID != 0 {
		return s.selectBoundAccountTx(tx, ep.AccountID, ep.ModelCode, excludeAccountIDs)
	}
	return s.selectAccountTx(tx, ep.ChannelID, ep.ModelCode, excludeAccountIDs, false)
}

// selectAndAssignAccountForEndpoint acquires a fallback account slot and
// records its ownership on the task in the same transaction.
func (s *UnifiedService) selectAndAssignAccountForEndpoint(
	taskID uint,
	ep *model.Endpoint,
	excludeAccountIDs []uint,
) (*model.ChannelAccount, error) {
	var selected *model.ChannelAccount
	err := model.DB().Transaction(func(tx *gorm.DB) error {
		var task model.Task
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "status", "account_slot_released").
			First(&task, taskID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return ErrTaskNotExecutable
			}
			return err
		}
		if (task.Status != model.TaskStatusPending && task.Status != model.TaskStatusProcessing) ||
			!task.AccountSlotReleased {
			return ErrTaskNotExecutable
		}

		var err error
		if ep.AccountID != 0 {
			selected, err = s.selectBoundAccountTx(tx, ep.AccountID, ep.ModelCode, excludeAccountIDs)
		} else {
			selected, err = s.selectAccountTx(tx, ep.ChannelID, ep.ModelCode, excludeAccountIDs, false)
		}
		if err != nil {
			return err
		}

		result := tx.Model(&model.Task{}).
			Where("id = ? AND status IN ? AND account_slot_released = ?", taskID,
				[]model.TaskStatus{model.TaskStatusPending, model.TaskStatusProcessing}, true).
			Updates(map[string]any{
				"endpoint_id":           ep.ID,
				"channel_id":            ep.ChannelID,
				"account_id":            selected.ID,
				"account_slot_released": false,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrTaskNotExecutable
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return selected, nil
}

func (s *UnifiedService) ensureTaskAccountExecutable(taskID uint, ep *model.Endpoint, accountID uint) error {
	var count int64
	err := model.DB().Model(&model.Task{}).
		Where("id = ? AND status IN ? AND endpoint_id = ? AND channel_id = ? AND account_id = ? AND account_slot_released = ?",
			taskID, []model.TaskStatus{model.TaskStatusPending, model.TaskStatusProcessing},
			ep.ID, ep.ChannelID, accountID, false).
		Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrTaskNotExecutable
	}
	return nil
}

// selectBoundAccountTx acquires an endpoint-bound account inside the caller's transaction.
func (s *UnifiedService) selectBoundAccountTx(
	tx *gorm.DB,
	accountID uint,
	modelCode string,
	excludeAccountIDs []uint,
) (*model.ChannelAccount, error) {
	for _, id := range excludeAccountIDs {
		if id == accountID {
			return nil, gorm.ErrRecordNotFound // 已试过该 key,无其它候选
		}
	}

	var account model.ChannelAccount
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
		return nil, err // 不存在/禁用/并发满/熔断中
	}
	if err := tx.Model(&account).UpdateColumn("current_tasks", gorm.Expr("current_tasks + 1")).Error; err != nil {
		return nil, err
	}
	return &account, nil
}
