package admin

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestDecodeCatalogTagsSupportsLegacyObject(t *testing.T) {
	legacy, err := decodeCatalogTags([]byte(`{"source":"legacy","scope":"chat"}`))
	if err != nil {
		t.Fatalf("decode legacy tags: %v", err)
	}
	if want := []string{"scope=chat", "source=legacy"}; !reflect.DeepEqual(legacy, want) {
		t.Fatalf("legacy tags=%v want=%v", legacy, want)
	}
	modern, err := decodeCatalogTags([]byte(`["video","image"]`))
	if err != nil {
		t.Fatalf("decode modern tags: %v", err)
	}
	if want := []string{"video", "image"}; !reflect.DeepEqual(modern, want) {
		t.Fatalf("modern tags=%v want=%v", modern, want)
	}
}

func TestListUnifiedCatalogModelEntriesKeepsModelsIndependentFromReleaseRows(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var previous *gorm.DB
	if model.HasDB() {
		previous = model.DB()
	}
	model.SetDB(db)
	t.Cleanup(func() { model.SetDB(previous) })

	mock.ExpectQuery(`SELECT EXISTS\(`).
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"active"}).AddRow(true))
	mock.ExpectQuery(`SELECT cm\.id,cm\.model_id,m\.model_code`).
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "model_id", "model_code", "display_name", "description", "capability_tags", "visibility"}).
			AddRow(101, 11, "seedance-2.0", "Seedance 2.0", "", []byte(`["video"]`), "visible"))
	mock.ExpectQuery(`SELECT cm\.id,\s+COALESCE\(s\.id,0\)`).
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows(catalogModelSignalColumns()).
			AddRow(101, 201, "seedance-standard", "seedance-2.0", "videos.create", "/v1/videos", "/v1/videos", 301, 0, 0, "", "", "", "seedance", "seedance", "/v1/videos", "task", true))
	mock.ExpectQuery(`SELECT cm\.id,m\.model_code,cm\.display_name,cm\.visibility`).
		WithArgs(uint64(7), 101).
		WillReturnRows(sqlmock.NewRows([]string{
			"catalog_model_id", "model_code", "display_name", "visibility", "capability_tags", "sku_id", "sku_code", "variant_code", "delivery_mode", "max_results", "idempotency_mode", "service_tiers", "api_name", "operation_code", "contract_version", "http_method", "route_template", "sell_rate_count", "route_count", "downstream_paths",
		}).AddRow(101, "seedance-2.0", "Seedance 2.0", "visible", []byte(`["video"]`), 201, "seedance-standard", "default", "managed_copy", 1, "required", []byte(`["standard"]`), "seedance-2.0", "videos.create", 1, "POST", "/v1/videos", 1, 1, []byte(`["/v1/videos"]`)))
	mock.ExpectQuery(`(?s)SELECT cm\.id,p\.id,p\.product_code.*runtime_state\.state,runtime_state\.state_version.*JOIN gw_offering_runtime_state runtime_state`).
		WithArgs(uint64(7), 101).
		WillReturnRows(sqlmock.NewRows([]string{
			"model_id", "product_id", "product_code", "vendor_model", "capability_constraints", "constraints_schema_version",
			"channel_id", "channel_name", "product_transport_id", "task_scope", "cancel_mode", "source_url_policy",
			"channel_transport_id", "transport_code", "base_url", "protocol", "request_method", "request_path",
			"adapter_code", "adapter_version", "offering_id", "offering_state", "offering_state_version",
			"pool_id", "pool_code", "pool_name", "cost_plan_id", "cost_plan_code", "route_count", "cost_rate_count",
			"sku_id", "route_priority", "route_weight",
		}).AddRow(
			101, 701, "seedance-product", "seedance2.0-480p", []byte(`{"resolution":"480p"}`), 1,
			42, "AICost", 601, "task", "none", "fixed",
			801, "seedance-v1", "https://aicost.me", "seedance", "POST", "/v1/videos",
			"seedance", 1, 301, "draining", 4,
			501, "fseedance", "FSeedance", 901, "primary", 1, 2,
			201, 100, 100,
		))

	router := gin.New()
	router.GET("/catalog/:id/model-entries", ListUnifiedCatalogModelEntries)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/catalog/7/model-entries?page=1&page_size=20", nil))
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if body := response.Body.String(); !strings.Contains(body, `"model_type":"video"`) || !strings.Contains(body, `"status":"active"`) || !strings.Contains(body, `"operation_code":"videos.create"`) || !strings.Contains(body, `"offering_state":"draining"`) || !strings.Contains(body, `"offering_state_version":4`) {
		t.Fatalf("unexpected response=%s", body)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListUnifiedCatalogModelEntriesFiltersAllModelsBeforePagination(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var previous *gorm.DB
	if model.HasDB() {
		previous = model.DB()
	}
	model.SetDB(db)
	t.Cleanup(func() { model.SetDB(previous) })

	mock.ExpectQuery(`SELECT EXISTS\(`).
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"active"}).AddRow(true))
	mock.ExpectQuery(`SELECT cm\.id,cm\.model_id,m\.model_code`).
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "model_id", "model_code", "display_name", "description", "capability_tags", "visibility"}).
			AddRow(101, 11, "alpha", "Alpha", "chat model", []byte(`[]`), "visible").
			AddRow(102, 12, "neutral-renderer", "Canvas Renderer", "image edit service", []byte(`[]`), "visible"))
	mock.ExpectQuery(`SELECT cm\.id,\s+COALESCE\(s\.id,0\)`).
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows(catalogModelSignalColumns()).
			AddRow(101, 201, "alpha-default", "alpha", "chat.completions", "/v1/chat/completions", "/v1/chat/completions", 301, 401, 501, "alpha-product", "alpha-upstream", "Chat", "openai_chat", "openai", "/v1/chat/completions", "none", true).
			AddRow(102, 202, "canvas-default", "canvas", "images.edit", "/v1/images/edits", "/v1/images/edits", 302, 402, 502, "canvas-product", "canvas-upstream", "Images", "openai_images", "openai_images", "/v1/images/edits", "request", false))
	mock.ExpectQuery(`SELECT cm\.id,m\.model_code,cm\.display_name,cm\.visibility`).
		WithArgs(uint64(7), 102).
		WillReturnRows(sqlmock.NewRows([]string{
			"catalog_model_id", "model_code", "display_name", "visibility", "capability_tags", "sku_id", "sku_code", "variant_code", "delivery_mode", "max_results", "idempotency_mode", "service_tiers", "api_name", "operation_code", "contract_version", "http_method", "route_template", "sell_rate_count", "route_count", "downstream_paths",
		}).AddRow(102, "neutral-renderer", "Canvas Renderer", "visible", []byte(`[]`), 202, "canvas-default", "default", "managed_copy", 1, "optional", []byte(`["standard"]`), "canvas", "images.edit", 1, "POST", "/v1/images/edits", 1, 1, []byte(`["/v1/images/edits"]`)))
	mock.ExpectQuery(`SELECT cm\.id,p\.id,p\.product_code`).
		WithArgs(uint64(7), 102).
		WillReturnRows(sqlmock.NewRows([]string{"unused"}))

	router := gin.New()
	router.GET("/catalog/:id/model-entries", ListUnifiedCatalogModelEntries)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/catalog/7/model-entries?page=1&page_size=1&q=CANVAS-UPSTREAM&type=image&status=inactive", nil))
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"total":1`) || !strings.Contains(body, `"model_code":"neutral-renderer"`) || !strings.Contains(body, `"model_type":"image"`) || !strings.Contains(body, `"status":"inactive"`) || strings.Contains(body, `"model_code":"alpha"`) {
		t.Fatalf("filtering did not precede pagination: %s", body)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClassifyUnifiedCatalogModelUsesAuthoritySignals(t *testing.T) {
	image := newUnifiedCatalogModelEntry()
	image.modelCode = "looks-like-a-chat-model"
	addCatalogModelSignal(image.adapterCodes, "openai_images")
	if got := classifyUnifiedCatalogModel(&image); got != "image" {
		t.Fatalf("image type=%q", got)
	}
	video := newUnifiedCatalogModelEntry()
	addCatalogModelSignal(video.operationCodes, "videos.create")
	if got := classifyUnifiedCatalogModel(&video); got != "video" {
		t.Fatalf("video type=%q", got)
	}
	genericTask := newUnifiedCatalogModelEntry()
	addCatalogModelSignal(genericTask.taskScopes, "task")
	addCatalogModelSignal(genericTask.adapterCodes, "generic")
	if got := classifyUnifiedCatalogModel(&genericTask); got != "other" {
		t.Fatalf("generic task type=%q", got)
	}
	taggedImage := newUnifiedCatalogModelEntry()
	taggedImage.capabilityTags = []string{"type=image"}
	addCatalogModelSignal(taggedImage.operationCodes, "videos.create")
	addCatalogModelSignal(taggedImage.adapterCodes, "seedance")
	if got := classifyUnifiedCatalogModel(&taggedImage); got != "image" {
		t.Fatalf("tagged image type=%q", got)
	}
	llm := newUnifiedCatalogModelEntry()
	addCatalogModelSignal(llm.operationCodes, "responses.create")
	if got := classifyUnifiedCatalogModel(&llm); got != "llm" {
		t.Fatalf("llm type=%q", got)
	}
	multimodalLLM := newUnifiedCatalogModelEntry()
	multimodalLLM.capabilityTags = []string{"video"}
	addCatalogModelSignal(multimodalLLM.operationCodes, "responses.create")
	if got := classifyUnifiedCatalogModel(&multimodalLLM); got != "llm" {
		t.Fatalf("multimodal llm type=%q", got)
	}
}

func catalogModelSignalColumns() []string {
	return []string{
		"catalog_model_id", "sku_id", "sku_code", "api_name", "operation_code", "operation_route",
		"downstream_path", "route_id", "product_id", "offering_id", "product_code", "vendor_model",
		"channel_name", "adapter_code", "protocol", "request_path", "task_scope", "serviceable",
	}
}

func TestUnifiedCatalogModelSignalsDoNotReadLegacyValidations(t *testing.T) {
	for _, table := range []string{
		"gw_credential_entitlement_state",
		"gw_credential_validation_events",
		"gw_commercial_state",
		"gw_commercial_validation_events",
	} {
		if strings.Contains(unifiedCatalogModelSignalsSQL, table) {
			t.Fatalf("model status SQL still reads legacy validation table %q", table)
		}
	}
}

func TestListUnifiedCatalogModelEntriesRejectsInvalidReleaseID(t *testing.T) {
	router := gin.New()
	router.GET("/catalog/:id/model-entries", ListUnifiedCatalogModelEntries)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/catalog/not-a-release/model-entries", nil))
	if response.Code != 400 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestListUnifiedCatalogModelEntriesRejectsInvalidFilters(t *testing.T) {
	for _, query := range []string{"type=audio", "status=ready", "q=" + strings.Repeat("x", 129), "q=%FF"} {
		router := gin.New()
		router.GET("/catalog/:id/model-entries", ListUnifiedCatalogModelEntries)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/catalog/7/model-entries?"+query, nil))
		if response.Code != 400 {
			t.Fatalf("query=%q status=%d body=%s", query, response.Code, response.Body.String())
		}
	}
}
