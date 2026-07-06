package service

import (
	"time"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AccountCircuitService 账号级 per-model 熔断管理
// 当某账号(key)对某模型调用失败(401/403/404/429)时,标记其在一段时间内不可用,
// 到期后由清理任务物理删除,自动恢复。
type AccountCircuitService struct{}

func NewAccountCircuitService() *AccountCircuitService {
	return &AccountCircuitService{}
}

// MarkUnavailable 根据上游错误决定是否熔断该账号对该模型的调用
// 非熔断类错误(如 5xx 渠道故障)直接跳过。幂等: 已存在则延长退避+累加 fail_count。
func (s *AccountCircuitService) MarkUnavailable(accountID uint, modelCode string, err error) {
	if accountID == 0 || modelCode == "" || err == nil {
		return
	}
	shouldBreak, backoff := domain.ClassifyUpstreamError(err)
	if !shouldBreak || backoff <= 0 {
		return
	}

	statusCode := domain.UpstreamStatusCode(err)
	disabledUntil := time.Now().Add(backoff)
	reason := err.Error()
	if len(reason) > 500 {
		reason = reason[:500]
	}

	state := model.AccountModelState{
		AccountID:     accountID,
		ModelCode:     modelCode,
		DisabledUntil: disabledUntil,
		Reason:        reason,
		StatusCode:    statusCode,
		FailCount:     1,
	}

	if dbErr := model.DB().Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "account_id"}, {Name: "model_code"}},
		DoUpdates: clause.Assignments(map[string]any{
			"disabled_until": disabledUntil,
			"reason":         reason,
			"status_code":    statusCode,
			"fail_count":     gorm.Expr("fail_count + 1"),
			"updated_at":     time.Now(),
		}),
	}).Create(&state).Error; dbErr != nil {
		logger.Error("mark account model unavailable failed",
			zap.Uint("account_id", accountID), zap.String("model", modelCode), zap.Error(dbErr))
		return
	}

	logger.Warn("account model circuit-broken",
		zap.Uint("account_id", accountID), zap.String("model", modelCode),
		zap.Int("status", statusCode), zap.Time("until", disabledUntil))
}

// IsAvailable 检查账号对某模型当前是否可用(未在熔断退避期内)
func (s *AccountCircuitService) IsAvailable(accountID uint, modelCode string) bool {
	var count int64
	model.DB().Model(&model.AccountModelState{}).
		Where("account_id = ? AND model_code = ? AND disabled_until > ?", accountID, modelCode, time.Now()).
		Count(&count)
	return count == 0
}

// ListByAccount 列出账号当前生效的熔断状态(供管理界面展示)
func (s *AccountCircuitService) ListByAccount(accountID uint) ([]model.AccountModelState, error) {
	var states []model.AccountModelState
	err := model.DB().
		Where("account_id = ? AND disabled_until > ?", accountID, time.Now()).
		Order("disabled_until DESC").
		Find(&states).Error
	return states, err
}

// Clear 手动解除某账号对某模型的熔断(管理员操作)
func (s *AccountCircuitService) Clear(accountID uint, modelCode string) error {
	return model.DB().
		Where("account_id = ? AND model_code = ?", accountID, modelCode).
		Delete(&model.AccountModelState{}).Error
}

// CleanExpired 物理删除已到期的熔断记录,避免表膨胀
func (s *AccountCircuitService) CleanExpired() (int64, error) {
	result := model.DB().Where("disabled_until < ?", time.Now()).Delete(&model.AccountModelState{})
	return result.RowsAffected, result.Error
}
