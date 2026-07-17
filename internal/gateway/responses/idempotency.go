package responses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	responseIdempotencyPendingTTL        = 30 * time.Minute
	responseIdempotencyHeartbeatInterval = 10 * time.Minute
	responseIdempotencyResultTTL         = 24 * time.Hour
	responseIdempotencyPollDelay         = 25 * time.Millisecond
)

var errResponseIdempotencyLeaseLost = errors.New("response idempotency lease was lost")

type responseIdempotencyLeaseOptions struct {
	duration  time.Duration
	heartbeat time.Duration
}

type responseIdempotencyClaim struct {
	tokenID     uint
	key         string
	requestHash string
	owner       string
	duration    time.Duration
	heartbeat   time.Duration
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	stopOnce    sync.Once
	errMu       sync.RWMutex
	err         error
}

func acquireResponseIdempotency(ctx context.Context, tokenID uint, key string, requestJSON []byte) (*responseIdempotencyClaim, *Result, error) {
	return acquireResponseIdempotencyWithOptions(ctx, tokenID, key, requestJSON, responseIdempotencyLeaseOptions{
		duration: responseIdempotencyPendingTTL, heartbeat: responseIdempotencyHeartbeatInterval,
	})
}

func acquireResponseIdempotencyWithOptions(
	ctx context.Context,
	tokenID uint,
	key string,
	requestJSON []byte,
	options responseIdempotencyLeaseOptions,
) (*responseIdempotencyClaim, *Result, error) {
	if options.duration <= 0 || options.heartbeat <= 0 || options.heartbeat >= options.duration {
		return nil, nil, errors.New("invalid response idempotency lease options")
	}
	// 幂等范围是 token + key；请求哈希用于拒绝同一 key 被不同正文复用。
	requestHash := hashResponseRequest(requestJSON)
	owner := uuid.NewString()
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}

		now := time.Now()
		entry := &model.AIResponseIdempotencyCache{
			TokenID: tokenID, IdempotencyKey: key, RequestHash: requestHash,
			Owner: owner, Status: model.ResponseIdempotencyPending,
			ExpiresAt: now.Add(options.duration),
		}
		// 先尝试无冲突插入，失败后读取现有行，避免依赖数据库特定的锁等待行为。
		createResult := model.DB().WithContext(ctx).
			Clauses(clause.OnConflict{DoNothing: true}).
			Create(entry)
		if createResult.Error != nil {
			return nil, nil, createResult.Error
		}

		var existing model.AIResponseIdempotencyCache
		lookupErr := model.DB().WithContext(ctx).
			Where("token_id = ? AND idempotency_key = ?", tokenID, key).
			First(&existing).Error
		if lookupErr != nil {
			if errors.Is(lookupErr, gorm.ErrRecordNotFound) {
				continue
			}
			return nil, nil, lookupErr
		}
		if existing.Owner == owner && existing.RequestHash == requestHash && existing.Status == model.ResponseIdempotencyPending {
			return newResponseIdempotencyClaim(ctx, &existing, options), nil, nil
		}

		if !existing.ExpiresAt.After(now) {
			// 过期 pending 代表原执行者已失联，可通过带过期条件的更新安全接管。
			result := model.DB().WithContext(ctx).Model(&model.AIResponseIdempotencyCache{}).
				Where("token_id = ? AND idempotency_key = ? AND expires_at <= ?", tokenID, key, now).
				Updates(map[string]any{
					"request_hash": requestHash, "owner": owner,
					"status":      model.ResponseIdempotencyPending,
					"response_id": "", "response_json": nil,
					"expires_at": now.Add(options.duration),
					"created_at": now, "updated_at": now,
				})
			if result.Error != nil {
				return nil, nil, result.Error
			}
			if result.RowsAffected > 0 {
				entry.CreatedAt = now
				entry.UpdatedAt = now
				return newResponseIdempotencyClaim(ctx, entry, options), nil, nil
			}
			continue
		}

		if existing.RequestHash != requestHash {
			return nil, nil, domain.ErrBadRequest("Idempotency-Key was already used with a different request")
		}
		if existing.Status == model.ResponseIdempotencyCompleted {
			replay, err := resultFromResponseIdempotency(&existing)
			return nil, replay, err
		}
		if existing.Status != model.ResponseIdempotencyPending {
			return nil, nil, fmt.Errorf("unsupported response idempotency state %q", existing.Status)
		}

		// 相同请求正在执行时短轮询结果；调用方 context 负责限制等待时间。
		timer := time.NewTimer(responseIdempotencyPollDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func newResponseIdempotencyClaim(
	ctx context.Context,
	entry *model.AIResponseIdempotencyCache,
	options responseIdempotencyLeaseOptions,
) *responseIdempotencyClaim {
	leaseCtx, cancel := context.WithCancel(ctx)
	claim := &responseIdempotencyClaim{
		tokenID: entry.TokenID, key: entry.IdempotencyKey,
		requestHash: entry.RequestHash, owner: entry.Owner,
		duration: options.duration, heartbeat: options.heartbeat,
		ctx: leaseCtx, cancel: cancel, done: make(chan struct{}),
	}
	go claim.renewLoop()
	return claim
}

func (claim *responseIdempotencyClaim) Context() context.Context {
	if claim == nil || claim.ctx == nil {
		return context.Background()
	}
	return claim.ctx
}

func (claim *responseIdempotencyClaim) Err() error {
	if claim == nil {
		return nil
	}
	claim.errMu.RLock()
	defer claim.errMu.RUnlock()
	return claim.err
}

func (claim *responseIdempotencyClaim) stopRenewal() {
	if claim == nil {
		return
	}
	claim.stopOnce.Do(claim.cancel)
	<-claim.done
}

func (claim *responseIdempotencyClaim) renewLoop() {
	defer close(claim.done)
	ticker := time.NewTicker(claim.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-claim.ctx.Done():
			return
		case <-ticker.C:
			err := renewResponseIdempotency(claim.ctx, claim, time.Now())
			if err == nil {
				continue
			}
			if claim.ctx.Err() != nil {
				return
			}
			claim.errMu.Lock()
			if claim.err == nil {
				claim.err = err
			}
			claim.errMu.Unlock()
			claim.cancel()
			return
		}
	}
}

func renewResponseIdempotency(ctx context.Context, claim *responseIdempotencyClaim, now time.Time) error {
	if claim == nil {
		return errResponseIdempotencyLeaseLost
	}
	result := model.DB().WithContext(ctx).Model(&model.AIResponseIdempotencyCache{}).
		Where("token_id = ? AND idempotency_key = ? AND request_hash = ? AND owner = ? AND status = ? AND expires_at > ?",
			claim.tokenID, claim.key, claim.requestHash, claim.owner, model.ResponseIdempotencyPending, now).
		Updates(map[string]any{"expires_at": now.Add(claim.duration), "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errResponseIdempotencyLeaseLost
	}
	return nil
}

func resultFromResponseIdempotency(entry *model.AIResponseIdempotencyCache) (*Result, error) {
	if entry == nil || entry.ResponseID == "" || len(entry.ResponseJSON) == 0 {
		return nil, errors.New("cached idempotent response is incomplete")
	}
	var response protocol.Response
	if err := json.Unmarshal(entry.ResponseJSON, &response); err != nil {
		return nil, fmt.Errorf("decode cached idempotent response: %w", err)
	}
	var record model.AIResponse
	if err := model.DB().Where("id = ? AND token_id = ?", entry.ResponseID, entry.TokenID).First(&record).Error; err != nil {
		return nil, err
	}
	return &Result{Response: &response, Record: &record, IdempotentReplay: true}, nil
}

func completeResponseIdempotency(claim *responseIdempotencyClaim, responseID string, response *protocol.Response) error {
	if claim == nil {
		return nil
	}
	defer claim.stopRenewal()
	if response == nil {
		return nil
	}
	responseJSON, err := json.Marshal(response)
	if err != nil {
		return err
	}
	err = model.DB().Transaction(func(tx *gorm.DB) error {
		return completeResponseIdempotencyTx(tx, claim, responseID, responseJSON)
	})
	return err
}

func completeResponseIdempotencyTx(tx *gorm.DB, claim *responseIdempotencyClaim, responseID string, responseJSON []byte) error {
	if claim == nil {
		return nil
	}
	now := time.Now()
	// owner 与未过期条件确保失去租约的旧执行者不能发布缓存结果。
	result := tx.Model(&model.AIResponseIdempotencyCache{}).
		Where("token_id = ? AND idempotency_key = ? AND request_hash = ? AND owner = ? AND status = ? AND expires_at > ?",
			claim.tokenID, claim.key, claim.requestHash, claim.owner, model.ResponseIdempotencyPending, now).
		Updates(map[string]any{
			"status": model.ResponseIdempotencyCompleted, "response_id": responseID,
			"response_json": responseJSON, "owner": "",
			"expires_at": now.Add(responseIdempotencyResultTTL), "updated_at": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errResponseIdempotencyLeaseLost
	}
	return nil
}

func releaseResponseIdempotency(claim *responseIdempotencyClaim) error {
	if claim == nil {
		return nil
	}
	claim.stopRenewal()
	return model.DB().
		Where("token_id = ? AND idempotency_key = ? AND request_hash = ? AND owner = ? AND status = ?",
			claim.tokenID, claim.key, claim.requestHash, claim.owner, model.ResponseIdempotencyPending).
		Delete(&model.AIResponseIdempotencyCache{}).Error
}
