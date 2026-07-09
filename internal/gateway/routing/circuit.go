package routing

import (
	"time"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Circuit key 级 per-model 熔断。复用 account_model_states 表
// (account_id↔key_id, model_code↔model_name 语义映射)。
type Circuit struct{}

func NewCircuit() *Circuit { return &Circuit{} }

// MarkUnavailable 据上游错误决定是否熔断该 key 对该 model 的调用。
// 非熔断类错误(5xx 渠道故障)跳过。幂等:已存在则延长退避 + fail_count+1。
func (c *Circuit) MarkUnavailable(keyID uint, modelName string, err error) {
	if keyID == 0 || modelName == "" || err == nil {
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
		AccountID:     keyID,
		ModelCode:     modelName,
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
		logger.Error("gw circuit mark unavailable failed",
			zap.Uint("key_id", keyID), zap.String("model", modelName), zap.Error(dbErr))
		return
	}

	logger.Warn("gw circuit-broken",
		zap.Uint("key_id", keyID), zap.String("model", modelName),
		zap.Int("status", statusCode), zap.Time("until", disabledUntil))
}
