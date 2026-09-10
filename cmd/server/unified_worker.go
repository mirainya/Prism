package main

import (
	"context"
	"errors"
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
	runtimeReadinessInterval = 2 * time.Second
	runtimeReadinessTimeout  = 3 * time.Second
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
	credentialKEK, kekErr := migrationKey("PRISM_GATEWAY_KEK_B64")
	credentialHMAC, hmacErr := migrationKey("PRISM_GATEWAY_HMAC_B64")
	if kekErr != nil || hmacErr != nil {
		clear(credentialKEK)
		clear(credentialHMAC)
		if activeSources == 0 {
			return func() {}, nil
		}
		return nil, fmt.Errorf("catalog discovery keys unavailable: %w", errors.Join(kekErr, hmacErr))
	}
	defer clear(credentialKEK)
	defer clear(credentialHMAC)
	store, err := repository.New(db)
	if err != nil {
		return nil, err
	}
	worker, err := catalogsource.NewWorker(store, nil, catalogsource.WorkerKeys{CredentialKEK: credentialKEK, CredentialHMAC: credentialHMAC})
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
	var keys gatewayruntime.AsyncKeys
	for _, config := range []struct {
		name string
		key  *[]byte
	}{
		{"PRISM_GATEWAY_KEK_B64", &keys.CredentialKEK},
		{"PRISM_GATEWAY_HMAC_B64", &keys.CredentialHMAC},
		{"PRISM_GATEWAY_PAYLOAD_KEK_B64", &keys.PayloadKEK},
		{"PRISM_GATEWAY_PAYLOAD_HMAC_B64", &keys.PayloadHMAC},
	} {
		*config.key, err = migrationKey(config.name)
		if err != nil {
			readiness.Disable()
			return nil, err
		}
		defer clear(*config.key)
	}
	store, err := repository.New(db)
	if err != nil {
		return nil, err
	}
	identity, err := gatewayruntime.CurrentProcessIdentity()
	if err != nil {
		readiness.Disable()
		return nil, err
	}
	service, err := gatewayruntime.NewWithDeploymentIdentity(store, readiness.Require, identity)
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
	dispatcher, err := gatewayruntime.NewAsyncDispatcher(service, nil, keys, adapter.VideoAsyncCodecs())
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
			probeCtx, probeCancel := context.WithTimeout(ctx, runtimeReadinessTimeout)
			defer probeCancel()
			return gatewayruntime.RequireConfiguredReadiness(probeCtx, db)
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
