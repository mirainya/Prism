package repository

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func validActiveCatalogProductInput() CatalogProductInput {
	return CatalogProductInput{
		ChannelID: 11, CredentialPoolID: 12, ProductCode: "gpt-4o-mini-primary", VendorModel: "gpt-4o-mini",
		CapabilityConstraints: []byte(`{}`), ConstraintsSchemaVersion: 1,
		AdapterCode: "openai_chat", AdapterVersion: 1, TransportCode: "openai-chat-v1", BaseURL: "https://api.example.com",
		Protocol: "openai", RequestMethod: "POST", RequestPath: "/v1/chat/completions", AuthScheme: "bearer",
		TransportTimeoutMS: 30000, TaskTimeoutMS: 30000, TaskScope: "request", CancelMode: "none", SourceURLPolicy: "fixed",
		UpstreamScopeKind: "product_transport", UpstreamScopeKey: "openai-chat-v1", CostPlanCode: "primary",
		Routes: []CatalogRouteInput{{SKUID: 21, Priority: 100, Weight: 100}},
		Adapter: CatalogAdapterInput{
			Code: "openai_chat", Version: 1, Protocol: "openai",
			ImplementationDigest: strings.Repeat("a", 64), MinimumSemanticVersion: "1.0.0",
		},
	}
}

func TestCatalogProductInputAcceptsOpaqueVendorModel(t *testing.T) {
	for _, vendorModel := range []string{"seedance-2.5", "seedance2.5-9图"} {
		in := validActiveCatalogProductInput()
		in.ExpectedVersion = 1
		in.VendorModel = vendorModel
		if err := in.Normalize(); err != nil {
			t.Fatalf("vendor_model=%q normalize: %v", vendorModel, err)
		}
		if err := in.Validate(); err != nil {
			t.Fatalf("vendor_model=%q validate: %v", vendorModel, err)
		}
	}
}

func TestCatalogProductInputRejectsUnsafeVendorModel(t *testing.T) {
	invalidUTF8 := string([]byte{'m', 0xff})
	for _, vendorModel := range []string{
		"", "   ", "model\x00name", "model\rname", "model\nname", "model\tname",
		strings.Repeat("a", 256), strings.Repeat("图", 86), invalidUTF8,
	} {
		in := validActiveCatalogProductInput()
		in.VendorModel = vendorModel
		if err := in.Normalize(); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("vendor_model=%q err=%v, want ErrInvalidInput", vendorModel, err)
		}
	}
}

func TestCreateActiveCatalogProductRejectsStaleConfigBeforeContentWrites(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1 FOR UPDATE`)).
		WillReturnRows(sqlmock.NewRows([]string{"active_release_id"}).AddRow(7))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT status,config_version,content_hash,semantic_digest FROM gw_catalog_releases WHERE id=? FOR UPDATE`)).
		WithArgs(7).
		WillReturnRows(sqlmock.NewRows([]string{"status", "config_version", "content_hash", "semantic_digest"}).AddRow("published", 4, strings.Repeat("b", 64), strings.Repeat("c", 64)))

	_, err = store.CreateActiveCatalogProduct(context.Background(), tx, 7, 3, validActiveCatalogProductInput(), 1)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v, want ErrConflict", err)
	}
	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
