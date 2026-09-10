package main

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	deploymentproof "github.com/mirainya/Prism/internal/gateway/deployment"
	"github.com/mirainya/Prism/internal/gateway/repository"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/pkg/logger"
)

// startDeploymentReadinessReporter refreshes only a proof already owned by
// this exact executable. Registration and first proof remain explicit admin
// actions, so starting a different binary cannot adopt an active generation.
func startDeploymentReadinessReporter(db *sql.DB) (func(), error) {
	identity, err := gatewayruntime.CurrentProcessIdentity()
	if err != nil {
		return nil, err
	}
	store, err := repository.New(db)
	if err != nil {
		return nil, err
	}
	prover, err := deploymentproof.NewProver(store, identity)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, initialErr := prover.RefreshActive(ctx)
	if errors.Is(initialErr, repository.ErrNotFound) {
		initialErr = nil
	} else if initialErr != nil {
		logger.Warn("deployment readiness refresh failed: " + initialErr.Error())
	}
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(deploymentproof.ProofTTL / 3)
		defer ticker.Stop()
		failed := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, err := prover.RefreshActive(ctx)
				if err != nil && !errors.Is(err, repository.ErrNotFound) {
					if !failed {
						logger.Warn("deployment readiness refresh failed: " + err.Error())
					}
					failed = true
					continue
				}
				if failed {
					logger.Info("deployment readiness refresh recovered")
				}
				failed = false
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			workers.Wait()
		})
	}, initialErr
}
