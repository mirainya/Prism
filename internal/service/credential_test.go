package service

import (
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/tokenauth"
	"github.com/shopspring/decimal"
)

func TestTokenSecretIsReturnedOnlyAtCreation(t *testing.T) {
	db := setupTestDB(t)
	setupUnifiedFundsSchema(t, db)
	user := &model.User{BaseModel: model.BaseModel{ID: 42}, Username: "credential-owner", Status: 1}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	created, err := NewTokenService().CreateToken(42, &CreateTokenReq{
		Name:    "desktop",
		Balance: decimal.Zero,
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	plainKey, ok := created["key"].(string)
	if !ok || !strings.HasPrefix(plainKey, "sk-prism-") {
		t.Fatalf("creation key = %#v", created["key"])
	}

	var stored model.Token
	if err := db.First(&stored, created["id"]).Error; err != nil {
		t.Fatalf("load stored token: %v", err)
	}
	columns, err := db.Migrator().ColumnTypes(&model.Token{})
	if err != nil {
		t.Fatalf("read token columns: %v", err)
	}
	for _, column := range columns {
		if column.Name() == "key" || column.Name() == "plain_key" {
			t.Fatalf("token schema still contains legacy full-key column %q", column.Name())
		}
	}
	if stored.Selector == "" || len(stored.SecretDigest) != 32 || stored.SecretDigestVersion != tokenauth.DigestVersion {
		t.Fatalf("stored token credential is incomplete: selector=%q digest_bytes=%d version=%d", stored.Selector, len(stored.SecretDigest), stored.SecretDigestVersion)
	}
	wantHint := tokenauth.KeyHint(plainKey)
	if stored.KeyHint != wantHint {
		t.Fatalf("stored key hint = %q, want %q", stored.KeyHint, wantHint)
	}

	list, err := NewTokenService().ListTokens(42)
	if err != nil {
		t.Fatalf("list tokens: %v", err)
	}
	if len(list) != 1 || list[0]["key"] != wantHint || list[0]["key_hint"] != wantHint {
		t.Fatalf("list token key fields = %#v", list)
	}
	if list[0]["key"] == plainKey {
		t.Fatal("token list returned the plaintext key")
	}

	detail, err := NewTokenService().GetToken(42, stored.ID)
	if err != nil {
		t.Fatalf("get token: %v", err)
	}
	if detail["key"] != wantHint || detail["key_hint"] != wantHint {
		t.Fatalf("detail token key fields = %#v", detail)
	}
}
