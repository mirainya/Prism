package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

const (
	testXFSKeyOne = "xfs_0123456789abcdef0123456789abcdef"
	testXFSKeyTwo = "xfs_fedcba9876543210fedcba9876543210"
)

func TestTokenFileStorageReplacesCurrentKey(t *testing.T) {
	db := setupTestDB(t)
	setupUnifiedFundsSchema(t, db)
	owner := &model.User{Username: "storage-owner", Status: 1}
	other := &model.User{Username: "storage-other", Status: 1}
	if err := db.Create(owner).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(other).Error; err != nil {
		t.Fatal(err)
	}
	token := &model.Token{UserID: owner.ID, Selector: "storage-selector", SecretDigest: make([]byte, 32), SecretDigestVersion: 1, AuthVersion: 1, KeyHint: "****test", Name: "media", Status: 1}
	if err := db.Create(token).Error; err != nil {
		t.Fatal(err)
	}
	seedTokenBudgetForStorageTest(t, db, token.ID)

	service := NewTokenService()
	var probed []string
	service.probeFileStorage = func(_ context.Context, apiKey string) error {
		probed = append(probed, apiKey)
		return nil
	}
	if status, err := service.BindFileStorage(context.Background(), owner.ID, token.ID, testXFSKeyOne); err != nil || !status.Configured || status.KeyHint != "****cdef" {
		t.Fatalf("first bind status=%+v err=%v", status, err)
	}
	if status, err := service.BindFileStorage(context.Background(), owner.ID, token.ID, testXFSKeyTwo); err != nil || !status.Configured || status.KeyHint != "****3210" {
		t.Fatalf("replacement status=%+v err=%v", status, err)
	}
	if err := db.First(token, token.ID).Error; err != nil {
		t.Fatal(err)
	}
	if token.XFSAPIKey != testXFSKeyTwo || len(probed) != 2 {
		t.Fatalf("stored key=%q probed=%#v", token.XFSAPIKey, probed)
	}

	list, err := service.ListTokens(owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertTokenStorageStatus(t, list[0]["xfs_storage"], true, "****3210")
	encoded, _ := json.Marshal(list)
	if strings.Contains(string(encoded), testXFSKeyOne) || strings.Contains(string(encoded), testXFSKeyTwo) {
		t.Fatalf("token response exposed XFS key: %s", encoded)
	}
	if _, err := service.BindFileStorage(context.Background(), other.ID, token.ID, testXFSKeyOne); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("bind as another owner error=%v", err)
	}
	if len(probed) != 2 {
		t.Fatalf("unowned token triggered probe: %#v", probed)
	}
	if _, err := service.UnbindFileStorage(context.Background(), owner.ID, token.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.First(token, token.ID).Error; err != nil {
		t.Fatal(err)
	}
	if token.XFSAPIKey != "" {
		t.Fatalf("key remained after unbind: %q", token.XFSAPIKey)
	}
}

func TestTokenFileStorageRejectsMalformedKeys(t *testing.T) {
	service := NewTokenService()
	for _, apiKey := range []string{"", "xfs_short", "xfs_0123456789ABCDEF0123456789ABCDEF", "xfs_0123456789abcdef0123456789abcdeg"} {
		if _, err := service.BindFileStorage(context.Background(), 1, 1, apiKey); !errors.Is(err, ErrInvalidFileStorageAPIKey) {
			t.Fatalf("key %q error=%v", apiKey, err)
		}
	}
}

func TestTokenFileStorageFailedProbeKeepsCurrentKey(t *testing.T) {
	db := setupTestDB(t)
	owner := &model.User{Username: "storage-probe-owner", Status: 1}
	if err := db.Create(owner).Error; err != nil {
		t.Fatal(err)
	}
	token := &model.Token{UserID: owner.ID, XFSAPIKey: testXFSKeyTwo, Selector: "storage-probe-selector", SecretDigest: make([]byte, 32), SecretDigestVersion: 1, AuthVersion: 1, KeyHint: "****test", Name: "probe", Status: 1}
	if err := db.Create(token).Error; err != nil {
		t.Fatal(err)
	}
	service := NewTokenService()
	service.probeFileStorage = func(context.Context, string) error { return errors.New("probe denied") }
	if _, err := service.BindFileStorage(context.Background(), owner.ID, token.ID, testXFSKeyOne); !errors.Is(err, ErrFileStorageProbeFailed) {
		t.Fatalf("bind error=%v", err)
	}
	if err := db.First(token, token.ID).Error; err != nil {
		t.Fatal(err)
	}
	if token.XFSAPIKey != testXFSKeyTwo {
		t.Fatalf("failed probe changed current key: %q", token.XFSAPIKey)
	}
}

func seedTokenBudgetForStorageTest(t *testing.T, db *gorm.DB, tokenID uint) {
	t.Helper()
	now := time.Now().UTC()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO token_budget_policies(id,token_id,policy_code,window_kind,limit_amount,timezone_name,algorithm_version,created_at) VALUES (1,?,'default-lifetime','lifetime','0','UTC',1,?)`, []any{tokenID, now}},
		{`INSERT INTO token_budget_policy_activations(id,token_id,policy_id,activation_seq,effective_at,created_at) VALUES (1,?,1,1,?,?)`, []any{tokenID, now, now}},
		{`INSERT INTO token_budget_windows(id,token_id,policy_id,activation_id,window_start,limit_amount,used_amount,held_amount,created_at) VALUES (1,?,1,1,?,'0','0','0',?)`, []any{tokenID, now, now}},
	} {
		if err := db.Exec(statement.sql, statement.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func assertTokenStorageStatus(t *testing.T, value any, configured bool, hint string) {
	t.Helper()
	status, ok := value.(TokenFileStorageStatus)
	if !ok || status.Configured != configured || status.KeyHint != hint {
		t.Fatalf("storage status=%+v", value)
	}
}
