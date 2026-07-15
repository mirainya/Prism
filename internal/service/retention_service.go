package service

import (
	"time"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

type RetentionService struct{}

func NewRetentionService() *RetentionService { return &RetentionService{} }

func (s *RetentionService) DeleteExpiredCallMetadata(cutoff time.Time, limit int) (int64, error) {
	if !model.DB().Migrator().HasTable(&model.APICall{}) {
		return 0, nil
	}
	limit = normalizeRetentionBatchSize(limit)
	var ids []string
	if err := model.DB().Model(&model.APICall{}).
		Where("status IN ? AND COALESCE(completed_at, updated_at) < ?", []model.APICallStatus{
			model.APICallStatusCompleted, model.APICallStatusFailed, model.APICallStatusCancelled,
		}, cutoff).
		Order("created_at ASC").Limit(limit).Pluck("id", &ids).Error; err != nil || len(ids) == 0 {
		return 0, err
	}
	err := model.DB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("call_id IN ?", ids).Delete(&model.APICallPayload{}).Error; err != nil {
			return err
		}
		if err := tx.Where("call_id IN ?", ids).Delete(&model.APICallAttempt{}).Error; err != nil {
			return err
		}
		return tx.Where("id IN ?", ids).Delete(&model.APICall{}).Error
	})
	if err != nil {
		return 0, err
	}
	return int64(len(ids)), nil
}

func (s *RetentionService) DeleteExpiredRequestLogs(cutoff time.Time, limit int) (int64, error) {
	return deleteBaseModelBatch(&model.ChannelRequestLog{}, "created_at", cutoff, limit)
}

func (s *RetentionService) DeleteExpiredBillingLogs(cutoff time.Time, limit int) (int64, error) {
	return deleteBaseModelBatch(&model.BillingLog{}, "created_at", cutoff, limit)
}

func (s *RetentionService) DeleteExpiredBalanceEntries(cutoff time.Time, limit int) (int64, error) {
	return deleteUint64ModelBatch(&model.BalanceEntry{}, cutoff, limit)
}

func (s *RetentionService) DeleteExpiredAPIAccessLogs(cutoff time.Time, limit int) (int64, error) {
	return deleteUint64ModelBatch(&model.APIAccessLog{}, cutoff, limit)
}

func (s *RetentionService) DeleteExpiredAuditEvents(cutoff time.Time, limit int) (int64, error) {
	return deleteUint64ModelBatch(&model.AuditEvent{}, cutoff, limit)
}

// ClearLegacyBodies applies the payload retention policy to body fields that
// predate api_call_payloads. New terminal tasks already clear these fields.
func (s *RetentionService) ClearLegacyBodies(
	now time.Time,
	retentionHours int,
	retainBodies bool,
	limit int,
) (int64, error) {
	if now.IsZero() {
		now = time.Now()
	}
	limit = normalizeRetentionBatchSize(limit)
	cutoff := now
	if retainBodies {
		if retentionHours <= 0 {
			return 0, nil
		}
		cutoff = now.Add(-time.Duration(retentionHours) * time.Hour)
	}

	taskCount, err := clearLegacyTaskBodies(cutoff, limit)
	if err != nil {
		return 0, err
	}
	responseCount, err := clearLegacyResponseBodies(cutoff, limit)
	if err != nil {
		return taskCount, err
	}
	return taskCount + responseCount, nil
}

func clearLegacyTaskBodies(cutoff time.Time, limit int) (int64, error) {
	if !model.DB().Migrator().HasTable(&model.Task{}) {
		return 0, nil
	}
	var ids []uint
	if err := model.DB().Model(&model.Task{}).
		Where("status IN ?", []model.TaskStatus{
			model.TaskStatusSuccess, model.TaskStatusFailed, model.TaskStatusCancelled,
		}).
		Where("COALESCE(completed_at, updated_at, created_at) <= ?", cutoff).
		Where("request_params IS NOT NULL OR mapped_params IS NOT NULL").
		Order("id ASC").Limit(limit).Pluck("id", &ids).Error; err != nil || len(ids) == 0 {
		return 0, err
	}
	result := model.DB().Model(&model.Task{}).Where("id IN ?", ids).Updates(map[string]any{
		"request_params": nil,
		"mapped_params":  nil,
	})
	return result.RowsAffected, result.Error
}

func clearLegacyResponseBodies(cutoff time.Time, limit int) (int64, error) {
	if !model.DB().Migrator().HasTable(&model.AIResponse{}) {
		return 0, nil
	}
	var ids []string
	if err := model.DB().Model(&model.AIResponse{}).
		Where("store = ?", false).
		Where("status IN ?", []string{"completed", "incomplete", "failed", "cancelled"}).
		Where("COALESCE(completed_at, created_at) <= ?", cutoff).
		Where("request_json IS NOT NULL OR input_items IS NOT NULL OR output_items IS NOT NULL OR response_json IS NOT NULL OR metadata IS NOT NULL").
		Order("id ASC").Limit(limit).Pluck("id", &ids).Error; err != nil || len(ids) == 0 {
		return 0, err
	}
	result := model.DB().Model(&model.AIResponse{}).Where("id IN ?", ids).Updates(map[string]any{
		"request_json":  nil,
		"input_items":   nil,
		"output_items":  nil,
		"response_json": nil,
		"metadata":      nil,
	})
	return result.RowsAffected, result.Error
}

func deleteBaseModelBatch(value any, timeColumn string, cutoff time.Time, limit int) (int64, error) {
	if !model.DB().Migrator().HasTable(value) {
		return 0, nil
	}
	limit = normalizeRetentionBatchSize(limit)
	var ids []uint
	if err := model.DB().Unscoped().Model(value).Where(timeColumn+" < ?", cutoff).
		Order(timeColumn+" ASC").Limit(limit).Pluck("id", &ids).Error; err != nil || len(ids) == 0 {
		return 0, err
	}
	result := model.DB().Unscoped().Where("id IN ?", ids).Delete(value)
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func deleteUint64ModelBatch(value any, cutoff time.Time, limit int) (int64, error) {
	if !model.DB().Migrator().HasTable(value) {
		return 0, nil
	}
	limit = normalizeRetentionBatchSize(limit)
	var ids []uint64
	if err := model.DB().Model(value).Where("created_at < ?", cutoff).
		Order("created_at ASC").Limit(limit).Pluck("id", &ids).Error; err != nil || len(ids) == 0 {
		return 0, err
	}
	result := model.DB().Where("id IN ?", ids).Delete(value)
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func normalizeRetentionBatchSize(limit int) int {
	if limit <= 0 || limit > 5000 {
		return 500
	}
	return limit
}
