package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/mirainya/Prism/internal/api"
	"github.com/mirainya/Prism/internal/gateway"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/gateway/security"
	schemamigrate "github.com/mirainya/Prism/internal/migrate"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider"
	"github.com/mirainya/Prism/pkg/cache"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/database"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Printf("Prism %s (%s)\n", Version, BuildTime)
		return
	}

	// 同时支持从源码目录启动和直接运行已安装的二进制，因此配置文件要在
	// 当前工作目录与可执行文件目录中依次查找。
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
	migrationCommand := len(os.Args) > 1 && os.Args[1] == "migrate"

	if err := config.Load(configPath); err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	if !migrationCommand {
		if err := config.ValidateRuntime(); err != nil {
			log.Fatalf("invalid runtime configuration: %v", err)
		}
	}
	config.Watch()

	if err := logger.Init(); err != nil {
		log.Fatalf("failed to init logger: %v", err)
	}

	var db *gorm.DB
	if migrationCommand {
		db, err = database.ConnectForMigrations()
	} else {
		db, err = database.Connect()
	}
	if err != nil {
		log.Fatalf("failed to connect database: %v", err)
	}
	model.SetDB(db)
	if migrationCommand {
		if err := runMigrationCommand(context.Background(), db, os.Args[2:]); err != nil {
			log.Fatalf("migration command failed: %v", err)
		}
		closeDatabase(db)
		return
	}
	if err := schemamigrate.EnsureCurrent(context.Background(), db); err != nil {
		log.Fatalf("database schema is not current: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("failed to open runtime readiness database: %v", err)
	}
	runtimeErr := gatewayruntime.RequireConfiguredReadiness(context.Background(), sqlDB)
	runtimeReady := runtimeErr == nil
	dataPlaneGate := gatewayruntime.NewReadinessGate(runtimeReady)
	if !runtimeReady {
		logger.Warn("unified gateway data plane disabled; control plane remains available", zap.Error(runtimeErr))
	}

	if err := cache.Init(); err != nil {
		log.Fatalf("failed to init cache: %v", err)
	}
	// 基础设施必须先于网关和 Worker 初始化；后两者会立即使用这些全局连接。
	provider.InitHTTPClient()
	v2Engine, err := gateway.NewV2Engine()
	if err != nil {
		log.Fatalf("failed to initialize Gateway V2: %v", err)
	}
	stopCatalogWorker, err := startCatalogSourceWorker(db)
	if err != nil {
		log.Fatalf("failed to initialize catalog discovery worker: %v", err)
	}
	defer stopCatalogWorker()
	stopUnifiedWorker := func() {}
	if runtimeReady {
		stopUnifiedWorker, err = startUnifiedWorker(db, v2Engine, dataPlaneGate)
		if err != nil {
			log.Fatalf("failed to initialize unified async worker: %v", err)
		}
	}
	defer stopUnifiedWorker()

	// 设置路由并启动 HTTP 服务
	r := api.SetupRouter(v2Engine, dataPlaneGate.Require)
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

	// 先停止接收 HTTP 请求，再等待后台任务退出，避免关闭共享连接时仍有任务使用它们。
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
	stopUnifiedWorker()
	stopCatalogWorker()
	logger.Info("worker stopped")

	// 3. 关闭缓存
	cache.Close()

	// 4. 关闭数据库
	closeDatabase(db)

	logger.Info("server exited gracefully")
}

func runMigrationCommand(ctx context.Context, db *gorm.DB, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: prism migrate <up|status|adopt|audit|audit-deep|import-legacy|import-runtime|import-video-assets|import-ai-files|verify-crypto|cleanup-legacy>")
	}
	switch args[0] {
	case "up":
		applied, err := schemamigrate.Up(ctx, db)
		if err != nil {
			return err
		}
		if len(applied) == 0 {
			fmt.Println("database schema is current")
			return nil
		}
		for _, migration := range applied {
			fmt.Printf("applied %s\n", migration.Filename)
		}
		return nil
	case "status":
		status, err := schemamigrate.Inspect(ctx, db)
		if err != nil {
			return err
		}
		if status.Legacy {
			fmt.Printf("legacy database: apply SQL through 20260718_120000 and run `prism migrate adopt`\n")
		}
		for _, migration := range status.Applied {
			fmt.Printf("applied %s %s\n", migration.Version, migration.Name)
		}
		for _, migration := range status.Pending {
			fmt.Printf("pending %s\n", migration.Filename)
		}
		return nil
	case "adopt":
		baseline, err := schemamigrate.Adopt(ctx, db)
		if err != nil {
			return err
		}
		fmt.Printf("adopted schema baseline %s\n", baseline.Version)
		return nil
	case "audit":
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		report, err := schemamigrate.Audit(ctx, sqlDB)
		if err != nil {
			return err
		}
		fmt.Printf("legacy_channels=%d legacy_abilities=%d target_channels=%d target_models=%d target_credentials=%d target_releases=%d target_offerings=%d target_routes=%d sell_rates=%d cost_rates=%d currencies=%d target_calls=%d active_release_id=%s deployment_status=%s ready_for_cutover=%t\n",
			report.LegacyChannels,
			report.LegacyAbilities,
			report.TargetChannels,
			report.TargetModels,
			report.TargetCredentials,
			report.TargetReleases,
			report.TargetOfferings,
			report.TargetRoutes,
			report.SellRates,
			report.CostRates,
			report.Currencies,
			report.TargetCalls,
			nullInt64String(report.ActiveReleaseID),
			report.DeploymentStatus,
			report.ReadyForCutover(),
		)
		return nil
	case "audit-deep":
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		report, err := schemamigrate.DeepAudit(ctx, sqlDB)
		if err != nil {
			return err
		}
		fmt.Printf("missing_target_tables=%v missing_audit_tables=%v legacy_tables=%v unverified_history=%v unmapped_channels=%d unmapped_keys=%d unmapped_abilities=%d open_issues=%d migration_runs=%d succeeded_runs=%d ready_for_cleanup=%t\n", report.MissingTargetTables, report.MissingAuditTables, report.LegacyTablesPresent, report.UnverifiedLegacyHistory, report.UnmappedLegacyChannels, report.UnmappedLegacyKeys, report.UnmappedLegacyAbilities, report.OpenMigrationIssues, report.MigrationRunCount, report.SucceededMigrationRuns, report.ReadyForCleanup())
		return nil
	case "import-legacy":
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		kek, err := migrationKey("PRISM_GATEWAY_KEK_B64")
		if err != nil {
			return err
		}
		hmacKey, err := migrationKey("PRISM_GATEWAY_HMAC_B64")
		if err != nil {
			return err
		}
		report, err := schemamigrate.ImportLegacyGateway(ctx, sqlDB, schemamigrate.ImportOptions{KEK: kek, HMACKey: hmacKey})
		if err != nil {
			return err
		}
		fmt.Printf("imported channels=%d credentials=%d models=%d abilities=%d release_id=%d\n", report.Channels, report.Credentials, report.Models, report.Abilities, report.ReleaseID)
		return nil
	case "import-runtime":
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		hmacKey, err := migrationKey("PRISM_GATEWAY_HMAC_B64")
		if err != nil {
			return err
		}
		report, importErr := schemamigrate.ImportLegacyRuntime(ctx, sqlDB, schemamigrate.ImportOptions{HMACKey: hmacKey})
		fmt.Printf("legacy runtime import: run_id=%d source_rows=%d billing_accounts=%d budget_windows=%d calls=%d attempts=%d payloads=%d request_logs=%d capability_tasks=%d video_tasks=%d responses=%d async_executions=%d billing_evidence=%d balance_evidence=%d skipped=%d issues=%d source_revision_hmac=%s\n",
			report.RunID, report.SourceRows, report.BillingAccounts, report.BudgetWindows, report.Calls, report.Attempts, report.Payloads, report.RequestLogs,
			report.CapabilityTasks, report.VideoTasks, report.Responses, report.AsyncExecutions,
			report.BillingEvidence, report.BalanceEvidence, report.Skipped, report.Issues, report.SourceRevisionHMAC)
		return importErr
	case "import-video-assets":
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		hmacKey, err := migrationKey("PRISM_GATEWAY_HMAC_B64")
		if err != nil {
			return err
		}
		report, importErr := schemamigrate.ImportLegacyVideoAssets(ctx, sqlDB, schemamigrate.ImportOptions{HMACKey: hmacKey})
		fmt.Printf("imported legacy video assets: imported=%d skipped=%d issues=%d run_id=%d\n", report.Imported, report.Skipped, report.Issues, report.RunID)
		return importErr
	case "import-ai-files":
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		hmacKey, err := migrationKey("PRISM_GATEWAY_HMAC_B64")
		if err != nil {
			return err
		}
		report, importErr := schemamigrate.ImportLegacyAIFiles(ctx, sqlDB, schemamigrate.ImportOptions{HMACKey: hmacKey})
		fmt.Printf("imported legacy AI files: imported=%d skipped=%d issues=%d run_id=%d\n", report.Imported, report.Skipped, report.Issues, report.RunID)
		return importErr
	case "verify-crypto":
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		kek, err := migrationKey("PRISM_GATEWAY_KEK_B64")
		if err != nil {
			return err
		}
		if err := schemamigrate.VerifyEncryptedCredentials(ctx, sqlDB, kek); err != nil {
			return err
		}
		fmt.Println("encrypted credential verification passed")
		return nil
	case "cleanup-legacy":
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		if err := schemamigrate.CleanupLegacyGateway(ctx, sqlDB); err != nil {
			return err
		}
		fmt.Println("legacy gateway configuration tables removed")
		return nil
	default:
		return fmt.Errorf("unknown migration command %q; use up, status, adopt, audit, audit-deep, import-legacy, import-runtime, import-video-assets, import-ai-files, verify-crypto, or cleanup-legacy", args[0])
	}
}

func migrationKey(name string) ([]byte, error) {
	value := os.Getenv(name)
	if value == "" {
		return nil, fmt.Errorf("%s is required", name)
	}
	decoded, err := security.DecodeBase64Key(value)
	if err != nil {
		return nil, fmt.Errorf("%s must be base64-encoded 32 bytes", name)
	}
	return decoded, nil
}

func nullInt64String(value sql.NullInt64) string {
	if !value.Valid {
		return "null"
	}
	return strconv.FormatInt(value.Int64, 10)
}

func closeDatabase(db *gorm.DB) {
	sqlDB, err := db.DB()
	if err == nil {
		_ = sqlDB.Close()
	}
}
