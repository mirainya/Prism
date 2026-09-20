package console

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

func TestListPlaygroundImageModelsUsesOnlyPublishedImageEndpoints(t *testing.T) {
	mock := playgroundVideoCatalogMock(t)
	rows := sqlmock.NewRows([]string{
		"release_id", "catalog_model_id", "model_id", "sort_order", "model_code", "api_name", "is_primary",
		"display_name", "description", "visibility", "capability_tags", "operation_code", "http_method",
		"route_template", "downstream_operation", "downstream_http_method", "downstream_path",
		"sku_id", "sku_code", "delivery_mode", "max_results", "idempotency_mode",
		"service_tiers", "channel_id", "channel_code", "channel_name", "vendor_model", "protocol",
		"task_scope", "capability_constraints", "cancel_mode",
	})
	addImageCatalogRow := func(apiName, displayName, downstreamMethod, downstreamPath string, skuID int, serviceTiers, constraints string) {
		rows.AddRow(1, 10+skuID, 20+skuID, skuID, "image_model", apiName, true,
			displayName, "Image model", "public", []byte(`[]`), "images.generate", "POST",
			"/v1/images/generations", "images.generate", downstreamMethod, downstreamPath,
			skuID, "standard", "sync", 1, "request", []byte(serviceTiers),
			40, "images", "Images", apiName, "openai_images@1", "request", []byte(constraints), "none")
	}
	addImageCatalogRow("gpt-image", "GPT Image", "POST", "/v1/images/generations", 30, `["standard"]`,
		`{"sizes":["1024x1024"],"qualities":["standard"]}`)
	addImageCatalogRow("gpt-image", "GPT Image", "POST", "/v1/images/generations", 31, `["standard","priority"]`,
		`{"sizes":["1536x1024","1024x1024"],"aspect_ratios":["16:9"],"qualities":["high"]}`)
	addImageCatalogRow("gpt-image", "GPT Image", "POST", "/v1/images/edits", 32, `["standard"]`,
		`{"sizes":["512x512"],"qualities":["edit"]}`)
	addImageCatalogRow("edit-only", "Edit Only", "POST", "/v1/images/edits", 33, `["standard"]`,
		`{"aspect_ratios":["4:3"]}`)
	addImageCatalogRow("gpt-image", "GPT Image", "POST", "/v1/images/generations", 34, `["priority"]`,
		`{"sizes":["2048x2048"],"qualities":["priority-only"]}`)
	addImageCatalogRow("priority-only", "Priority Only", "POST", "/v1/images/edits", 35, `["priority"]`, `{}`)
	addImageCatalogRow("wrong-method", "Wrong Method", "GET", "/v1/images/generations", 36, `["standard"]`, `{}`)
	addImageCatalogRow("video-model", "Video", "POST", "/v1/videos/generations", 37, `["standard"]`, `{}`)
	mock.ExpectQuery(`SELECT rel\.id`).WillReturnRows(rows)

	result, err := listPlaygroundImageModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []playgroundImageModel{
		{
			ID: "gpt-image", Name: "GPT Image", Description: "Image model", SupportsGeneration: true, SupportsEdit: true,
			GenerationOptions: &playgroundImageModelOptions{
				Sizes: []string{"1024x1024", "1536x1024"}, AspectRatios: []string{"16:9"}, Qualities: []string{"standard", "high"},
			},
			EditOptions: &playgroundImageModelOptions{Sizes: []string{"512x512"}, Qualities: []string{"edit"}},
		},
		{
			ID: "edit-only", Name: "Edit Only", Description: "Image model", SupportsEdit: true,
			EditOptions: &playgroundImageModelOptions{AspectRatios: []string{"4:3"}},
		},
	}
	if !reflect.DeepEqual(result.Models, want) {
		t.Fatalf("models = %#v, want %#v", result.Models, want)
	}
}

func TestRegisterRoutesIncludesImagePlayground(t *testing.T) {
	router := gin.New()
	RegisterRoutes(router.Group("/api"))

	want := map[string]bool{
		"GET /api/playground/:token_id/images/models":       false,
		"POST /api/playground/:token_id/images/generations": false,
		"POST /api/playground/:token_id/images/edits":       false,
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, exists := want[key]; exists {
			want[key] = true
		}
	}
	for route, found := range want {
		if !found {
			t.Fatalf("route is not registered: %s", route)
		}
	}
}

func TestPlaygroundCreateImageGenerationInjectsOwnedToken(t *testing.T) {
	testPlaygroundImageHandlerInjectsOwnedToken(t, "/api/playground/:token_id/images/generations", "/api/playground/%d/images/generations", PlaygroundCreateImageGeneration)
}

func TestPlaygroundCreateImageEditInjectsOwnedToken(t *testing.T) {
	testPlaygroundImageHandlerInjectsOwnedToken(t, "/api/playground/:token_id/images/edits", "/api/playground/%d/images/edits", PlaygroundCreateImageEdit)
}

func testPlaygroundImageHandlerInjectsOwnedToken(t *testing.T, route, requestPath string, handler gin.HandlerFunc) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:playground-image-token?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Token{}); err != nil {
		t.Fatal(err)
	}
	var previous *gorm.DB
	if model.HasDB() {
		previous = model.DB()
	}
	model.SetDB(db)
	t.Cleanup(func() {
		model.SetDB(previous)
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})

	token := model.Token{UserID: 42, Name: "playground-image", Status: 1}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}

	var injectedTokenID uint
	router := gin.New()
	router.POST(route, func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, uint(42))
		handler(c)
		injectedTokenID = middleware.GetTokenID(c)
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf(requestPath, token.ID),
		strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if injectedTokenID != token.ID {
		t.Fatalf("injected token id = %d, want %d", injectedTokenID, token.ID)
	}
}
