package service

import (
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

func (s *UnifiedService) selectAccountForEndpointTx(
	tx *gorm.DB,
	ep *model.Endpoint,
	excludeAccountIDs []uint,
) (*model.ChannelAccount, error) {
	var bindings []model.EndpointAccount
	q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Model(&model.EndpointAccount{}).
		Select("endpoint_accounts.*").
		Joins("JOIN channel_accounts ON channel_accounts.id = endpoint_accounts.account_id AND channel_accounts.deleted_at IS NULL").
		Where("endpoint_accounts.endpoint_id = ? AND endpoint_accounts.status = 1", ep.ID).
		Where("channel_accounts.channel_id = ? AND channel_accounts.status = 1", ep.ChannelID).
		Where("channel_accounts.max_tasks = 0 OR channel_accounts.current_tasks < channel_accounts.max_tasks")
	if ep.ModelCode != "" {
		q = q.Where(
			"endpoint_accounts.account_id NOT IN (SELECT account_id FROM account_model_states WHERE model_code = ? AND disabled_until > ?)",
			ep.ModelCode, time.Now(),
		)
	}
	if len(excludeAccountIDs) > 0 {
		q = q.Where("endpoint_accounts.account_id NOT IN ?", excludeAccountIDs)
	}
	if err := q.Order("endpoint_accounts.priority DESC, endpoint_accounts.id ASC").Find(&bindings).Error; err != nil {
		return nil, err
	}
	if len(bindings) == 0 {
		return nil, gorm.ErrRecordNotFound
	}

	// Priority defines fallback tiers. Weight distributes traffic only inside the highest available tier.
	highestPriority := bindings[0].Priority
	candidateCount := 0
	totalWeight := 0
	for i := range bindings {
		if bindings[i].Priority != highestPriority {
			break
		}
		candidateCount++
		weight := bindings[i].Weight
		if weight <= 0 {
			weight = 1
		}
		totalWeight += weight
	}
	selected := &bindings[0]
	randomWeight := rand.Intn(totalWeight)
	cumulative := 0
	for i := 0; i < candidateCount; i++ {
		weight := bindings[i].Weight
		if weight <= 0 {
			weight = 1
		}
		cumulative += weight
		if randomWeight < cumulative {
			selected = &bindings[i]
			break
		}
	}
	return s.selectBoundAccountTx(tx, selected.AccountID, ep.ModelCode, excludeAccountIDs)
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
		selected, err = s.selectAccountForEndpointTx(tx, ep, excludeAccountIDs)
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
