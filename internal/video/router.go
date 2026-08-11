package video

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var (
	ErrNoChannel      = errors.New("no available video channel")
	ErrNoKey          = errors.New("no available video channel key")
	ErrCircuitOpen    = errors.New("video channel key circuit open")
	ErrMaxConcurrency = errors.New("video channel key at max concurrency")
)

const (
	circuitThreshold  = 5
	circuitCooldownS  = 60
	concurrencyKeyTTL = 10 * time.Minute
)

// Router 视频选路器
type Router struct {
	db    *gorm.DB
	redis *redis.Client
}

func NewRouter(db *gorm.DB, rds *redis.Client) *Router {
	return &Router{db: db, redis: rds}
}

// Select 选择渠道和密钥
func (r *Router) Select(ctx context.Context, model string, caps RequiredCaps) (*VideoChannel, *VideoChannelKey, error) {
	return r.selectRoute(ctx, model, caps, 0, true, nil)
}

// SelectForEstimate selects a representative route without consuming generation concurrency.
func (r *Router) SelectForEstimate(ctx context.Context, model string, caps RequiredCaps) (*VideoChannel, *VideoChannelKey, error) {
	return r.selectRoute(ctx, model, caps, 0, false, nil)
}

// SelectCompatible selects the first route accepted by match. The matcher runs
// after a key is selected so it can validate adapter-specific request limits.
// A rejected route releases any concurrency slot acquired for this attempt.
func (r *Router) SelectCompatible(ctx context.Context, model string, caps RequiredCaps, channelID uint, acquireConcurrency bool, match func(*VideoChannel, *VideoChannelKey) bool) (*VideoChannel, *VideoChannelKey, error) {
	return r.selectRoute(ctx, model, caps, channelID, acquireConcurrency, match)
}

func (r *Router) selectRoute(ctx context.Context, model string, caps RequiredCaps, channelID uint, acquireConcurrency bool, match func(*VideoChannel, *VideoChannelKey) bool) (*VideoChannel, *VideoChannelKey, error) {
	channels, err := r.findChannels(ctx, model, caps, channelID)
	if err != nil {
		return nil, nil, err
	}
	if len(channels) == 0 {
		return nil, nil, ErrNoChannel
	}

	for _, ch := range channels {
		key, err := r.selectKey(ctx, ch, acquireConcurrency)
		if err != nil {
			continue
		}
		if match != nil && !match(ch, key) {
			if acquireConcurrency {
				r.ReleaseConcurrency(ctx, key.ID)
			}
			continue
		}
		return ch, key, nil
	}
	return nil, nil, ErrNoKey
}

// ReleaseConcurrency 任务结束后释放并发槽
func (r *Router) ReleaseConcurrency(ctx context.Context, keyID uint) {
	if r == nil || r.redis == nil || r.db == nil || keyID == 0 {
		return
	}
	var key VideoChannelKey
	if err := r.db.WithContext(ctx).Select("id", "max_concurrency").First(&key, keyID).Error; err != nil || key.MaxConcurrency <= 0 {
		return
	}
	rkey := fmt.Sprintf("video:concurrency:%d", keyID)
	value, err := r.redis.Decr(ctx, rkey).Result()
	if err == nil && value < 0 {
		r.redis.Set(ctx, rkey, 0, concurrencyKeyTTL)
	}
}

func (r *Router) findChannels(ctx context.Context, model string, caps RequiredCaps, channelID uint) ([]*VideoChannel, error) {
	var channels []*VideoChannel
	query := r.db.WithContext(ctx).Where("status = ?", "active")
	if channelID > 0 {
		query = query.Where("id = ?", channelID)
	}
	err := query.
		Order("priority DESC").
		Find(&channels).Error
	if err != nil {
		return nil, err
	}

	var matched []*VideoChannel
	for _, ch := range channels {
		if !r.channelSupportsModel(ch, model) {
			continue
		}
		if !r.channelMeetsCaps(ch, caps) {
			continue
		}
		matched = append(matched, ch)
	}
	return matched, nil
}

func (r *Router) channelSupportsModel(ch *VideoChannel, model string) bool {
	var models []string
	if err := json.Unmarshal(ch.Models, &models); err != nil {
		return false
	}
	for _, m := range models {
		if m == model {
			return true
		}
	}
	return false
}

func (r *Router) channelMeetsCaps(ch *VideoChannel, caps RequiredCaps) bool {
	if ch.Capabilities == nil {
		return !caps.FirstFrame && !caps.LastFrame && !caps.Cancel && !caps.Audio && !caps.WebSearch
	}
	var declared map[string]bool
	if err := json.Unmarshal(ch.Capabilities, &declared); err != nil {
		return false
	}
	if caps.FirstFrame && !declared["first_frame"] {
		return false
	}
	if caps.LastFrame && !declared["last_frame"] {
		return false
	}
	if caps.Cancel && !declared["cancel"] {
		return false
	}
	if caps.Audio && !declared["audio"] {
		return false
	}
	if caps.WebSearch && !declared["web_search"] {
		return false
	}
	return true
}

func (r *Router) selectKey(ctx context.Context, ch *VideoChannel, acquireConcurrency bool) (*VideoChannelKey, error) {
	var keys []*VideoChannelKey
	err := r.db.WithContext(ctx).
		Where("channel_id = ? AND status = ?", ch.ID, "active").
		Find(&keys).Error
	if err != nil || len(keys) == 0 {
		return nil, ErrNoKey
	}

	// 过滤熔断
	var candidates []*VideoChannelKey
	for _, k := range keys {
		if r.isCircuitOpen(ctx, k.ID) {
			continue
		}
		candidates = append(candidates, k)
	}
	if len(candidates) == 0 {
		return nil, ErrCircuitOpen
	}

	// 加权随机
	key := r.weightedSelect(candidates)

	// 并发检查
	if acquireConcurrency && key.MaxConcurrency > 0 {
		if err := r.acquireConcurrency(ctx, key); err != nil {
			return nil, err
		}
	}
	return key, nil
}

func (r *Router) isCircuitOpen(ctx context.Context, keyID uint) bool {
	if r == nil || r.redis == nil {
		return false
	}
	rkey := fmt.Sprintf("video:circuit:%d", keyID)
	val, err := r.redis.Get(ctx, rkey).Int()
	if err != nil {
		return false
	}
	return val >= circuitThreshold
}

// RecordFailure 记录失败，触发熔断
func (r *Router) RecordFailure(ctx context.Context, keyID uint) {
	if r == nil || r.redis == nil {
		return
	}
	rkey := fmt.Sprintf("video:circuit:%d", keyID)
	r.redis.Incr(ctx, rkey)
	r.redis.Expire(ctx, rkey, time.Duration(circuitCooldownS)*time.Second)
}

// RecordSuccess 记录成功，重置熔断计数
func (r *Router) RecordSuccess(ctx context.Context, keyID uint) {
	if r == nil || r.redis == nil {
		return
	}
	rkey := fmt.Sprintf("video:circuit:%d", keyID)
	r.redis.Del(ctx, rkey)
}

func (r *Router) weightedSelect(keys []*VideoChannelKey) *VideoChannelKey {
	totalWeight := 0
	for _, k := range keys {
		totalWeight += k.Weight
	}
	if totalWeight == 0 {
		return keys[rand.Intn(len(keys))]
	}
	roll := rand.Intn(totalWeight)
	for _, k := range keys {
		roll -= k.Weight
		if roll < 0 {
			return k
		}
	}
	return keys[len(keys)-1]
}

func (r *Router) acquireConcurrency(ctx context.Context, key *VideoChannelKey) error {
	if r == nil || r.redis == nil {
		return nil
	}
	rkey := fmt.Sprintf("video:concurrency:%d", key.ID)
	val, err := r.redis.Incr(ctx, rkey).Result()
	if err != nil {
		return err
	}
	r.redis.Expire(ctx, rkey, concurrencyKeyTTL)
	if int(val) > key.MaxConcurrency {
		r.redis.Decr(ctx, rkey)
		return ErrMaxConcurrency
	}
	return nil
}
