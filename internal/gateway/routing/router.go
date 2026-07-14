package routing

import (
	"encoding/json"
	"errors"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Router 基于 gw_* 表选路。
type Router struct{}

func NewRouter() *Router { return &Router{} }

var (
	// ErrModelNotFound means no enabled route declares the requested model.
	ErrModelNotFound = errors.New("model not found")
	// ErrCapabilityUnavailable means the model exists but none of its routes
	// support every feature required by the request.
	ErrCapabilityUnavailable = errors.New("requested capabilities are not supported by this model")
	// ErrNoCompatibleTransport means semantically matching routes exist, but
	// none declare a transport supported by the request execution plan.
	ErrNoCompatibleTransport = errors.New("no compatible upstream transport")
	// ErrNoRoute means matching routes exist but none are currently available.
	ErrNoRoute = errors.New("no route currently available")
)

// candidate 选路查询的一行(内部)。
// Select 选出一个可用路由目标,并原子占用其并发(current_conc+1)。
// excludeChannels/excludeKeys 为本轮重试已试过的,需排除。
// 返回的 RouteResult 需在请求结束后由调用方 Release 释放并发。

// 普通 FOR UPDATE(5.7 兼容,勿加 Table 别名)

// priority DESC, ability_id 稳定

// 最高优先级档内按 key weight 加权随机

// 原子占用并发

// Release 释放某 key 的并发占用(current_conc-1,不低于0)。请求结束时调用。
func (r *Router) Release(keyID uint) {
	if keyID == 0 {
		return
	}
	result := model.DB().Model(&model.GwChannelKey{}).
		Where("id = ? AND current_conc > 0", keyID).
		UpdateColumn("current_conc", gorm.Expr("current_conc - 1"))
	if result.Error != nil {
		logger.Error("failed to release gateway concurrency", zap.Uint("key_id", keyID), zap.Error(result.Error))
	}
}

func parseStrMap(raw []byte) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]string
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

func parseAnyMap(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}
