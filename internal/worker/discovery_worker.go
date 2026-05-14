package worker

import (
	"context"

	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/pkg/logger"
)

const TypeModelDiscoverySync = "model:discovery_sync"

// HandleModelDiscoverySync 定时同步上游模型列表（暂未实现）
func HandleModelDiscoverySync(ctx context.Context, t *asynq.Task) error {
	logger.Info("model discovery sync: not implemented yet")
	return nil
}

func NewModelDiscoverySyncTask() *asynq.Task {
	return asynq.NewTask(TypeModelDiscoverySync, nil)
}
