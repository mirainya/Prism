//go:build integration

package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/credentials"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/tokenauth"
	"gorm.io/gorm"
)

func verifyGatewaySlotAdmission(t *testing.T, orm *gorm.DB) {
	t.Helper()
	db, _ := orm.DB()
	db.SetMaxOpenConns(16)
	defer db.SetMaxOpenConns(1)
	ctx := context.Background()
	store, _ := repository.New(db)
	runtime, _ := gatewayruntime.New(store)
	user := model.User{Username: "qa-slot-owner", Password: "not-a-login-hash"}
	if err := orm.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	_, selector, secretDigest, err := tokenauth.Generate()
	if err != nil {
		t.Fatal(err)
	}
	token := model.Token{UserID: user.ID, Name: "qa-slots", Selector: selector, SecretDigest: secretDigest, SecretDigestVersion: tokenauth.DigestVersion, AuthVersion: 1, Status: 1}
	if err := orm.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	currencyCode := "CNY"
	currencyVersion := uint32(1)
	if err := db.QueryRowContext(ctx, `SELECT currency_code,currency_version FROM billing_system_state WHERE id=1`).Scan(&currencyCode, &currencyVersion); errors.Is(err, sql.ErrNoRows) {
		if err := store.WithTx(ctx, func(tx *sql.Tx) error {
			return store.InitializeBilling(ctx, tx, billing.Currency{Code: currencyCode, Version: currencyVersion, FractionDigits: 8, RoundingMode: "half_even", MaxAmount: "1000000"})
		}); err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	var attempt repository.BeginAttemptInput
	call := repository.CreateCallInput{UserID: uint64(user.ID), TokenID: uint64(token.ID), Currency: currencyCode, CurrencyVersion: currencyVersion, DeliveryMode: "reference"}
	if err := db.QueryRow(`SELECT r.release_id,r.sku_id,r.id,r.offering_id,p.id,o.product_transport_id,o.credential_pool_id,c.id,c.current_version_id,g.id,s.model_operation_id,m.operation_contract_id
FROM gw_routes r JOIN gw_skus s ON s.id=r.sku_id JOIN gw_model_operations m ON m.id=s.model_operation_id
JOIN gw_offerings o ON o.id=r.offering_id JOIN gw_credentials c ON c.credential_pool_id=o.credential_pool_id
JOIN gw_cost_plans p ON p.release_id=o.release_id AND p.offering_id=o.id AND p.plan_code=o.cost_plan_code
JOIN gw_credential_purpose_grants g ON g.credential_id=c.id AND g.purpose='execution' AND g.status='active'
ORDER BY r.id,c.id LIMIT 1`).Scan(&attempt.CatalogReleaseID, &attempt.SKUID, &attempt.RouteID, &attempt.OfferingID, &attempt.CostPlanID, &attempt.ProductTransportID, &attempt.CredentialPoolID, &attempt.CredentialID, &attempt.CredentialVersionID, &attempt.PurposeGrantID, &call.ModelOperationID, &call.OperationContractID); err != nil {
		t.Fatal(err)
	}
	call.CatalogReleaseID, call.SKUID = attempt.CatalogReleaseID, attempt.SKUID
	second := attempt
	if err := store.WithTx(ctx, func(tx *sql.Tx) error {
		id, err := store.CreateManagedCredential(ctx, tx, attempt.CredentialPoolID, repository.ManagedCredentialInput{Code: "qa-slot-key", Weight: 1, Secret: []byte("qa-slot-second-secret"), Purposes: []credentials.Purpose{credentials.PurposeExecution}}, bytes.Repeat([]byte{71}, 32), bytes.Repeat([]byte{72}, 32), uint64(user.ID))
		second.CredentialID = id
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT c.current_version_id,g.id FROM gw_credentials c JOIN gw_credential_purpose_grants g ON g.credential_id=c.id AND g.purpose='execution' AND g.status='active' WHERE c.id=?`, second.CredentialID).Scan(&second.CredentialVersionID, &second.PurposeGrantID); err != nil {
		t.Fatal(err)
	}
	var seq int
	newAttempt := func(input repository.BeginAttemptInput) uint64 {
		t.Helper()
		seq++
		call.PublicID = fmt.Sprintf("qa-slot-call-%d", seq)
		var id uint64
		if err := store.WithTx(ctx, func(tx *sql.Tx) error {
			var err error
			input.CallID, err = store.CreateCall(ctx, tx, call)
			if err != nil {
				return err
			}
			id, err = store.BeginAttempt(ctx, tx, input)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	setLimits := func(scope string, poolLimit, keyLimit any) {
		t.Helper()
		if scope != "task" && scope != "request" {
			t.Fatal("invalid fixture scope")
		}
		if _, err := db.Exec("UPDATE gw_credential_pools SET "+scope+"_limit=? WHERE id=?", poolLimit, attempt.CredentialPoolID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("UPDATE gw_credentials SET "+scope+"_limit=? WHERE credential_pool_id=?", keyLimit, attempt.CredentialPoolID); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("slot_pool_and_key_contention", func(t *testing.T) {
		setLimits("task", 3, 2)
		inputs := make([]repository.SlotInput, 16)
		for i := range inputs {
			selected := attempt
			if i%2 != 0 {
				selected = second
			}
			id := newAttempt(selected)
			inputs[i] = repository.SlotInput{CredentialID: selected.CredentialID, CredentialPoolID: selected.CredentialPoolID, Scope: "task", AttemptID: &id}
		}
		var wg sync.WaitGroup
		errorsOut := make(chan error, len(inputs))
		start := make(chan struct{})
		for _, in := range inputs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errorsOut <- store.WithTx(ctx, func(tx *sql.Tx) error { _, err := store.AcquireCredentialSlot(ctx, tx, in); return err })
			}()
		}
		close(start)
		wg.Wait()
		close(errorsOut)
		succeeded := 0
		for err := range errorsOut {
			if err == nil {
				succeeded++
			} else if !errors.Is(err, repository.ErrConcurrencyLimit) {
				t.Fatalf("unexpected admission failure: %v", err)
			}
		}
		if succeeded != 3 {
			t.Fatalf("admitted=%d want=3", succeeded)
		}
		var maximum int
		if err := db.QueryRow(`SELECT MAX(n) FROM (SELECT COUNT(*) n FROM gw_credential_slots WHERE scope='task' AND state='active' GROUP BY credential_id) counts`).Scan(&maximum); err != nil || maximum > 2 {
			t.Fatalf("per-key maximum=%d error=%v", maximum, err)
		}
		for _, in := range inputs {
			if err := store.WithTx(ctx, func(tx *sql.Tx) error {
				if err := store.TransitionAttempt(ctx, tx, *in.AttemptID, execution.AttemptStarted, execution.AttemptCompleted, 1, "qa_provider_completed"); err != nil {
					return err
				}
				return store.FinalizeAttemptSlot(ctx, tx, *in.AttemptID)
			}); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("key_limit_and_stale_snapshot", func(t *testing.T) {
		setLimits("task", nil, 1)
		first, next := newAttempt(attempt), newAttempt(attempt)
		stale, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if err != nil {
			t.Fatal(err)
		}
		defer stale.Rollback()
		var before int
		if err := stale.QueryRow(`SELECT COUNT(*) FROM gw_credential_slots WHERE state IN ('active','recovery_required') AND scope='task'`).Scan(&before); err != nil || before != 0 {
			t.Fatalf("initial snapshot=%d err=%v", before, err)
		}
		if err := store.WithTx(ctx, func(tx *sql.Tx) error {
			_, err := store.AcquireCredentialSlot(ctx, tx, repository.SlotInput{CredentialID: attempt.CredentialID, CredentialPoolID: attempt.CredentialPoolID, Scope: "task", AttemptID: &first})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		_, err = store.AcquireCredentialSlot(ctx, stale, repository.SlotInput{CredentialID: attempt.CredentialID, CredentialPoolID: attempt.CredentialPoolID, Scope: "task", AttemptID: &next})
		if !errors.Is(err, repository.ErrConcurrencyLimit) {
			t.Fatalf("stale snapshot bypassed key limit: %v", err)
		}
		stale.Rollback()
		if err := store.WithTx(ctx, func(tx *sql.Tx) error {
			if err := store.TransitionAttempt(ctx, tx, first, execution.AttemptStarted, execution.AttemptCompleted, 1, "qa_completed"); err != nil {
				return err
			}
			return store.FinalizeAttemptSlot(ctx, tx, first)
		}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("request_capacity_rollback_and_recovery", func(t *testing.T) {
		setLimits("request", 1, 1)
		first, next := newAttempt(attempt), newAttempt(attempt)
		in := repository.RequestLogInput{AttemptID: &first, RequestSeq: 1, Action: "submit", MappingHMAC: strings.Repeat("a", 64)}
		requestID, err := runtime.BeginRequest(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		in.AttemptID = &next
		if _, err := runtime.BeginRequest(ctx, in); !errors.Is(err, repository.ErrConcurrencyLimit) {
			t.Fatalf("full request pool: %v", err)
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM gw_channel_request_logs WHERE attempt_id=?`, next).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed admission left request log: count=%d err=%v", count, err)
		}
		if err := runtime.FinishRequest(ctx, requestID, "unknown", repository.RequestLogResult{ErrorCode: "connection_lost"}); err != nil {
			t.Fatal(err)
		}
		requestID, err = runtime.BeginRequest(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := runtime.FinishRequest(ctx, requestID, "response_recorded", repository.RequestLogResult{RequestComplete: true, ResponseComplete: true}); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("unknown_task_keeps_capacity", func(t *testing.T) {
		setLimits("task", 1, 1)
		first, next := newAttempt(attempt), newAttempt(attempt)
		if err := store.WithTx(ctx, func(tx *sql.Tx) error {
			_, err := store.AcquireCredentialSlot(ctx, tx, repository.SlotInput{CredentialID: attempt.CredentialID, CredentialPoolID: attempt.CredentialPoolID, Scope: "task", AttemptID: &first})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.WithTx(ctx, func(tx *sql.Tx) error {
			if err := store.TransitionAttempt(ctx, tx, first, execution.AttemptStarted, execution.AttemptTerminatedUnknown, 1, "qa_unknown"); err != nil {
				return err
			}
			return store.FinalizeAttemptSlot(ctx, tx, first)
		}); err != nil {
			t.Fatal(err)
		}
		err := store.WithTx(ctx, func(tx *sql.Tx) error {
			_, err := store.AcquireCredentialSlot(ctx, tx, repository.SlotInput{CredentialID: attempt.CredentialID, CredentialPoolID: attempt.CredentialPoolID, Scope: "task", AttemptID: &next})
			return err
		})
		if !errors.Is(err, repository.ErrConcurrencyLimit) {
			t.Fatalf("unknown task released capacity: %v", err)
		}
	})
	verifyAtomicGatewaySubmission(t, db, store, call, attempt)
	verifyUnifiedEngineIsolation(t, db, call, attempt)
	t.Run("disabled_channel_rejects_preselected_attempt_and_send", func(t *testing.T) {
		id := newAttempt(attempt)
		var channelID uint64
		if err := db.QueryRow(`SELECT channel_id FROM gw_credential_pools WHERE id=?`, attempt.CredentialPoolID).Scan(&channelID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE gateway_channels SET status='disabled' WHERE id=?`, channelID); err != nil {
			t.Fatal(err)
		}
		defer db.Exec(`UPDATE gateway_channels SET status='active' WHERE id=?`, channelID)
		_, err := runtime.BeginRequest(ctx, repository.RequestLogInput{AttemptID: &id, RequestSeq: 1, Action: "submit", MappingHMAC: strings.Repeat("a", 64)})
		if !errors.Is(err, repository.ErrConflict) {
			t.Fatalf("disabled channel received send authorization: %v", err)
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM gw_channel_request_logs WHERE attempt_id=?`, id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("denied send left a log: %d %v", count, err)
		}
		call.PublicID = "qa-disabled-channel"
		err = store.WithTx(ctx, func(tx *sql.Tx) error {
			var err error
			attempt.CallID, err = store.CreateCall(ctx, tx, call)
			if err != nil {
				return err
			}
			_, err = store.BeginAttempt(ctx, tx, attempt)
			return err
		})
		if !errors.Is(err, repository.ErrConflict) {
			t.Fatalf("disabled channel accepted an attempt: %v", err)
		}
	})
}
