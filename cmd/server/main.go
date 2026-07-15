package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/internal/api"
	"github.com/mirainya/Prism/internal/gateway"
	"github.com/mirainya/Prism/internal/gateway/engine"
	responsepipeline "github.com/mirainya/Prism/internal/gateway/responses"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider"
	"github.com/mirainya/Prism/internal/worker"
	"github.com/mirainya/Prism/pkg/cache"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/database"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/mirainya/Prism/pkg/queue"
)

func main() {
	// 获取可执行文件所在目录
	execPath, err := os.Executable()
	if err != nil {
		log.Fatalf("failed to get executable path: %v", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		log.Fatalf("failed to eval symlinks: %v", err)
	}
	execDir := filepath.Dir(execPath)

	cwd, _ := os.Getwd()

	configPaths := []string{
		filepath.Join(cwd, "configs", "config.yaml"),
		filepath.Join(execDir, "configs", "config.yaml"),
		"configs/config.yaml",
	}

	var configPath string
	for _, p := range configPaths {
		if _, err := os.Stat(p); err == nil {
			configPath = p
			break
		}
	}

	if configPath == "" {
		log.Fatalf("config file not found, searched paths:\n  - %s",
			filepath.Join(cwd, "configs", "config.yaml")+"\n  - "+
				filepath.Join(execDir, "configs", "config.yaml"))
	}

	log.Printf("loading config from: %s", configPath)

	if err := config.Load(configPath); err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	config.Watch()

	if err := logger.Init(); err != nil {
		log.Fatalf("failed to init logger: %v", err)
	}

	db, err := database.Connect()
	if err != nil {
		log.Fatalf("failed to connect database: %v", err)
	}
	model.SetDB(db)
	if config.C.Server.ShouldResetGatewayConcurrency() {
		if err := model.DB().Model(&model.GwChannelKey{}).Where("current_conc <> 0").Update("current_conc", 0).Error; err != nil {
			log.Fatalf("failed to reset gateway concurrency: %v", err)
		}
	}

	if err := model.AutoMigrate(); err != nil {
		log.Fatalf("failed to migrate database: %v", err)
	}

	if err := cache.Init(); err != nil {
		log.Fatalf("failed to init cache: %v", err)
	}

	if err := queue.InitClient(); err != nil {
		log.Fatalf("failed to init queue client: %v", err)
	}

	// 初始化 HTTP Client
	provider.InitHTTPClient()
	v2Engine, err := gateway.NewV2Engine()
	if err != nil {
		log.Fatalf("failed to initialize Gateway V2: %v", err)
	}

	// 启动 Worker
	reconciled, err := responsepipeline.ReconcilePendingResponseRefunds(context.Background())
	if err != nil {
		log.Fatalf("failed to reconcile response refunds: %v", err)
	}
	if reconciled > 0 {
		logger.Info(fmt.Sprintf("reconciled %d response refunds", reconciled))
	}
	recovered, err := responsepipeline.RequeuePendingBackground(context.Background())
	if err != nil {
		log.Fatalf("failed to recover background responses: %v", err)
	}
	if recovered > 0 {
		logger.Info(fmt.Sprintf("requeued %d background responses", recovered))
	}
	recoveredTasks, err := worker.RecoverPendingTaskSubmissions(context.Background())
	if err != nil {
		log.Fatalf("failed to recover pending task submissions: %v", err)
	}
	if recoveredTasks > 0 {
		logger.Info(fmt.Sprintf("recovered %d pending task submissions", recoveredTasks))
	}
	workerSrv := startWorker(v2Engine)

	// 启动 Scheduler
	scheduler := startScheduler()

	// 设置路由并启动 HTTP 服务
	r := api.SetupRouter(v2Engine)
	addr := fmt.Sprintf(":%d", config.C.Server.Port)
	httpSrv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	go func() {
		logger.Info("server starting on " + addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to start server: %v", err)
		}
	}()

	// 监听退出信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Info(fmt.Sprintf("received signal %s, shutting down...", sig))

	// 1. 关闭 HTTP Server（等待正在处理的请求完成）
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		logger.Error("http server shutdown error: " + err.Error())
	} else {
		logger.Info("http server stopped")
	}

	// 2. 关闭 Worker（等待正在处理的任务完成）
	workerSrv.Shutdown()
	logger.Info("worker stopped")

	// 3. 关闭 Scheduler
	scheduler.Shutdown()
	logger.Info("scheduler stopped")

	// 4. 关闭队列客户端和缓存
	queue.Close()
	cache.Close()

	// 5. 关闭数据库
	sqlDB, err := db.DB()
	if err == nil {
		sqlDB.Close()
	}

	logger.Info("server exited gracefully")
}

func startWorker(v2Engine *engine.Engine) *asynq.Server {
	srv := queue.NewServer()
	mux := asynq.NewServeMux()
	worker.RegisterHandlers(mux, v2Engine)

	go func() {
		logger.Info("worker starting...")
		if err := srv.Run(mux); err != nil {
			log.Printf("worker stopped: %v", err)
		}
	}()

	return srv
}

func startScheduler() *asynq.Scheduler {
	cfg := config.C.Redis
	scheduler := asynq.NewScheduler(
		asynq.RedisClientOpt{
			Addr:     cfg.Addr,
			Password: cfg.Password,
			DB:       cfg.DB,
		},
		nil,
	)

	_, err := scheduler.Register("*/5 * * * *", worker.NewTimeoutCheckTask(), asynq.Queue("low"))
	if err != nil {
		log.Fatalf("failed to register timeout check task: %v", err)
	}
	_, err = scheduler.Register("* * * * *", worker.NewResponseRecoveryTask(), asynq.Queue("low"))
	if err != nil {
		log.Fatalf("failed to register response recovery task: %v", err)
	}
	_, err = scheduler.Register("17 * * * *", worker.NewAPICallPayloadCleanupTask(), asynq.Queue("low"))
	if err != nil {
		log.Fatalf("failed to register API call payload cleanup task: %v", err)
	}

	// 每 6 小时同步一次上游模型
	_, err = scheduler.Register("0 */6 * * *", worker.NewModelDiscoverySyncTask(), asynq.Queue("low"))
	if err != nil {
		log.Fatalf("failed to register model discovery sync task: %v", err)
	}

	go func() {
		logger.Info("scheduler starting...")
		if err := scheduler.Run(); err != nil {
			log.Printf("scheduler stopped: %v", err)
		}
	}()

	return scheduler
}
