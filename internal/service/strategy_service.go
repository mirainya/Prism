package service

import (
	"errors"
	"math/rand"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrNoChannelCapability = errors.New("no available channel capability")
	ErrNoChannelAccount    = errors.New("no available channel account")
)

type ChannelCapabilityResult struct {
	Channel           *model.Channel
	ChannelCapability *model.ChannelCapability
}

type AccountResult struct {
	Account *model.ChannelAccount
}

type StrategyService struct{}

func NewStrategyService() *StrategyService {
	return &StrategyService{}
}

// SelectChannelCapability 根据统一模型名选择渠道能力配置（加权随机）
func (s *StrategyService) SelectChannelCapability(modelName string) (*ChannelCapabilityResult, error) {
	var ccList []model.ChannelCapability
	err := model.DB().Where("model = ? AND status = 1", modelName).Find(&ccList).Error
	if err != nil || len(ccList) == 0 {
		return nil, ErrNoChannelCapability
	}

	// 过滤出渠道启用的配置
	type candidate struct {
		cc      model.ChannelCapability
		channel model.Channel
	}
	var candidates []candidate
	for _, cc := range ccList {
		var ch model.Channel
		if err := model.DB().Where("id = ? AND status = 1", cc.ChannelID).First(&ch).Error; err == nil {
			candidates = append(candidates, candidate{cc: cc, channel: ch})
		}
	}

	if len(candidates) == 0 {
		return nil, ErrNoChannelCapability
	}

	// 只有一个直接返回
	if len(candidates) == 1 {
		return &ChannelCapabilityResult{
			Channel:           &candidates[0].channel,
			ChannelCapability: &candidates[0].cc,
		}, nil
	}

	// 多个候选：随机选择（简单均匀随机）
	idx := rand.Intn(len(candidates))
	return &ChannelCapabilityResult{
		Channel:           &candidates[idx].channel,
		ChannelCapability: &candidates[idx].cc,
	}, nil
}

// SelectAccount 从渠道账号池中选择账号（SELECT ... FOR UPDATE 防并发）
func (s *StrategyService) SelectAccount(channelID uint) (*AccountResult, error) {
	var account model.ChannelAccount

	err := model.DB().Transaction(func(tx *gorm.DB) error {
		// 加行锁选择负载最低的账号（过滤掉已达并发上限的，max_tasks=0表示不限制）
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("channel_id = ? AND status = 1", channelID).
			Where("max_tasks = 0 OR current_tasks < max_tasks").
			Order("current_tasks ASC, weight DESC").
			First(&account).Error; err != nil {
			return err
		}
		// 选中后立即递增任务数（在同一事务内）
		return tx.Model(&account).UpdateColumn("current_tasks", gorm.Expr("current_tasks + 1")).Error
	})

	if err != nil {
		return nil, ErrNoChannelAccount
	}

	return &AccountResult{
		Account: &account,
	}, nil
}

// IncrementAccountTasks 增加账号当前任务数
func (s *StrategyService) IncrementAccountTasks(accountID uint) error {
	return model.DB().Model(&model.ChannelAccount{}).
		Where("id = ?", accountID).
		UpdateColumn("current_tasks", gorm.Expr("current_tasks + 1")).Error
}

// DecrementAccountTasks 减少账号当前任务数
func (s *StrategyService) DecrementAccountTasks(accountID uint) error {
	return model.DB().Model(&model.ChannelAccount{}).
		Where("id = ? AND current_tasks > 0", accountID).
		UpdateColumn("current_tasks", gorm.Expr("current_tasks - 1")).Error
}
