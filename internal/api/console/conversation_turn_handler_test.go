package console

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestGetConversationTurnsUsesIndependentOwnedPagination(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Conversation{}, &model.ConversationTurn{}, &model.ConversationItem{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	conversation := &model.Conversation{UserID: 10, TokenID: 20, Status: 1}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatal(err)
	}
	turn := &model.ConversationTurn{
		ConversationID: conversation.ID, Sequence: 1, CallID: "call-handler",
		Status: model.ConversationTurnCompleted,
	}
	if err := db.Create(turn).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ConversationItem{
		ConversationID: conversation.ID, TurnID: turn.ID, TurnSequence: turn.Sequence,
		Direction: model.ConversationItemInput, Ordinal: 0,
		CanonicalJSON: datatypes.JSON(`{"type":"message","role":"user"}`),
	}).Error; err != nil {
		t.Fatal(err)
	}

	request := func(userID uint) *httptest.ResponseRecorder {
		gin.SetMode(gin.TestMode)
		router := gin.New()
		router.GET("/api/conversations/:id/turns", func(c *gin.Context) {
			c.Set(middleware.ContextKeyUserID, userID)
			c.Set(middleware.ContextKeyUserRole, string(model.UserRoleUser))
			GetConversationTurns(c)
		})
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("/api/conversations/%d/turns?page=1&page_size=1", conversation.ID), nil))
		return recorder
	}

	owned := request(conversation.UserID)
	if owned.Code != http.StatusOK {
		t.Fatalf("owned status = %d; body=%s", owned.Code, owned.Body.String())
	}
	var body struct {
		Data struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(owned.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Total != 1 || len(body.Data.Items) != 1 || body.Data.Items[0]["id"] != fmt.Sprint(turn.ID) {
		t.Fatalf("turn response = %#v", body.Data)
	}
	if _, exposed := body.Data.Items[0]["request_log_id"]; exposed {
		t.Fatal("user response exposed request_log_id")
	}

	forbidden := request(conversation.UserID + 1)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("foreign status = %d, want %d", forbidden.Code, http.StatusForbidden)
	}
}
