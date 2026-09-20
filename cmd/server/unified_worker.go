package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/catalogsource"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/repository"
	responsepipeline "github.com/mirainya/Prism/internal/gateway/responses"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/logger"
	"gorm.io/gorm"
)

const (
	runtimeReadinessInterval = 10 * time.Second
	runtimeReadinessTimeout  = 10 * time.Second
	runtimeReadinessAttempts = 3
)

func startCatalogSourceWorker(orm *gorm.DB) (func(), error) {
	db, err := orm.DB()
	if err != nil {
		return nil, err
	}
	var activeSources uint64
	if err := db.QueryRow(`SELECT COUNT(*) FROM gw_catalog_sources WHERE status IN ('active','draining') OR nonterminal_run_count>0`).Scan(&activeSources); err != nil {
		return nil, err
	}
	if activeSources == 0 {
		return func() {}, nil
	}
	evidenceHMAC, err := migrationKey("PRISM_GATEWAY_PAYLOAD_HMAC_B64")
	if err != nil {
		return nil, fmt.Errorf("catalog discovery evidence key unavailable: %w", err)
	}
	defer clear(evidenceHMAC)
	credentialKEK, credentialHMAC, err := legacyCredentialKeys(context.Background(), db)
	if err != nil {
		return nil, fmt.Errorf("catalog discovery credential keys unavailable: %w", err)
	}
	defer clear(credentialKEK)
	defer clear(credentialHMAC)
	store, err := repository.New(db)
	if err != nil {
		return nil, err
	}
	worker, err := catalogsource.NewWorker(store, nil, catalogsource.WorkerKeys{
		CredentialKEK: credentialKEK, CredentialHMAC: credentialHMAC, EvidenceHMAC: evidenceHMAC,
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	workers.Add(1)
	owner := fmt.Sprintf("catalog-%d-%d", os.Getpid(), time.Now().UnixNano())
	go func() {
		defer workers.Done()
		_ = worker.Run(ctx, owner, func(err error) {
			logger.Error("catalog discovery worker: " + err.Error())
		})
	}()
	// Availability polling shares this worker's credentials and lifetime but not
	// its loop: it refreshes a volatile observation that never enters a catalog
	// release, so a provider outage there must not stall catalog discovery.
	workers.Add(1)
	go func() {
		defer workers.Done()
		_ = worker.RunAvailability(ctx, func(err error) {
			logger.Error("upstream availability worker: " + err.Error())
		})
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			workers.Wait()
			worker.Close()
		})
	}, nil
}

func startUnifiedWorker(orm *gorm.DB, executionEngine *engine.Engine, readiness *gatewayruntime.ReadinessGate) (func(), error) {
	if readiness == nil || !readiness.Ready() {
		return nil, gatewayruntime.ErrNotReady
	}
	db, err := orm.DB()
	if err != nil {
		return nil, err
	}
	var releases uint64
	if err := db.QueryRow(`SELECT COUNT(*) FROM gw_catalog_releases`).Scan(&releases); err != nil {
		return nil, err
	}
	if releases == 0 {
		readiness.Disable()
		return nil, gatewayruntime.ErrNotReady
	}
	keys, err := unifiedWorkerKeys(context.Background(), db)
	if err != nil {
		readiness.Disable()
		return nil, err
	}
	defer clearAsyncKeys(&keys)
	store, err := repository.New(db)
	if err != nil {
		return nil, err
	}
	service, err := gatewayruntime.NewWithReadiness(store, readiness.Require)
	if err != nil {
		readiness.Disable()
		return nil, err
	}
	if err := service.ConfigureCallbackDelivery(gatewayruntime.CallbackDeliveryKeys{
		PayloadKEK: keys.PayloadKEK, PayloadHMAC: keys.PayloadHMAC,
	}); err != nil {
		readiness.Disable()
		service.Close()
		return nil, err
	}
	callbackDeliveryWorker, err := gatewayruntime.NewCallbackDeliveryWorker(service)
	if err != nil {
		readiness.Disable()
		service.Close()
		return nil, err
	}
	dispatcher, err := gatewayruntime.NewAsyncDispatcher(service, nil, keys, adapter.AsyncCodecs())
	if err != nil {
		readiness.Disable()
		service.Close()
		return nil, err
	}
	responseDispatcher, err := responsepipeline.NewBackgroundDispatcher(service, executionEngine, keys)
	if err != nil {
		readiness.Disable()
		service.Close()
		return nil, err
	}
	if err := gatewayruntime.RequireConfiguredReadiness(context.Background(), db); err != nil {
		readiness.Disable()
		service.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	instance := fmt.Sprintf("unified-%d-%d", os.Getpid(), time.Now().UnixNano())
	startWorker := func(name string, run func(context.Context) error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			err := run(ctx)
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				err = fmt.Errorf("%s stopped unexpectedly", name)
			}
			readiness.Disable()
			logger.Error(name + ": " + err.Error())
			cancel()
		}()
	}
	startWorker("unified readiness watchdog", func(ctx context.Context) error {
		return readiness.Watch(ctx, runtimeReadinessInterval, func(ctx context.Context) error {
			return probeUnifiedReadiness(ctx, db)
		})
	})
	for index := range 4 {
		startWorker("unified async worker", func(ctx context.Context) error {
			return service.RunOutbox(ctx, fmt.Sprintf("%s-%d", instance, index), dispatcher, func(err error) {
				logger.Error("unified async worker: " + err.Error())
			})
		})
	}
	for index := range 2 {
		startWorker("unified response worker", func(ctx context.Context) error {
			return service.RunAttemptOutbox(ctx, fmt.Sprintf("%s-response-%d", instance, index), responseDispatcher, func(err error) {
				logger.Error("unified response worker: " + err.Error())
			})
		})
	}
	startWorker("unified callback worker", func(ctx context.Context) error {
		return service.RunCallbackOutboxWithHandler(ctx, fmt.Sprintf("%s-callback", instance), dispatcher, func(err error) {
			logger.Error("unified callback worker: " + err.Error())
		})
	})
	startWorker("unified client callback worker", func(ctx context.Context) error {
		return callbackDeliveryWorker.Run(ctx, fmt.Sprintf("%s-client-callback", instance), func(err error) {
			logger.Error("unified client callback worker: " + err.Error())
		})
	})
	startWorker("unified delivery expiry worker", func(ctx context.Context) error {
		return service.RunDeliveryExpiry(ctx, func(err error) {
			logger.Error("unified delivery expiry worker: " + err.Error())
		})
	})
	startWorker("unified delivery recovery worker", func(ctx context.Context) error {
		return service.RunDeliveryRecovery(ctx, fmt.Sprintf("%s-delivery", instance), dispatcher, func(err error) {
			logger.Error("unified delivery recovery worker: " + err.Error())
		})
	})
	startWorker("unified capability recovery", func(ctx context.Context) error {
		return service.RunCapabilityRecovery(ctx, gatewayruntime.CapabilityRecoveryPolicy{}, func(err error) {
			logger.Error("unified capability recovery: " + err.Error())
		})
	})
	startWorker("unified sensitive payload retention", func(ctx context.Context) error {
		return service.RunSensitiveRetention(ctx, gatewayruntime.SensitiveRetentionPolicy{
			RequestLogAge: config.APICallPayloadRetentionDuration(), Interval: time.Hour, BatchSize: 500,
		}, func(err error) {
			logger.Error("unified sensitive payload retention: " + err.Error())
		})
	})
	startWorker("unified media asset cleanup", func(ctx context.Context) error {
		return video.NewUnifiedAssetService(orm).RunCleanup(ctx, video.UnifiedAssetCleanupPolicy{
			Interval: time.Hour, BatchSize: 500,
		}, func(err error) {
			logger.Error("unified media asset cleanup: " + err.Error())
		})
	})
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			workers.Wait()
			service.Close()
		})
	}, nil
}

func probeUnifiedReadiness(ctx context.Context, db *sql.DB) error {
	var lastErr error
	for attempt := 0; attempt < runtimeReadinessAttempts; attempt++ {
		probeCtx, cancel := context.WithTimeout(ctx, runtimeReadinessTimeout)
		lastErr = gatewayruntime.RequireConfiguredReadiness(probeCtx, db)
		cancel()
		if lastErr == nil || ctx.Err() != nil {
			return lastErr
		}
	}
	return lastErr
}

func legacyCredentialKeys(ctx context.Context, db repository.ReadinessQuery) ([]byte, []byte, error) {
	required, err := repository.LegacyCredentialCryptoRequired(ctx, db)
	if err != nil || !required {
		return nil, nil, err
	}
	kek, err := migrationKey("PRISM_GATEWAY_KEK_B64")
	if err != nil {
		return nil, nil, err
	}
	hmacKey, err := migrationKey("PRISM_GATEWAY_HMAC_B64")
	if err != nil {
		clear(kek)
		return nil, nil, err
	}
	return kek, hmacKey, nil
}

func unifiedWorkerKeys(ctx context.Context, db repository.ReadinessQuery) (gatewayruntime.AsyncKeys, error) {
	var keys gatewayruntime.AsyncKeys
	var err error
	keys.PayloadKEK, err = migrationKey("PRISM_GATEWAY_PAYLOAD_KEK_B64")
	if err != nil {
		return keys, err
	}
	keys.PayloadHMAC, err = migrationKey("PRISM_GATEWAY_PAYLOAD_HMAC_B64")
	if err != nil {
		clearAsyncKeys(&keys)
		return keys, err
	}
	keys.CredentialKEK, keys.CredentialHMAC, err = legacyCredentialKeys(ctx, db)
	if err != nil {
		clearAsyncKeys(&keys)
		return keys, err
	}
	return keys, nil
}

func clearAsyncKeys(keys *gatewayruntime.AsyncKeys) {
	if keys == nil {
		return
	}
	clear(keys.CredentialKEK)
	clear(keys.CredentialHMAC)
	clear(keys.PayloadKEK)
	clear(keys.PayloadHMAC)
}
