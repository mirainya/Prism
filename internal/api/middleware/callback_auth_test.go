package middleware

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/auth"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var callbackAuthDBCounter int64

func TestCallbackAuthAllowsValidSignatureAndPreservesBody(t *testing.T) {
	db := setupCallbackAuthTestDB(t)
	channel := createCallbackTestChannel(t, db, "channel-a", "secret-a")
	task := createCallbackTestTask(t, db, channel.ID, "task_valid")
	body := []byte("{\n  \"task_id\": \"vendor-1\",\n  \"unknown\": {\"kept\": true}\n}")

	handlerCalled := false
	router := newCallbackAuthTestRouter(func(c *gin.Context) {
		handlerCalled = true

		gotChannel, ok := c.Get(CallbackChannelContextKey)
		if !ok || gotChannel.(*model.Channel).ID != channel.ID {
			t.Fatal("authenticated channel was not stored in context")
		}
		gotTask, ok := c.Get(CallbackTaskContextKey)
		if !ok || gotTask.(*model.Task).ID != task.ID {
			t.Fatal("authenticated task was not stored in context")
		}

		gotBody, err := io.ReadAll(c.Request.Body)
		if err != nil {
			t.Fatalf("read callback body: %v", err)
		}
		if !bytes.Equal(gotBody, body) {
			t.Fatalf("callback body changed: got %q, want %q", gotBody, body)
		}
		c.Status(http.StatusNoContent)
	})

	signature := auth.SignCallback(channel.CallbackSecret, channel.ID, task.TaskNo)
	response := performCallbackRequest(router, channel.Type, task.TaskNo, signature, body)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusNoContent, response.Body.String())
	}
	if !handlerCalled {
		t.Fatal("valid callback did not reach the handler")
	}
}

func TestCallbackAuthAllowsDisabledChannelForExistingTask(t *testing.T) {
	db := setupCallbackAuthTestDB(t)
	channel := createCallbackTestChannel(t, db, "channel-disabled", "secret-disabled")
	task := createCallbackTestTask(t, db, channel.ID, "task_disabled")
	if err := db.Model(channel).Update("status", 0).Error; err != nil {
		t.Fatalf("disable channel: %v", err)
	}

	router := newCallbackAuthTestRouter(func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	signature := auth.SignCallback(channel.CallbackSecret, channel.ID, task.TaskNo)
	response := performCallbackRequest(router, channel.Type, task.TaskNo, signature, []byte(`{"status":"SUCCESS"}`))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusNoContent, response.Body.String())
	}
}

func TestCallbackAuthRejectsTamperingAndCrossChannelTasks(t *testing.T) {
	db := setupCallbackAuthTestDB(t)
	channelA := createCallbackTestChannel(t, db, "channel-a", "secret-a")
	channelB := createCallbackTestChannel(t, db, "channel-b", "secret-b")
	taskA := createCallbackTestTask(t, db, channelA.ID, "task_a")
	taskA2 := createCallbackTestTask(t, db, channelA.ID, "task_a2")

	validSignature := auth.SignCallback(channelA.CallbackSecret, channelA.ID, taskA.TaskNo)
	tests := []struct {
		name        string
		channelType string
		taskNo      string
		signature   string
	}{
		{
			name:        "signature changed",
			channelType: channelA.Type,
			taskNo:      taskA.TaskNo,
			signature:   tamperCallbackSignature(validSignature),
		},
		{
			name:        "task changed",
			channelType: channelA.Type,
			taskNo:      taskA2.TaskNo,
			signature:   validSignature,
		},
		{
			name:        "channel changed",
			channelType: channelB.Type,
			taskNo:      taskA.TaskNo,
			signature:   validSignature,
		},
		{
			name:        "valid signature for wrong channel task",
			channelType: channelB.Type,
			taskNo:      taskA.TaskNo,
			signature: auth.SignCallback(
				channelB.CallbackSecret,
				channelB.ID,
				taskA.TaskNo,
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handlerCalled := false
			router := newCallbackAuthTestRouter(func(c *gin.Context) {
				handlerCalled = true
				c.Status(http.StatusNoContent)
			})

			response := performCallbackRequest(router, tt.channelType, tt.taskNo, tt.signature, []byte(`{"status":"SUCCESS"}`))
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body.String())
			}
			if handlerCalled {
				t.Fatal("rejected callback reached the handler")
			}
		})
	}
}

func TestLegacyUnsignedCallbackRouteIsNotRegistered(t *testing.T) {
	setupCallbackAuthTestDB(t)
	handlerCalled := false
	router := newCallbackAuthTestRouter(func(c *gin.Context) {
		handlerCalled = true
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodPost, "/internal/callback/channel-a", strings.NewReader(`{"status":"SUCCESS"}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("legacy route status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if handlerCalled {
		t.Fatal("legacy unsigned callback reached the handler")
	}
}

func TestCallbackAuthReturnsServerErrorForDatabaseFailure(t *testing.T) {
	db := setupCallbackAuthTestDB(t)
	channel := createCallbackTestChannel(t, db, "channel-db-error", "secret-db-error")
	task := createCallbackTestTask(t, db, channel.ID, "task_db_error")
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql db: %v", err)
	}

	handlerCalled := false
	router := newCallbackAuthTestRouter(func(c *gin.Context) {
		handlerCalled = true
		c.Status(http.StatusNoContent)
	})
	signature := auth.SignCallback(channel.CallbackSecret, channel.ID, task.TaskNo)
	response := performCallbackRequest(router, channel.Type, task.TaskNo, signature, []byte(`{"status":"SUCCESS"}`))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if handlerCalled {
		t.Fatal("callback handler ran after authentication database failure")
	}
}

func setupCallbackAuthTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousLogger := logger.L
	logger.L = zap.NewNop()
	t.Cleanup(func() {
		logger.L = previousLogger
	})

	dsn := fmt.Sprintf("file:callback-auth-%d?mode=memory&cache=shared", atomic.AddInt64(&callbackAuthDBCounter, 1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Channel{}, &model.Task{}); err != nil {
		t.Fatalf("migrate callback auth tables: %v", err)
	}
	model.SetDB(db)
	return db
}

func createCallbackTestChannel(t *testing.T, db *gorm.DB, channelType, secret string) *model.Channel {
	t.Helper()
	channel := &model.Channel{
		Type:           channelType,
		Name:           channelType,
		CallbackSecret: secret,
		Status:         1,
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create callback channel: %v", err)
	}
	return channel
}

func createCallbackTestTask(t *testing.T, db *gorm.DB, channelID uint, taskNo string) *model.Task {
	t.Helper()
	task := &model.Task{
		TaskNo:    taskNo,
		ChannelID: channelID,
		Status:    model.TaskStatusProcessing,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("create callback task: %v", err)
	}
	return task
}

func newCallbackAuthTestRouter(handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	internal := router.Group("/internal")
	internal.Use(CallbackAuth())
	internal.POST("/callback/v1/:channel_type/:task_no/:signature", handler)
	return router
}

func performCallbackRequest(router *gin.Engine, channelType, taskNo, signature string, body []byte) *httptest.ResponseRecorder {
	path := fmt.Sprintf("/internal/callback/v1/%s/%s/%s", channelType, taskNo, signature)
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func tamperCallbackSignature(signature string) string {
	if strings.HasPrefix(signature, "0") {
		return "1" + signature[1:]
	}
	return "0" + signature[1:]
}
