package console

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
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
		Status: model.TaskStatusPending,
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
}

func requestPlaygroundTaskEndpoint(
	userID, tokenID uint,
	taskNo, method string,
	handler gin.HandlerFunc,
) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	path := "/api/playground/:token_id/tasks/:task_no"
	router.Handle(method, path, func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, userID)
		handler(c)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		method,
		fmt.Sprintf("/api/playground/%d/tasks/%s", tokenID, taskNo),
		nil,
	)
	router.ServeHTTP(recorder, request)
	return recorder
}
