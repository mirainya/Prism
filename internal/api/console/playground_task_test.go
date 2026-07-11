package console

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

var playgroundDebugDBCounter int64

func setupPlaygroundDebugTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:playground-debug-%d?mode=memory&cache=shared", atomic.AddInt64(&playgroundDebugDBCounter, 1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Token{},
		&model.Conversation{},
		&model.ChannelRequestLog{},
	); err != nil {
		t.Fatalf("migrate playground debug tables: %v", err)
	}
	model.SetDB(db)
	return db
}

func requestPlaygroundDebug(userID, tokenID, requestLogID uint) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/playground/:token_id/debug/:request_log_id", func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, userID)
		PlaygroundGetDebug(c)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/playground/%d/debug/%d", tokenID, requestLogID),
		nil,
	)
	router.ServeHTTP(recorder, request)
	return recorder
}

func createPlaygroundDebugToken(t *testing.T, db *gorm.DB, userID uint, key string) *model.Token {
	t.Helper()
	token := &model.Token{UserID: userID, Key: key, Status: 1}
	if err := db.Create(token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	return token
}

func TestPlaygroundGetDebugRejectsUnownedLogs(t *testing.T) {
	t.Run("unassigned request log", func(t *testing.T) {
		db := setupPlaygroundDebugTestDB(t)
		token := createPlaygroundDebugToken(t, db, 10, "debug-token-unassigned")
		requestLog := &model.ChannelRequestLog{RequestType: model.RequestTypeChat}
		if err := db.Create(requestLog).Error; err != nil {
			t.Fatalf("create request log: %v", err)
		}

		response := requestPlaygroundDebug(token.UserID, token.ID, requestLog.ID)
		if response.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
		}
	})

	t.Run("different token conversation", func(t *testing.T) {
		db := setupPlaygroundDebugTestDB(t)
		currentToken := createPlaygroundDebugToken(t, db, 20, "debug-token-current")
		otherToken := createPlaygroundDebugToken(t, db, 20, "debug-token-other")
		conversation := &model.Conversation{UserID: 20, TokenID: otherToken.ID, Status: 1}
		if err := db.Create(conversation).Error; err != nil {
			t.Fatalf("create conversation: %v", err)
		}
		requestLog := &model.ChannelRequestLog{
			ConversationID: conversation.ID,
			RequestType:    model.RequestTypeChat,
		}
		if err := db.Create(requestLog).Error; err != nil {
			t.Fatalf("create request log: %v", err)
		}

		response := requestPlaygroundDebug(currentToken.UserID, currentToken.ID, requestLog.ID)
		if response.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
		}
	})
}

func TestPlaygroundGetDebugAllowsOwnedLog(t *testing.T) {
	db := setupPlaygroundDebugTestDB(t)
	token := createPlaygroundDebugToken(t, db, 30, "debug-token-owned")
	conversation := &model.Conversation{UserID: token.UserID, TokenID: token.ID, Status: 1}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	requestLog := &model.ChannelRequestLog{
		ConversationID: conversation.ID,
		RequestType:    model.RequestTypeChat,
		RequestBody:    `{"model":"test-model"}`,
	}
	if err := db.Create(requestLog).Error; err != nil {
		t.Fatalf("create request log: %v", err)
	}

	response := requestPlaygroundDebug(token.UserID, token.ID, requestLog.ID)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
}
