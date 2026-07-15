package responses

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestResponseIdempotencyConcurrentRequestWaitsForCachedBody(t *testing.T) {
	setupResponseIdempotencyDB(t)
	requestJSON := []byte(`{"model":"m","input":"hello","store":false}`)
	claim, replay, err := acquireResponseIdempotency(context.Background(), 11, "same-key", requestJSON)
	if err != nil || claim == nil || replay != nil {
		t.Fatalf("initial claim=%#v replay=%#v err=%v", claim, replay, err)
	}
	record := model.AIResponse{
		ID: "resp_cached", UserID: 7, TokenID: 11, Model: "m", Status: "completed",
		Store: false, IdempotencyKey: "internal:resp_cached", CreatedAt: time.Now(),
	}
	if err := model.DB().Create(&record).Error; err != nil {
		t.Fatal(err)
	}

	type acquireResult struct {
		claim  *responseIdempotencyClaim
		replay *Result
		err    error
	}
	resultCh := make(chan acquireResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		secondClaim, secondReplay, acquireErr := acquireResponseIdempotency(ctx, 11, "same-key", requestJSON)
		resultCh <- acquireResult{claim: secondClaim, replay: secondReplay, err: acquireErr}
	}()

	select {
	case result := <-resultCh:
		t.Fatalf("concurrent request returned before completion: %#v", result)
	case <-time.After(75 * time.Millisecond):
	}

	response := &protocol.Response{
		ID: "resp_cached", Object: "response", Status: "completed", Model: "m",
		Store: false, Output: json.RawMessage(`[{"type":"message","content":[{"type":"output_text","text":"done"}]}]`),
	}
	if err := completeResponseIdempotency(claim, record.ID, response); err != nil {
		t.Fatal(err)
	}

	result := <-resultCh
	if result.err != nil || result.claim != nil || result.replay == nil || result.replay.Response == nil {
		t.Fatalf("concurrent replay=%#v err=%v", result, result.err)
	}
	if string(result.replay.Response.Output) != string(response.Output) {
		t.Fatalf("cached output=%s", result.replay.Response.Output)
	}
}

func TestResponseIdempotencyLeaseRenewsUntilTerminalResult(t *testing.T) {
	setupResponseIdempotencyDB(t)
	requestJSON := []byte(`{"model":"m","input":"long"}`)
	options := responseIdempotencyLeaseOptions{duration: 200 * time.Millisecond, heartbeat: 30 * time.Millisecond}
	claim, replay, err := acquireResponseIdempotencyWithOptions(context.Background(), 21, "renewed-key", requestJSON, options)
	if err != nil || claim == nil || replay != nil {
		t.Fatalf("initial claim=%#v replay=%#v err=%v", claim, replay, err)
	}
	record := model.AIResponse{
		ID: "resp_renewed", UserID: 7, TokenID: 21, Model: "m", Status: "completed",
		Store: false, IdempotencyKey: "internal:resp_renewed", CreatedAt: time.Now(),
	}
	if err := model.DB().Create(&record).Error; err != nil {
		t.Fatal(err)
	}

	type acquireResult struct {
		claim  *responseIdempotencyClaim
		replay *Result
		err    error
	}
	resultCh := make(chan acquireResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		secondClaim, secondReplay, acquireErr := acquireResponseIdempotencyWithOptions(ctx, 21, "renewed-key", requestJSON, options)
		resultCh <- acquireResult{claim: secondClaim, replay: secondReplay, err: acquireErr}
	}()

	select {
	case result := <-resultCh:
		t.Fatalf("active lease was replaced: %#v", result)
	case <-time.After(500 * time.Millisecond):
	}
	response := &protocol.Response{ID: record.ID, Object: "response", Status: "completed", Model: "m", Output: json.RawMessage(`[]`)}
	completedAt := time.Now()
	if err := completeResponseIdempotency(claim, record.ID, response); err != nil {
		t.Fatal(err)
	}

	select {
	case result := <-resultCh:
		if result.err != nil || result.claim != nil || result.replay == nil || result.replay.Response == nil {
			t.Fatalf("terminal replay=%#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("waiting request did not receive the terminal replay")
	}
	var cached model.AIResponseIdempotencyCache
	if err := model.DB().First(&cached, "token_id = ? AND idempotency_key = ?", 21, "renewed-key").Error; err != nil {
		t.Fatal(err)
	}
	if cached.Status != model.ResponseIdempotencyCompleted || cached.Owner != "" || cached.ExpiresAt.Before(completedAt.Add(23*time.Hour)) {
		t.Fatalf("terminal cache=%#v", cached)
	}
}

func TestResponseIdempotencyLeaseCanBeTakenOverAfterRenewalStops(t *testing.T) {
	setupResponseIdempotencyDB(t)
	requestJSON := []byte(`{"model":"m","input":"takeover"}`)
	options := responseIdempotencyLeaseOptions{duration: 180 * time.Millisecond, heartbeat: 30 * time.Millisecond}
	first, replay, err := acquireResponseIdempotencyWithOptions(context.Background(), 22, "takeover-key", requestJSON, options)
	if err != nil || first == nil || replay != nil {
		t.Fatalf("initial claim=%#v replay=%#v err=%v", first, replay, err)
	}
	first.stopRenewal()

	type acquireResult struct {
		claim  *responseIdempotencyClaim
		replay *Result
		err    error
	}
	resultCh := make(chan acquireResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		claim, cached, acquireErr := acquireResponseIdempotencyWithOptions(ctx, 22, "takeover-key", requestJSON, options)
		resultCh <- acquireResult{claim: claim, replay: cached, err: acquireErr}
	}()

	select {
	case result := <-resultCh:
		t.Fatalf("unexpired lease was replaced: %#v", result)
	case <-time.After(60 * time.Millisecond):
	}
	var second *responseIdempotencyClaim
	select {
	case result := <-resultCh:
		if result.err != nil || result.claim == nil || result.replay != nil {
			t.Fatalf("takeover result=%#v", result)
		}
		second = result.claim
	case <-time.After(time.Second):
		t.Fatal("expired lease was not taken over")
	}
	defer func() { _ = releaseResponseIdempotency(second) }()
	if second.owner == first.owner {
		t.Fatal("takeover reused the stale owner")
	}
	response := &protocol.Response{ID: "resp_stale", Object: "response", Status: "completed", Model: "m", Output: json.RawMessage(`[]`)}
	if err := completeResponseIdempotency(first, response.ID, response); !errors.Is(err, errResponseIdempotencyLeaseLost) {
		t.Fatalf("stale owner completion error=%v", err)
	}
}

func TestResponseIdempotencyEncodingFailureStopsRenewal(t *testing.T) {
	setupResponseIdempotencyDB(t)
	options := responseIdempotencyLeaseOptions{duration: 200 * time.Millisecond, heartbeat: 30 * time.Millisecond}
	claim, replay, err := acquireResponseIdempotencyWithOptions(
		context.Background(), 23, "encoding-key", []byte(`{"input":"encoding"}`), options,
	)
	if err != nil || claim == nil || replay != nil {
		t.Fatalf("claim=%#v replay=%#v err=%v", claim, replay, err)
	}
	response := &protocol.Response{
		ID: "resp_encoding", Object: "response", Status: "completed", Model: "m",
		Output: json.RawMessage(`{"invalid"`),
	}
	if err := completeResponseIdempotency(claim, response.ID, response); err == nil {
		t.Fatal("invalid response encoding unexpectedly succeeded")
	}
	select {
	case <-claim.done:
	default:
		t.Fatal("encoding failure left the lease renewal running")
	}
	if err := releaseResponseIdempotency(claim); err != nil {
		t.Fatal(err)
	}
}

func TestResponseIdempotencyTerminalWriteFailureStopsRenewal(t *testing.T) {
	setupResponseIdempotencyDB(t)
	options := responseIdempotencyLeaseOptions{duration: 200 * time.Millisecond, heartbeat: 30 * time.Millisecond}
	claim, replay, err := acquireResponseIdempotencyWithOptions(
		context.Background(), 24, "write-failure-key", []byte(`{"input":"write"}`), options,
	)
	if err != nil || claim == nil || replay != nil {
		t.Fatalf("claim=%#v replay=%#v err=%v", claim, replay, err)
	}
	if err := model.DB().Model(&model.AIResponseIdempotencyCache{}).
		Where("token_id = ? AND idempotency_key = ?", 24, "write-failure-key").
		Update("owner", "replacement-owner").Error; err != nil {
		t.Fatal(err)
	}
	response := &protocol.Response{ID: "resp_write", Object: "response", Status: "completed", Model: "m", Output: json.RawMessage(`[]`)}
	if err := completeResponseIdempotency(claim, response.ID, response); !errors.Is(err, errResponseIdempotencyLeaseLost) {
		t.Fatalf("terminal write error=%v", err)
	}
	select {
	case <-claim.done:
	default:
		t.Fatal("terminal write failure left the lease renewal running")
	}
}

func TestResponseIdempotencyRejectsPendingHashConflict(t *testing.T) {
	setupResponseIdempotencyDB(t)
	claim, replay, err := acquireResponseIdempotency(context.Background(), 12, "conflict-key", []byte(`{"input":"one"}`))
	if err != nil || claim == nil || replay != nil {
		t.Fatalf("initial claim=%#v replay=%#v err=%v", claim, replay, err)
	}
	defer func() { _ = releaseResponseIdempotency(claim) }()

	_, _, err = acquireResponseIdempotency(context.Background(), 12, "conflict-key", []byte(`{"input":"two"}`))
	if err == nil || !strings.Contains(err.Error(), "different request") {
		t.Fatalf("conflict error=%v", err)
	}
}

func TestResponseIdempotencyExpiredKeyCanBeReused(t *testing.T) {
	setupResponseIdempotencyDB(t)
	old := model.AIResponseIdempotencyCache{
		TokenID: 13, IdempotencyKey: "expired-key", RequestHash: hashResponseRequest([]byte(`{"input":"old"}`)),
		Status: model.ResponseIdempotencyCompleted, ResponseID: "resp_old",
		ResponseJSON: datatypes.JSON(`{"id":"resp_old","status":"completed"}`),
		ExpiresAt:    time.Now().Add(-time.Minute),
	}
	if err := model.DB().Create(&old).Error; err != nil {
		t.Fatal(err)
	}

	newRequest := []byte(`{"input":"new"}`)
	claim, replay, err := acquireResponseIdempotency(context.Background(), 13, "expired-key", newRequest)
	if err != nil || claim == nil || replay != nil {
		t.Fatalf("replacement claim=%#v replay=%#v err=%v", claim, replay, err)
	}
	defer func() { _ = releaseResponseIdempotency(claim) }()

	var replaced model.AIResponseIdempotencyCache
	if err := model.DB().First(&replaced, "token_id = ? AND idempotency_key = ?", 13, "expired-key").Error; err != nil {
		t.Fatal(err)
	}
	if replaced.RequestHash != hashResponseRequest(newRequest) || replaced.Status != model.ResponseIdempotencyPending || replaced.Owner == "" || len(replaced.ResponseJSON) != 0 {
		t.Fatalf("expired entry was not replaced: %#v", replaced)
	}
}

func TestStoredResponseIdempotencyKeyExpiresWithoutDeletingResponse(t *testing.T) {
	setupResponseIdempotencyDB(t)
	requestJSON := []byte(`{"model":"m","input":"hello","store":true}`)
	record := model.AIResponse{
		ID: "resp_stored_expired", UserID: 7, TokenID: 14, Model: "m", Status: "completed",
		Store: true, IdempotencyKey: "stored-key", RequestHash: hashResponseRequest(requestJSON),
		CreatedAt: time.Now().Add(-responseIdempotencyResultTTL - time.Minute),
	}
	if err := model.DB().Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	replay, err := findIdempotentResponse(context.Background(), record.TokenID, "stored-key", requestJSON)
	if err != nil || replay != nil {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	var stored model.AIResponse
	if err := model.DB().First(&stored, "id = ?", record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.IdempotencyKey != "internal:"+record.ID || !stored.Store {
		t.Fatalf("stored response was not detached safely: %#v", stored)
	}
}

func TestStoredResponseIdempotencyWaitsForForegroundTerminalState(t *testing.T) {
	setupResponseIdempotencyDB(t)
	requestJSON := []byte(`{"model":"m","input":"hello","store":true}`)
	record := model.AIResponse{
		ID: "resp_stored_pending", UserID: 7, TokenID: 15, Model: "m", Status: "in_progress",
		Store: true, IdempotencyKey: "stored-pending", RequestHash: hashResponseRequest(requestJSON),
		CreatedAt: time.Now(),
	}
	if err := model.DB().Create(&record).Error; err != nil {
		t.Fatal(err)
	}

	type lookupResult struct {
		result *Result
		err    error
	}
	resultCh := make(chan lookupResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		result, err := findIdempotentResponse(ctx, record.TokenID, record.IdempotencyKey, requestJSON)
		resultCh <- lookupResult{result: result, err: err}
	}()

	select {
	case result := <-resultCh:
		t.Fatalf("foreground idempotency returned before terminal state: %#v err=%v", result.result, result.err)
	case <-time.After(75 * time.Millisecond):
	}
	responseJSON := datatypes.JSON(`{"id":"resp_stored_pending","object":"response","status":"completed","model":"m","output":[]}`)
	if err := model.DB().Model(&model.AIResponse{}).Where("id = ?", record.ID).Updates(map[string]any{
		"status": "completed", "response_json": responseJSON, "completed_at": time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	result := <-resultCh
	if result.err != nil || result.result == nil || result.result.Response == nil || result.result.Response.Status != "completed" {
		t.Fatalf("terminal replay=%#v err=%v", result.result, result.err)
	}
}

func setupResponseIdempotencyDB(t *testing.T) *gorm.DB {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "_") + "_" + uuid.NewString()
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.AIResponse{}, &model.AIResponseIdempotencyCache{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	model.SetDB(db)
	return db
}
