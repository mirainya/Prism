package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/billing"
)

type onboardDownstreamPathSource map[string][]string

func (source onboardDownstreamPathSource) DownstreamPaths(adapterCode string, _ uint32) ([]string, error) {
	paths, ok := source[adapterCode]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]string(nil), paths...), nil
}

func validCatalogModelOnboardInput() CatalogModelOnboardInput {
	return CatalogModelOnboardInput{
		ExpectedActiveReleaseID: 7,
		ExpectedConfigVersion:   3,
		SKU: CatalogSKUInput{
			ModelCode: "new-model", APIName: "new-model", DisplayName: "New model", Description: "A model",
			Visibility: "visible", CapabilityTags: []string{"llm"}, OperationCode: "chat.completions",
			ContractVersion: 1, HTTPMethod: "POST", RouteTemplate: "/v1/chat/completions", NormalizationVersion: 1,
			SKUCode: "new-model-standard", VariantCode: DefaultVariantCode, DeliveryMode: "reference", MaxResults: 1,
			IdempotencyMode: "optional", ServiceTiers: []string{"standard"},
		},
		Product: CatalogProductInput{
			ChannelID: 11, CredentialPoolID: 12, ProductCode: "new-model-primary", VendorModel: "provider-new-model",
			CapabilityConstraints: []byte(`{}`), ConstraintsSchemaVersion: 1,
			AdapterCode: "openai_chat", AdapterVersion: 1, TransportCode: "new-model-chat-v1", BaseURL: "https://api.example.com",
			Protocol: "openai", RequestMethod: "POST", RequestPath: "/v1/chat/completions", AuthScheme: "bearer",
			TransportTimeoutMS: 30000, TaskTimeoutMS: 30000, TaskScope: "request", CancelMode: "none", SourceURLPolicy: "fixed",
			UpstreamScopeKind: "product_transport", UpstreamScopeKey: "new-model-chat-v1", CostPlanCode: "new-model-cost",
			Adapter: CatalogAdapterInput{Code: "openai_chat", Version: 1, Protocol: "openai", ImplementationDigest: strings.Repeat("a", 64), MinimumSemanticVersion: "1.0.0"},
		},
		Route: CatalogModelOnboardRoute{Priority: 100, Weight: 100},
		SellRate: CatalogModelOnboardRate{UnitCode: "request", UnitPrice: "1.25", ComponentCode: "request", QuantitySource: billing.QuantityOne,
			ChargeEvent: billing.ChargeSucceeded, QuantityStep: "0", MaxQuantity: "1"},
		CostRate: CatalogModelOnboardRate{UnitCode: "request", UnitPrice: "1.00", ComponentCode: "request", QuantitySource: billing.QuantityOne,
			ChargeEvent: billing.ChargeSucceeded, QuantityStep: "0", MaxQuantity: "1"},
	}
}

func TestCatalogModelOnboardInputRejectsCallerOwnedIntermediateIdentity(t *testing.T) {
	base := validCatalogModelOnboardInput()
	if err := base.Normalize(); err != nil {
		t.Fatal(err)
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	for name, mutate := range map[string]func(*CatalogModelOnboardInput){
		"nested sku version":     func(v *CatalogModelOnboardInput) { v.SKU.ExpectedVersion = 1 },
		"nested product version": func(v *CatalogModelOnboardInput) { v.Product.ExpectedVersion = 1 },
		"caller route id": func(v *CatalogModelOnboardInput) {
			v.Product.Routes = []CatalogRouteInput{{SKUID: 99, Priority: 1, Weight: 1}}
		},
		"missing sell price": func(v *CatalogModelOnboardInput) { v.SellRate.UnitPrice = "" },
		"missing cost price": func(v *CatalogModelOnboardInput) { v.CostRate.UnitPrice = "" },
		"invalid sell meter": func(v *CatalogModelOnboardInput) { v.SellRate.UnitCode = "video" },
		"downstream path too long": func(v *CatalogModelOnboardInput) {
			v.SKU.RouteTemplate = "/" + strings.Repeat("a", 128)
		},
		"zero route weight": func(v *CatalogModelOnboardInput) { v.Route.Weight = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			if err := candidate.Validate(); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err=%v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestModelOnboardUsesAdapterDownstreamPaths(t *testing.T) {
	previous := downstreamPathSource
	SetDownstreamPathSource(onboardDownstreamPathSource{
		"openai_chat": {"/v1/chat/completions", "/v1/responses", "/v1/messages"},
	})
	t.Cleanup(func() { SetDownstreamPathSource(previous) })

	paths, err := modelOnboardDownstreamPaths("openai_chat", 1, "/v1/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"}
	if strings.Join(paths, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("paths=%v, want %v", paths, want)
	}
	if _, err := modelOnboardDownstreamPaths("openai_chat", 1, "/v1/videos/generations"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("mismatched operation path error=%v", err)
	}
}

func TestOnboardCatalogModelRollsBackPartialGraph(t *testing.T) {
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
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1 FOR UPDATE`)).
		WillReturnRows(sqlmock.NewRows([]string{"active_release_id"}).AddRow(7))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT status,config_version,content_hash,semantic_digest FROM gw_catalog_releases WHERE id=? FOR UPDATE`)).
		WithArgs(7).
		WillReturnRows(sqlmock.NewRows([]string{"status", "config_version", "content_hash", "semantic_digest"}).
			AddRow("published", 3, strings.Repeat("b", 64), strings.Repeat("c", 64)))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM gw_models WHERE model_code=?`)).
		WithArgs("new-model").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO gw_models(model_code,created_at) VALUES (?,?)`)).
		WithArgs("new-model", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(31, 1))
	forced := errors.New("forced model-name lookup failure")
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id,model_id FROM gw_model_names WHERE api_name=?`)).
		WithArgs("new-model").
		WillReturnError(forced)
	mock.ExpectRollback()

	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		_, err := store.OnboardCatalogModel(context.Background(), tx, validCatalogModelOnboardInput(), func(RateEvidenceInput) (string, error) {
			return strings.Repeat("d", 64), nil
		}, 1)
		return err
	})
	if !errors.Is(err, forced) {
		t.Fatalf("err=%v, want forced error", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
