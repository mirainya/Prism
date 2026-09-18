package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func validPublicModelIdentityChange() PublicModelIdentityChange {
	return PublicModelIdentityChange{
		catalogChangeGuard: catalogChangeGuard{ExpectedActiveReleaseID: 7, ExpectedConfigVersion: 9},
		ReasonCode:         "remove_source_brand",
		Renames: []PublicModelIdentityRename{
			{FromAPIName: "aicost-model-a", ToAPIName: "model-a", DisplayName: "Model A"},
			{FromAPIName: "aicost-model-b", ToAPIName: "model-b", DisplayName: "Model B"},
		},
	}
}

func TestPublicModelIdentityChangeNormalizesAndValidatesBatch(t *testing.T) {
	in := validPublicModelIdentityChange()
	in.ReasonCode = ""
	in.Renames[0] = PublicModelIdentityRename{
		FromAPIName: "  aicost-model-a ", ToAPIName: " model-a  ", DisplayName: "  Model A ",
	}
	in.Normalize()
	if in.ReasonCode != "public_identity_rename" || in.Renames[0].FromAPIName != "aicost-model-a" ||
		in.Renames[0].ToAPIName != "model-a" || in.Renames[0].DisplayName != "Model A" {
		t.Fatalf("normalization failed: %#v", in)
	}
	if err := in.Validate(); err != nil {
		t.Fatalf("valid batch rejected: %v", err)
	}
}

func TestPublicModelIdentityChangeRejectsInvalidAndAmbiguousBatches(t *testing.T) {
	for name, mutate := range map[string]func(*PublicModelIdentityChange){
		"missing config version": func(in *PublicModelIdentityChange) { in.ExpectedConfigVersion = 0 },
		"missing entries":        func(in *PublicModelIdentityChange) { in.Renames = nil },
		"bad reason":             func(in *PublicModelIdentityChange) { in.ReasonCode = "bad reason" },
		"bad source name":        func(in *PublicModelIdentityChange) { in.Renames[0].FromAPIName = "bad name" },
		"bad target name":        func(in *PublicModelIdentityChange) { in.Renames[0].ToAPIName = "bad name" },
		"bad display name":       func(in *PublicModelIdentityChange) { in.Renames[0].DisplayName = "Bad\nName" },
		"duplicate source": func(in *PublicModelIdentityChange) {
			in.Renames[1].FromAPIName = in.Renames[0].FromAPIName
		},
		"duplicate target": func(in *PublicModelIdentityChange) {
			in.Renames[1].ToAPIName = in.Renames[0].ToAPIName
		},
		"too many entries": func(in *PublicModelIdentityChange) {
			in.Renames = make([]PublicModelIdentityRename, maxPublicModelIdentityRenames+1)
			for index := range in.Renames {
				in.Renames[index] = PublicModelIdentityRename{
					FromAPIName: "source-" + strings.Repeat("x", index),
					ToAPIName:   "target-" + strings.Repeat("x", index),
					DisplayName: "Model",
				}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := validPublicModelIdentityChange()
			mutate(&in)
			in.Normalize()
			if err := in.Validate(); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err=%v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestChangePublicModelIdentitiesRejectsAPINameConflictBeforeWrite(t *testing.T) {
	db, mock, store, tx := publicIdentityTestTx(t)
	defer db.Close()
	expectPublicIdentityActiveLock(mock, 7, 9)
	expectPublicIdentitySource(mock, 7, "aicost-model-a", 101, 11, 21, "aicost-model-a", "AiCost Model A")
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM gw_model_names WHERE api_name=? FOR UPDATE`)).
		WithArgs("model-a").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(99))

	in := validPublicModelIdentityChange()
	in.Renames = in.Renames[:1]
	_, err := store.ChangePublicModelIdentities(context.Background(), tx, in, 42)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v, want ErrConflict", err)
	}
	expectPublicIdentityRollback(t, mock, tx)
}

func TestChangePublicModelIdentitiesRejectsModelCodeConflictBeforeWrite(t *testing.T) {
	db, mock, store, tx := publicIdentityTestTx(t)
	defer db.Close()
	expectPublicIdentityActiveLock(mock, 7, 9)
	expectPublicIdentitySource(mock, 7, "aicost-model-a", 101, 11, 21, "aicost-model-a", "AiCost Model A")
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM gw_model_names WHERE api_name=? FOR UPDATE`)).
		WithArgs("model-a").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM gw_models WHERE model_code=? FOR UPDATE`)).
		WithArgs("model-a").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(99))

	in := validPublicModelIdentityChange()
	in.Renames = in.Renames[:1]
	_, err := store.ChangePublicModelIdentities(context.Background(), tx, in, 42)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v, want ErrConflict", err)
	}
	expectPublicIdentityRollback(t, mock, tx)
}

func TestChangePublicModelIdentitiesRejectsSKUCodeConflictBeforeWrite(t *testing.T) {
	db, mock, store, tx := publicIdentityTestTx(t)
	defer db.Close()
	expectPublicIdentityActiveLock(mock, 7, 9)
	expectPublicIdentityConflictFreeNames(mock, 7, "aicost-model-a", 101, 11, 21, "aicost-model-a", "AiCost Model A", "model-a")
	mock.ExpectQuery(regexp.QuoteMeta(publicIdentitySKUQuery)).
		WithArgs(uint64(7), uint64(101)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "sku_code"}).AddRow(301, "aicost-model-a-standard"))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM gw_skus WHERE release_id=? AND sku_code=? FOR UPDATE`)).
		WithArgs(uint64(7), "model-a-standard").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(399))

	in := validPublicModelIdentityChange()
	in.Renames = in.Renames[:1]
	_, err := store.ChangePublicModelIdentities(context.Background(), tx, in, 42)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v, want ErrConflict", err)
	}
	expectPublicIdentityRollback(t, mock, tx)
}

func TestChangePublicModelIdentitiesRejectsMismatchedSourceModelCode(t *testing.T) {
	db, mock, store, tx := publicIdentityTestTx(t)
	defer db.Close()
	expectPublicIdentityActiveLock(mock, 7, 9)
	expectPublicIdentitySource(mock, 7, "aicost-model-a", 101, 11, 21, "internal-model-a", "AiCost Model A")

	in := validPublicModelIdentityChange()
	in.Renames = in.Renames[:1]
	_, err := store.ChangePublicModelIdentities(context.Background(), tx, in, 42)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v, want ErrConflict", err)
	}
	expectPublicIdentityRollback(t, mock, tx)
}

func TestChangePublicModelIdentitiesDirectlyRenamesBatchAndAdvancesOnce(t *testing.T) {
	db, mock, store, tx := publicIdentityTestTx(t)
	defer db.Close()
	expectPublicIdentityActiveLock(mock, 7, 9)
	expectPublicIdentityPlan(mock, 7, "aicost-model-a", 101, 11, 21, "AiCost Model A", "model-a", []publicSKUIdentityRename{
		{ID: 301, OldCode: "aicost-model-a-standard", NewCode: "model-a-standard"},
		{ID: 302, OldCode: "aicost-model-a-premium", NewCode: "model-a-premium"},
	})
	expectPublicIdentityPlan(mock, 7, "aicost-model-b", 102, 12, 22, "AiCost Model B", "model-b", []publicSKUIdentityRename{
		{ID: 303, OldCode: "aicost-model-b-standard", NewCode: "model-b-standard"},
	})

	expectDirectPublicIdentityUpdates(mock, 7, 101, 11, 21, "aicost-model-a", "model-a", "Model A", []publicSKUIdentityRename{
		{ID: 301, OldCode: "aicost-model-a-standard", NewCode: "model-a-standard"},
		{ID: 302, OldCode: "aicost-model-a-premium", NewCode: "model-a-premium"},
	})
	expectDirectPublicIdentityUpdates(mock, 7, 102, 12, 22, "aicost-model-b", "model-b", "Model B", []publicSKUIdentityRename{
		{ID: 303, OldCode: "aicost-model-b-standard", NewCode: "model-b-standard"},
	})

	for _, query := range catalogDigestQueries {
		mock.ExpectQuery(regexp.QuoteMeta(query)).WithArgs(uint64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"empty"}))
	}
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM gw_catalog_releases WHERE content_hash=? AND id<>?`)).
		WithArgs(sqlmock.AnyArg(), uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE gw_catalog_readiness SET content_hash=?,heartbeat_at=? WHERE release_id=?`)).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE gw_catalog_releases SET config_version=?,content_hash=?,updated_at=? WHERE id=? AND status='published' AND config_version=?`)).
		WithArgs(uint64(10), sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(7), uint64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO audit_events(actor_type,actor_user_id,action,resource_type,resource_id,outcome,http_status,metadata,created_at) VALUES ('user',?,?,?,?,'success',200,?,?)`)).
		WithArgs(uint64(42), "catalog_change.public_model_identities", "catalog_release", "7", directPublicIdentityAuditMatcher{}, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	result, err := store.ChangePublicModelIdentities(context.Background(), tx, validPublicModelIdentityChange(), 42)
	if err != nil {
		t.Fatalf("change public model identities: %v", err)
	}
	if result.ReleaseID != 7 || result.SourceReleaseID != 7 || result.ConfigVersion != 10 || !result.Activated {
		t.Fatalf("result=%+v", result)
	}
	mock.ExpectCommit()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func publicIdentityTestTx(t *testing.T) (*sql.DB, sqlmock.Sqlmock, *Store, *sql.Tx) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	store, err := New(db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db, mock, store, tx
}

func expectPublicIdentityRollback(t *testing.T, mock sqlmock.Sqlmock, tx *sql.Tx) {
	t.Helper()
	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func expectPublicIdentityActiveLock(mock sqlmock.Sqlmock, releaseID, version uint64) {
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1 FOR UPDATE`)).
		WillReturnRows(sqlmock.NewRows([]string{"active_release_id"}).AddRow(releaseID))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT status,config_version,content_hash,semantic_digest FROM gw_catalog_releases WHERE id=? FOR UPDATE`)).
		WithArgs(releaseID).
		WillReturnRows(sqlmock.NewRows([]string{"status", "config_version", "content_hash", "semantic_digest"}).
			AddRow("published", version, strings.Repeat("a", 64), strings.Repeat("b", 64)))
}

const publicIdentitySourceQuery = `SELECT cm.id,cm.model_id,mn.id,m.model_code,cm.display_name
FROM gw_catalog_models cm
JOIN gw_models m ON m.id=cm.model_id
JOIN gw_catalog_model_names cmn ON cmn.release_id=cm.release_id AND cmn.catalog_model_id=cm.id AND cmn.model_id=cm.model_id AND cmn.is_primary=TRUE
JOIN gw_model_names mn ON mn.id=cmn.model_name_id AND mn.model_id=cm.model_id
WHERE cm.release_id=? AND mn.api_name=? FOR UPDATE`

const publicIdentitySKUQuery = `SELECT s.id,s.sku_code
FROM gw_skus s
JOIN gw_model_operations mo ON mo.release_id=s.release_id AND mo.id=s.model_operation_id
WHERE s.release_id=? AND mo.catalog_model_id=? ORDER BY s.id FOR UPDATE`

func expectPublicIdentitySource(mock sqlmock.Sqlmock, releaseID uint64, oldName string, catalogModelID, modelID, modelNameID uint64, modelCode, displayName string) {
	mock.ExpectQuery(regexp.QuoteMeta(publicIdentitySourceQuery)).WithArgs(releaseID, oldName).
		WillReturnRows(sqlmock.NewRows([]string{"catalog_model_id", "model_id", "model_name_id", "model_code", "display_name"}).
			AddRow(catalogModelID, modelID, modelNameID, modelCode, displayName))
}

func expectPublicIdentityConflictFreeNames(mock sqlmock.Sqlmock, releaseID uint64, oldName string, catalogModelID, modelID, modelNameID uint64, modelCode, displayName, newName string) {
	expectPublicIdentitySource(mock, releaseID, oldName, catalogModelID, modelID, modelNameID, modelCode, displayName)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM gw_model_names WHERE api_name=? FOR UPDATE`)).
		WithArgs(newName).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM gw_models WHERE model_code=? FOR UPDATE`)).
		WithArgs(newName).WillReturnRows(sqlmock.NewRows([]string{"id"}))
}

func expectPublicIdentityPlan(mock sqlmock.Sqlmock, releaseID uint64, oldName string, catalogModelID, modelID, modelNameID uint64, oldDisplay, newName string, skus []publicSKUIdentityRename) {
	expectPublicIdentityConflictFreeNames(mock, releaseID, oldName, catalogModelID, modelID, modelNameID, oldName, oldDisplay, newName)
	rows := sqlmock.NewRows([]string{"id", "sku_code"})
	for _, sku := range skus {
		rows.AddRow(sku.ID, sku.OldCode)
	}
	mock.ExpectQuery(regexp.QuoteMeta(publicIdentitySKUQuery)).WithArgs(releaseID, catalogModelID).WillReturnRows(rows)
	for _, sku := range skus {
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM gw_skus WHERE release_id=? AND sku_code=? FOR UPDATE`)).
			WithArgs(releaseID, sku.NewCode).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	}
}

func expectDirectPublicIdentityUpdates(mock sqlmock.Sqlmock, releaseID, catalogModelID, modelID, modelNameID uint64, oldName, newName, newDisplay string, skus []publicSKUIdentityRename) {
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE gw_model_names SET api_name=? WHERE id=? AND model_id=? AND api_name=?`)).
		WithArgs(newName, modelNameID, modelID, oldName).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE gw_models SET model_code=? WHERE id=? AND model_code=?`)).
		WithArgs(newName, modelID, oldName).WillReturnResult(sqlmock.NewResult(0, 1))
	for _, sku := range skus {
		mock.ExpectExec(regexp.QuoteMeta(`UPDATE gw_skus SET sku_code=? WHERE release_id=? AND id=? AND sku_code=?`)).
			WithArgs(sku.NewCode, releaseID, sku.ID, sku.OldCode).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE gw_catalog_models SET display_name=? WHERE release_id=? AND id=?`)).
		WithArgs(newDisplay, releaseID, catalogModelID).WillReturnResult(sqlmock.NewResult(0, 1))
}

type directPublicIdentityAuditMatcher struct{}

func (directPublicIdentityAuditMatcher) Match(value driver.Value) bool {
	metadata, ok := value.(string)
	if !ok || strings.Contains(metadata, "old_alias_kept") {
		return false
	}
	for _, fragment := range []string{
		`"reason_code":"remove_source_brand"`, `"change_count":2`,
		`"api_name":"aicost-model-a"`, `"api_name":"model-a"`,
		`"model_code":"aicost-model-a"`, `"model_code":"model-a"`,
		`"aicost-model-a-standard"`, `"model-a-standard"`,
		`"api_name":"aicost-model-b"`, `"api_name":"model-b"`,
		`"model_code":"aicost-model-b"`, `"model_code":"model-b"`,
		`"aicost-model-b-standard"`, `"model-b-standard"`,
	} {
		if !strings.Contains(metadata, fragment) {
			return false
		}
	}
	return true
}
