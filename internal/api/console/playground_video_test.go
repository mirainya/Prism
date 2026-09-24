package console

import (
	"context"
	"reflect"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func playgroundVideoCatalogMock(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	gdb, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	var old *gorm.DB
	if model.HasDB() {
		old = model.DB()
	}
	model.SetDB(gdb)
	t.Cleanup(func() {
		model.SetDB(old)
		_ = db.Close()
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})
	return mock
}

func TestListVideoModelsUsesPublishedRuntimeCatalog(t *testing.T) {
	mock := playgroundVideoCatalogMock(t)
	mock.ExpectQuery(`SELECT rel\.id`).WillReturnRows(sqlmock.NewRows([]string{
		"release_id", "catalog_model_id", "model_id", "sort_order", "model_code", "api_name", "is_primary",
		"display_name", "description", "visibility", "capability_tags", "operation_code", "http_method",
		"route_template", "downstream_operation", "downstream_http_method", "downstream_path",
		"sku_id", "sku_code", "delivery_mode", "max_results", "idempotency_mode",
		"service_tiers", "channel_id", "channel_code", "channel_name", "vendor_model", "protocol",
		"task_scope", "capability_constraints", "cancel_mode",
	}).
		AddRow(1, 10, 20, 1, "seedance_2_0", "seedance-2.0", true,
			"Seedance 2.0", "", "public", []byte(`[]`), "videos.generate", "POST",
			"/v1/videos/generations", "videos.generate", "POST", "/v1/videos/generations",
			30, "standard", "async", 1, "request",
			[]byte(`["standard"]`), 40, "seedance", "Seedance", "seedance-2.0", "openai",
			"task", []byte(`{"resolutions":["720p"],"task_types":["text"]}`), "none").
		AddRow(1, 10, 20, 1, "seedance_2_0", "seedance-2.0", true,
			"Seedance 2.0", "", "public", []byte(`[]`), "videos.generate", "POST",
			"/v1/videos/generations", "videos.generate", "POST", "/v1/videos/generations",
			31, "priority", "async", 1, "request",
			[]byte(`["priority"]`), 40, "seedance", "Seedance", "seedance-2.0", "openai",
			"task", []byte(`{"resolutions":["1080p"],"task_types":["multimodal"]}`), "upstream"))

	result, err := listVideoModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Models, []string{"seedance-2.0"}) {
		t.Fatalf("models = %#v", result.Models)
	}
	options := result.ModelOptions["seedance-2.0"]
	if !reflect.DeepEqual(options.Resolutions, []string{"720p", "1080p"}) {
		t.Fatalf("resolutions = %#v", options.Resolutions)
	}
	if !reflect.DeepEqual(options.ServiceTiers, []string{"standard", "priority"}) {
		t.Fatalf("service tiers = %#v", options.ServiceTiers)
	}
	if !reflect.DeepEqual(options.TaskTypes, []string{"text", "multimodal"}) {
		t.Fatalf("task types = %#v", options.TaskTypes)
	}
}

func TestListVideoModelsDoesNotInventTaskTypes(t *testing.T) {
	mock := playgroundVideoCatalogMock(t)
	columns := []string{
		"release_id", "catalog_model_id", "model_id", "sort_order", "model_code", "api_name", "is_primary",
		"display_name", "description", "visibility", "capability_tags", "operation_code", "http_method",
		"route_template", "downstream_operation", "downstream_http_method", "downstream_path",
		"sku_id", "sku_code", "delivery_mode", "max_results", "idempotency_mode",
		"service_tiers", "channel_id", "channel_code", "channel_name", "vendor_model", "protocol",
		"task_scope", "capability_constraints", "cancel_mode",
	}
	rows := sqlmock.NewRows(columns)
	for _, item := range []struct {
		name        string
		constraints string
	}{
		{name: "unspecified-model", constraints: `{}`},
		{name: "text-only-model", constraints: `{"task_types":["text"]}`},
	} {
		rows.AddRow(1, 10, 20, 1, item.name, item.name, true,
			item.name, "", "public", []byte(`[]`), "videos.generate", "POST",
			"/v1/videos/generations", "videos.generate", "POST", "/v1/videos/generations",
			30, "standard", "async", 1, "request",
			[]byte(`["standard"]`), 40, "aicost", "AICost", item.name, "generic",
			"task", []byte(item.constraints), "none")
	}
	mock.ExpectQuery(`SELECT rel\.id`).WillReturnRows(rows)

	result, err := listVideoModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := result.ModelOptions["unspecified-model"].TaskTypes; len(got) != 0 {
		t.Fatalf("missing task types must remain unspecified, got %#v", got)
	}
	if got := result.ModelOptions["text-only-model"].TaskTypes; !reflect.DeepEqual(got, []string{"text"}) {
		t.Fatalf("text-only task types = %#v", got)
	}
}
