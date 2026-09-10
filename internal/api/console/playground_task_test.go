package console

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

func setupPlaygroundDebugTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupAPICallConsoleTestDB(t)
	if err := db.AutoMigrate(
		&model.Token{},
		&model.Conversation{},
		&model.ConversationTurn{},
	); err != nil {
		t.Fatalf("migrate playground debug tables: %v", err)
	}
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
	token := &model.Token{UserID: userID, Selector: key, Status: 1}
	if err := db.Create(token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	return token
}

func createPlaygroundDebugLog(t *testing.T, db *gorm.DB, userID, tokenID uint, callID string) uint {
	t.Helper()
	internalCallID := createAPICallConsoleTestCall(t, db, model.APICall{
		ID: callID, UserID: userID, TokenID: tokenID, Model: "model-a",
		Status: model.APICallStatusCompleted, CreatedAt: time.Now(),
	})
	var attemptID uint
	if err := db.Raw(`SELECT COALESCE(MAX(id), 0) + 1 FROM gw_api_call_attempts`).Scan(&attemptID).Error; err != nil {
		t.Fatalf("allocate attempt id: %v", err)
	}
	if err := db.Exec(`INSERT INTO gw_api_call_attempts(id,call_id,attempt_no,catalog_release_id,product_transport_id,credential_id,state,created_at,updated_at) VALUES (?,?,1,1,1,31,'completed',?,?)`, attemptID, internalCallID, time.Now(), time.Now()).Error; err != nil {
		t.Fatalf("create attempt: %v", err)
	}
	var requestLogID uint
	if err := db.Raw(`SELECT COALESCE(MAX(id), 0) + 1 FROM gw_channel_request_logs`).Scan(&requestLogID).Error; err != nil {
		t.Fatalf("allocate request log id: %v", err)
	}
	if err := db.Exec(`INSERT INTO gw_channel_request_logs(id,attempt_id,action,http_status,duration_ms,error_code,request_bytes_complete,response_bytes_complete,created_at) VALUES (?,?,'submit',200,12,'',1,1,?)`, requestLogID, attemptID, time.Now()).Error; err != nil {
		t.Fatalf("create request log: %v", err)
	}
	return requestLogID
}

func TestPlaygroundGetDebugRejectsUnownedLogs(t *testing.T) {
	t.Run("different user", func(t *testing.T) {
		db := setupPlaygroundDebugTestDB(t)
		token := createPlaygroundDebugToken(t, db, 10, "debug-token-current-user")
		otherToken := createPlaygroundDebugToken(t, db, 11, "debug-token-other-user")
		requestLogID := createPlaygroundDebugLog(t, db, otherToken.UserID, otherToken.ID, "debug-call-other-user")

		response := requestPlaygroundDebug(token.UserID, token.ID, requestLogID)
		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
		}
	})

	t.Run("different token conversation", func(t *testing.T) {
		db := setupPlaygroundDebugTestDB(t)
		currentToken := createPlaygroundDebugToken(t, db, 20, "debug-token-current")
		otherToken := createPlaygroundDebugToken(t, db, 20, "debug-token-other")
		requestLogID := createPlaygroundDebugLog(t, db, otherToken.UserID, otherToken.ID, "debug-call-other-token")

		response := requestPlaygroundDebug(currentToken.UserID, currentToken.ID, requestLogID)
		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
		}
	})
}

func TestPlaygroundGetDebugAllowsOwnedLog(t *testing.T) {
	db := setupPlaygroundDebugTestDB(t)
	token := createPlaygroundDebugToken(t, db, 30, "debug-token-owned")
	requestLogID := createPlaygroundDebugLog(t, db, token.UserID, token.ID, "debug-call-owned")

	response := requestPlaygroundDebug(token.UserID, token.ID, requestLogID)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
}
