package queue

import (
	"context"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

var Client *asynq.Client

func InitClient() error {
	Client = asynq.NewClient(redisClientOpt())
	return nil
}

func redisClientOpt() asynq.RedisClientOpt {
	cfg := config.C.Redis
	return asynq.RedisClientOpt{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	}
}

func NewServer() *asynq.Server {
	return asynq.NewServer(
		redisClientOpt(),
		asynq.Config{
			Concurrency: config.C.Worker.Concurrency,
			Queues: map[string]int{
				"critical": 6,
				"default":  3,
				"notify":   2,
				"low":      1,
			},
			Logger:       newAsynqLogger(),
			ErrorHandler: asynq.ErrorHandlerFunc(handleWorkerError),
		},
	)
}

// DefaultMaxRetry 返回配置的默认最大重试次数
func DefaultMaxRetry() int {
	r := config.C.Worker.MaxRetry
	if r <= 0 {
		return 3
	}
	return r
}

// handleWorkerError 自定义错误处理，将 worker 错误写入 zap logger
func handleWorkerError(ctx context.Context, task *asynq.Task, err error) {
	retried, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	queueName, _ := asynq.GetQueueName(ctx)

	logger.Error("worker task failed",
		zap.String("type", task.Type()),
		zap.String("queue", queueName),
		zap.Int("retried", retried),
		zap.Int("max_retry", maxRetry),
		zap.Error(err),
	)
}

// asynqLogger 适配 asynq.Logger 接口到 zap
type asynqLogger struct{}

func newAsynqLogger() *asynqLogger {
	return &asynqLogger{}
}

func (l *asynqLogger) Debug(args ...interface{}) {
	logger.Debug(fmt.Sprint(args...))
}

func (l *asynqLogger) Info(args ...interface{}) {
	logger.Info(fmt.Sprint(args...))
}

func (l *asynqLogger) Warn(args ...interface{}) {
	logger.Warn(fmt.Sprint(args...))
}

func (l *asynqLogger) Error(args ...interface{}) {
	logger.Error(fmt.Sprint(args...))
}

func (l *asynqLogger) Fatal(args ...interface{}) {
	logger.Error(fmt.Sprintf("[FATAL] %s", fmt.Sprint(args...)))
}

func Close() error {
	if Client != nil {
		return Client.Close()
	}
	return nil
}
