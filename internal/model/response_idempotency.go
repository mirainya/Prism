package model

import (
	"time"

	"gorm.io/datatypes"
)

const (
	ResponseIdempotencyPending   = "pending"
	ResponseIdempotencyCompleted = "completed"
)

// AIResponseIdempotencyCache retains a response only for the bounded lifetime
// of an explicit Idempotency-Key. It is independent from Responses store=true.
type AIResponseIdempotencyCache struct {
	TokenID        uint           `gorm:"primaryKey;autoIncrement:false" json:"-"`
	IdempotencyKey string         `gorm:"primaryKey;type:varbinary(128)" json:"-"`
	RequestHash    string         `gorm:"type:char(64);not null" json:"-"`
	Owner          string         `gorm:"type:varchar(64);not null;default:''" json:"-"`
	Status         string         `gorm:"type:varchar(16);not null;index:idx_ai_response_idempotency_status" json:"-"`
	ResponseID     string         `gorm:"type:varchar(64);not null;default:''" json:"-"`
	ResponseJSON   datatypes.JSON `gorm:"type:json" json:"-"`
	ExpiresAt      time.Time      `gorm:"not null;index:idx_ai_response_idempotency_expires" json:"-"`
	CreatedAt      time.Time      `gorm:"not null" json:"-"`
	UpdatedAt      time.Time      `gorm:"not null" json:"-"`
}

func (AIResponseIdempotencyCache) TableName() string {
	return "ai_response_idempotency_cache"
}

func DeleteExpiredResponseIdempotencyCache(now time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	var entries []AIResponseIdempotencyCache
	if err := DB().Select("token_id", "idempotency_key").
		Where("expires_at <= ?", now).Order("expires_at ASC").Limit(limit).
		Find(&entries).Error; err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, nil
	}
	result := DB().Where("expires_at <= ?", now).Delete(&entries)
	return result.RowsAffected, result.Error
}
