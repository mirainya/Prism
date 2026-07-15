package worker

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

const payloadCleanupBatchSize = 500

var timeNow = time.Now

func HandleAPICallPayloadCleanup(ctx context.Context, _ *asynq.Task) error {
	now := timeNow()
	var idempotencyTotal int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		deleted, err := model.DeleteExpiredResponseIdempotencyCache(now, payloadCleanupBatchSize)
		if err != nil {
			return err
		}
		idempotencyTotal += deleted
		if deleted < payloadCleanupBatchSize {
			break
		}
	}
	if idempotencyTotal > 0 {
		logger.Info("expired response idempotency cache deleted", zap.Int64("count", idempotencyTotal))
	}

	callService := service.NewAPICallService()
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		deleted, err := callService.DeleteExpiredPayloads(now, payloadCleanupBatchSize)
		if err != nil {
			return err
		}
		total += deleted
		if deleted < payloadCleanupBatchSize {
			break
		}
	}
	if total > 0 {
		logger.Info("expired API call payloads deleted", zap.Int64("count", total))
	}
	retentionHours := config.DefaultAPICallPayloadRetentionHours
	if cfg := config.Get(); cfg != nil && cfg.Observability.APICallPayloadRetentionHours > 0 {
		retentionHours = cfg.Observability.APICallPayloadRetentionHours
	}
	requestLogs := service.NewRequestLogService()
	var clearedTotal int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		cleared, err := requestLogs.ClearExpiredBodies(now, retentionHours, payloadCleanupBatchSize)
		if err != nil {
			return err
		}
		clearedTotal += cleared
		if cleared < payloadCleanupBatchSize {
			break
		}
	}
	if clearedTotal > 0 {
		logger.Info("expired upstream log bodies cleared", zap.Int64("count", clearedTotal))
	}
	if err := cleanupExpiredMetadata(ctx, now); err != nil {
		return err
	}
	return nil
}

func cleanupExpiredMetadata(ctx context.Context, now time.Time) error {
	metadataDays := config.DefaultAPICallMetadataRetentionDays
	resourceDays := config.DefaultResourceHistoryRetentionDays
	accessDays := config.DefaultAPIAccessLogRetentionDays
	auditDays := config.DefaultAuditEventRetentionDays
	billingDays := config.DefaultBillingLedgerRetentionDays
	if cfg := config.Get(); cfg != nil {
		if cfg.Observability.APICallMetadataRetentionDays > 0 {
			metadataDays = cfg.Observability.APICallMetadataRetentionDays
		}
		if cfg.Observability.ResourceHistoryRetentionDays > 0 {
			resourceDays = cfg.Observability.ResourceHistoryRetentionDays
		}
		if cfg.Observability.APIAccessLogRetentionDays > 0 {
			accessDays = cfg.Observability.APIAccessLogRetentionDays
		}
		if cfg.Observability.AuditEventRetentionDays > 0 {
			auditDays = cfg.Observability.AuditEventRetentionDays
		}
		if cfg.Observability.BillingLedgerRetentionDays > 0 {
			billingDays = cfg.Observability.BillingLedgerRetentionDays
		}
	}
	retention := service.NewRetentionService()
	jobs := []struct {
		name   string
		days   int
		delete func(time.Time, int) (int64, error)
	}{
		{name: "task history", days: resourceDays, delete: retention.DeleteExpiredTaskHistory},
		{name: "conversation history", days: resourceDays, delete: retention.DeleteExpiredConversationHistory},
		{name: "API call metadata", days: metadataDays, delete: retention.DeleteExpiredCallMetadata},
		{name: "upstream request log metadata", days: metadataDays, delete: retention.DeleteExpiredRequestLogs},
		{name: "API access logs", days: accessDays, delete: retention.DeleteExpiredAPIAccessLogs},
		{name: "audit events", days: auditDays, delete: retention.DeleteExpiredAuditEvents},
		{name: "billing logs", days: billingDays, delete: retention.DeleteExpiredBillingLogs},
		{name: "balance entries", days: billingDays, delete: retention.DeleteExpiredBalanceEntries},
	}
	for _, job := range jobs {
		var total int64
		cutoff := now.AddDate(0, 0, -job.days)
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			deleted, err := job.delete(cutoff, payloadCleanupBatchSize)
			if err != nil {
				return err
			}
			total += deleted
			if deleted < payloadCleanupBatchSize {
				break
			}
		}
		if total > 0 {
			logger.Info("expired metadata deleted", zap.String("kind", job.name), zap.Int64("count", total))
		}
	}
	return nil
}

func NewAPICallPayloadCleanupTask() *asynq.Task {
	return asynq.NewTask(TypeAPICallPayloadCleanup, nil)
}
