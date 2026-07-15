package service

import (
	"time"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RetentionService struct{}

func NewRetentionService() *RetentionService { return &RetentionService{} }

func (s *RetentionService) DeleteExpiredTaskHistory(cutoff time.Time, limit int) (int64, error) {
	if !model.DB().Migrator().HasTable(&model.Task{}) {
		return 0, nil
	}
	limit = normalizeRetentionBatchSize(limit)
	var ids []uint
	if err := model.DB().Model(&model.Task{}).
		Where("status IN ?", []model.TaskStatus{
			model.TaskStatusSuccess, model.TaskStatusFailed, model.TaskStatusCancelled,
		}).
		Where("COALESCE(completed_at, updated_at, created_at) < ?", cutoff).
		Order("id ASC").Limit(limit).Pluck("id", &ids).Error; err != nil || len(ids) == 0 {
		return 0, err
	}
	result := model.DB().Unscoped().Where("id IN ?", ids).Delete(&model.Task{})
	if result.Error != nil {
		return 0, result.Error
	}
	return int64(len(ids)), nil
}

func (s *RetentionService) DeleteExpiredConversationHistory(cutoff time.Time, limit int) (int64, error) {
	if !model.DB().Migrator().HasTable(&model.Conversation{}) {
		return 0, nil
	}
	limit = normalizeRetentionBatchSize(limit)
	var ids []uint
	hasProjectionOutbox := model.DB().Migrator().HasTable(&model.ConversationProjectionOutbox{})
	query := model.DB().Model(&model.Conversation{}).
		Where("updated_at < ?", cutoff).
		Order("id ASC").Limit(limit)
	if hasProjectionOutbox {
		query = excludePendingConversationProjections(query)
	}
	if err := query.Pluck("id", &ids).Error; err != nil || len(ids) == 0 {
		return 0, err
	}
	return deleteExpiredConversationCandidates(model.DB(), cutoff, ids, hasProjectionOutbox)
}

func deleteExpiredConversationCandidates(
	database *gorm.DB,
	cutoff time.Time,
	ids []uint,
	hasProjectionOutbox bool,
) (int64, error) {
	var deleted int64
	err := database.Transaction(func(tx *gorm.DB) error {
		lockedQuery := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Model(&model.Conversation{}).
			Select("id").
			Where("id IN ? AND updated_at < ?", ids, cutoff).
			Order("id ASC")
		if hasProjectionOutbox {
			lockedQuery = excludePendingConversationProjections(lockedQuery)
		}
		var locked []model.Conversation
		if err := lockedQuery.Find(&locked).Error; err != nil {
			return err
		}
		if len(locked) == 0 {
			return nil
		}
		lockedIDs := make([]uint, len(locked))
		for i := range locked {
			lockedIDs[i] = locked[i].ID
		}
		if tx.Migrator().HasTable(&model.ConversationItem{}) {
			if err := tx.Where("conversation_id IN ?", lockedIDs).Delete(&model.ConversationItem{}).Error; err != nil {
				return err
			}
		}
		if tx.Migrator().HasTable(&model.ConversationTurn{}) {
			if err := tx.Where("conversation_id IN ?", lockedIDs).Delete(&model.ConversationTurn{}).Error; err != nil {
				return err
			}
		}
		if tx.Migrator().HasTable(&model.Message{}) {
			if err := tx.Where("conversation_id IN ?", lockedIDs).Delete(&model.Message{}).Error; err != nil {
				return err
			}
		}
		result := tx.Unscoped().Where("id IN ?", lockedIDs).Delete(&model.Conversation{})
		deleted = result.RowsAffected
		return result.Error
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

// excludePendingConversationProjections preserves every conversation that an
// unresolved previous_response_id could select. Protecting all same-owner
// matches also keeps an ambiguous identifier from changing meaning mid-call.
func excludePendingConversationProjections(query *gorm.DB) *gorm.DB {
	publicResponseMatch := ""
	if query.Migrator().HasTable(&model.ConversationTurn{}) && query.Migrator().HasTable(&model.AIResponse{}) {
		publicResponseMatch = `
					OR EXISTS (
						SELECT 1
						FROM conversation_turns AS response_turn
						JOIN ai_responses AS response ON response.call_id = response_turn.call_id
						WHERE response_turn.conversation_id = conversations.id
							AND response.id = projection.previous_response_id
							AND response.user_id = conversations.user_id
							AND response.token_id = conversations.token_id
					)`
	}
	return query.Where(`NOT EXISTS (
		SELECT 1
		FROM conversation_projection_outbox AS projection
		LEFT JOIN api_calls AS projection_call ON projection_call.id = projection.call_id
		LEFT JOIN api_calls AS latest_call ON latest_call.id = conversations.call_id
		WHERE projection.conversation_id = conversations.id
			OR (
				projection.conversation_id = 0
				AND projection.previous_response_id <> ''
				AND conversations.status = 1
				AND projection_call.user_id = conversations.user_id
				AND projection_call.token_id = conversations.token_id
				AND (
					(conversations.provider_response_id <> '' AND projection.previous_response_id = conversations.provider_response_id)
					OR (latest_call.resource_id <> '' AND projection.previous_response_id = latest_call.resource_id)
					` + publicResponseMatch + `
				)
			)
	)`)
}

func (s *RetentionService) DeleteExpiredCallMetadata(cutoff time.Time, limit int) (int64, error) {
	if !model.DB().Migrator().HasTable(&model.APICall{}) {
		return 0, nil
	}
	limit = normalizeRetentionBatchSize(limit)
	hasProjectionOutbox := model.DB().Migrator().HasTable(&model.ConversationProjectionOutbox{})
	hasConversations := model.DB().Migrator().HasTable(&model.Conversation{})
	var ids []string
	query := model.DB().Model(&model.APICall{}).
		Where("status IN ? AND COALESCE(completed_at, updated_at) < ?", []model.APICallStatus{
			model.APICallStatusCompleted, model.APICallStatusFailed, model.APICallStatusCancelled,
		}, cutoff).
		Order("created_at ASC").Limit(limit)
	if hasProjectionOutbox {
		query = query.Where("NOT EXISTS (SELECT 1 FROM conversation_projection_outbox AS projection WHERE projection.call_id = api_calls.id)")
	}
	if hasConversations {
		query = excludeConversationLatestCalls(query)
	}
	if err := query.Pluck("id", &ids).Error; err != nil || len(ids) == 0 {
		return 0, err
	}
	return deleteExpiredCallCandidates(model.DB(), cutoff, ids, hasProjectionOutbox, hasConversations)
}

func deleteExpiredCallCandidates(
	database *gorm.DB,
	cutoff time.Time,
	ids []string,
	hasProjectionOutbox bool,
	hasConversations bool,
) (int64, error) {
	var deleted int64
	err := database.Transaction(func(tx *gorm.DB) error {
		lockedQuery := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Model(&model.APICall{}).
			Select("id").
			Where("id IN ?", ids).
			Where("status IN ? AND COALESCE(completed_at, updated_at) < ?", []model.APICallStatus{
				model.APICallStatusCompleted, model.APICallStatusFailed, model.APICallStatusCancelled,
			}, cutoff).
			Order("created_at ASC")
		if hasProjectionOutbox {
			lockedQuery = lockedQuery.Where("NOT EXISTS (SELECT 1 FROM conversation_projection_outbox AS projection WHERE projection.call_id = api_calls.id)")
		}
		if hasConversations {
			lockedQuery = excludeConversationLatestCalls(lockedQuery)
		}
		var locked []model.APICall
		if err := lockedQuery.Find(&locked).Error; err != nil {
			return err
		}
		if len(locked) == 0 {
			return nil
		}
		lockedIDs := make([]string, len(locked))
		for i := range locked {
			lockedIDs[i] = locked[i].ID
		}
		if err := tx.Where("call_id IN ?", lockedIDs).Delete(&model.APICallPayload{}).Error; err != nil {
			return err
		}
		if err := tx.Where("call_id IN ?", lockedIDs).Delete(&model.APICallAttempt{}).Error; err != nil {
			return err
		}
		result := tx.Where("id IN ?", lockedIDs).Delete(&model.APICall{})
		deleted = result.RowsAffected
		return result.Error
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

func excludeConversationLatestCalls(query *gorm.DB) *gorm.DB {
	return query.Where(`NOT EXISTS (
		SELECT 1
		FROM conversations AS conversation
		WHERE conversation.call_id = api_calls.id
			AND conversation.deleted_at IS NULL
	)`)
}

func (s *RetentionService) DeleteExpiredRequestLogs(cutoff time.Time, limit int) (int64, error) {
	if !model.DB().Migrator().HasTable(&model.ChannelRequestLog{}) {
		return 0, nil
	}
	limit = normalizeRetentionBatchSize(limit)
	hasProjectionOutbox := model.DB().Migrator().HasTable(&model.ConversationProjectionOutbox{})
	var ids []uint
	query := model.DB().Unscoped().Model(&model.ChannelRequestLog{}).
		Where("created_at < ?", cutoff).
		Order("created_at ASC").Limit(limit)
	if hasProjectionOutbox {
		query = excludePendingRequestLogProjections(query)
	}
	if err := query.Pluck("id", &ids).Error; err != nil || len(ids) == 0 {
		return 0, err
	}
	return deleteExpiredRequestLogCandidates(model.DB(), cutoff, ids, hasProjectionOutbox)
}

func deleteExpiredRequestLogCandidates(
	database *gorm.DB,
	cutoff time.Time,
	ids []uint,
	hasProjectionOutbox bool,
) (int64, error) {
	var deleted int64
	err := database.Transaction(func(tx *gorm.DB) error {
		lockedQuery := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).
			Model(&model.ChannelRequestLog{}).
			Select("id").
			Where("id IN ? AND created_at < ?", ids, cutoff).
			Order("created_at ASC")
		if hasProjectionOutbox {
			lockedQuery = excludePendingRequestLogProjections(lockedQuery)
		}
		var locked []model.ChannelRequestLog
		if err := lockedQuery.Find(&locked).Error; err != nil {
			return err
		}
		if len(locked) == 0 {
			return nil
		}
		lockedIDs := make([]uint, len(locked))
		for i := range locked {
			lockedIDs[i] = locked[i].ID
		}
		result := tx.Unscoped().Where("id IN ?", lockedIDs).Delete(&model.ChannelRequestLog{})
		deleted = result.RowsAffected
		return result.Error
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

func excludePendingRequestLogProjections(query *gorm.DB) *gorm.DB {
	return query.Where(`NOT EXISTS (
		SELECT 1
		FROM conversation_projection_outbox AS projection
		WHERE (projection.request_log_id <> 0 AND projection.request_log_id = channel_request_logs.id)
			OR (
				projection.call_id <> ''
				AND channel_request_logs.call_id <> ''
				AND projection.call_id = channel_request_logs.call_id
			)
	)`)
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

// ClearLegacyBodies applies the payload retention policy to legacy response
// fields that predate api_call_payloads. Task parameters belong to task history
// and are removed when the task itself expires.
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

	return clearLegacyResponseBodies(cutoff, limit)
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
