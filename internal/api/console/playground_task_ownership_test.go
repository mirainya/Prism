package console

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"gorm.io/datatypes"
)

func TestPlaygroundTaskEndpointsEnforceTokenOwnership(t *testing.T) {
	db := setupPlaygroundDebugTestDB(t)
	if err := db.AutoMigrate(&model.Task{}); err != nil {
		t.Fatal(err)
	}
	currentToken := createPlaygroundDebugToken(t, db, 70, "task-token-current")
	otherToken := createPlaygroundDebugToken(t, db, 70, "task-token-other")
	task := &model.Task{
		TaskNo: service.GenerateTaskNo(), UserID: currentToken.UserID, TokenID: otherToken.ID,
		Status: model.TaskStatusPending, RequestParams: datatypes.JSON(`{"prompt":"detail test"}`),
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}

	detail := requestPlaygroundTaskEndpoint(
		currentToken.UserID, currentToken.ID, task.TaskNo, http.MethodGet, PlaygroundGetTask,
	)
	if detail.Code != http.StatusNotFound {
		t.Fatalf("cross-token detail status = %d, want %d; body=%s", detail.Code, http.StatusNotFound, detail.Body.String())
	}
	cancel := requestPlaygroundTaskEndpoint(
		currentToken.UserID, currentToken.ID, task.TaskNo, http.MethodPost, PlaygroundCancelTask,
	)
	if cancel.Code != http.StatusBadRequest {
		t.Fatalf("cross-token cancel status = %d, want %d; body=%s", cancel.Code, http.StatusBadRequest, cancel.Body.String())
	}
	if err := db.First(task, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskStatusPending {
		t.Fatalf("cross-token request changed task status to %s", task.Status)
	}

	owned := requestPlaygroundTaskEndpoint(
		otherToken.UserID, otherToken.ID, task.TaskNo, http.MethodGet, PlaygroundGetTask,
	)
	if owned.Code != http.StatusOK {
		t.Fatalf("owned detail status = %d, want %d; body=%s", owned.Code, http.StatusOK, owned.Body.String())
	}
	var lightPayload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(owned.Body.Bytes(), &lightPayload); err != nil {
		t.Fatal(err)
	}
	if _, exists := lightPayload.Data["raw_params"]; exists {
		t.Fatalf("light task response includes raw_params: %s", owned.Body.String())
	}

	full := requestPlaygroundTaskEndpoint(
		otherToken.UserID, otherToken.ID, task.TaskNo, http.MethodGet, PlaygroundGetTask, "include_params=true",
	)
	var fullPayload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(full.Body.Bytes(), &fullPayload); err != nil {
		t.Fatal(err)
	}
	if _, exists := fullPayload.Data["raw_params"]; !exists {
		t.Fatalf("full task response omits raw_params: %s", full.Body.String())
	}
	rawParams, ok := fullPayload.Data["raw_params"].(map[string]any)
	if !ok || rawParams["prompt"] != "detail test" {
		t.Fatalf("full task response raw_params = %#v", fullPayload.Data["raw_params"])
	}
}

func requestPlaygroundTaskEndpoint(
	userID, tokenID uint,
	taskNo, method string,
	handler gin.HandlerFunc,
	query ...string,
) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	path := "/api/playground/:token_id/tasks/:task_no"
	router.Handle(method, path, func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, userID)
		handler(c)
	})

	recorder := httptest.NewRecorder()
	requestPath := fmt.Sprintf("/api/playground/%d/tasks/%s", tokenID, taskNo)
	if len(query) > 0 && query[0] != "" {
		requestPath += "?" + query[0]
	}
	request := httptest.NewRequest(
		method,
		requestPath,
		nil,
	)
	router.ServeHTTP(recorder, request)
	return recorder
}
