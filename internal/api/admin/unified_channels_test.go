package admin

import (
	"database/sql"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func unifiedChannelMock(t *testing.T) sqlmock.Sqlmock {
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
		db.Close()
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})
	return mock
}

func TestUnifiedChannelsPaginationAndBoundSearch(t *testing.T) {
	mock := unifiedChannelMock(t)
	search := "q' OR 1=1 --"
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM gateway_channels c").WithArgs("active", "active", search, search, search).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(21))
	mock.ExpectQuery("SELECT c.id,c.channel_code").WithArgs("active", "active", search, search, search, 20, 20).WillReturnRows(sqlmock.NewRows([]string{"id", "code", "name", "status", "created", "pools", "credentials"}).AddRow(1, "channel", "Name", "active", time.Now(), 2, 3))
	router := gin.New()
	router.GET("/channels", UnifiedChannels)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/channels?page=2&page_size=20&status=active", nil)
	query := req.URL.Query()
	query.Set("q", search)
	req.URL.RawQuery = query.Encode()
	router.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total":21`) || !strings.Contains(w.Body.String(), `"pool_count":2`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
}

func TestUnifiedChannelListsDoNotHideDatabaseErrors(t *testing.T) {
	mock := unifiedChannelMock(t)
	mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("db failed"))
	router := gin.New()
	router.GET("/channels", UnifiedChannels)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/channels", nil))
	if w.Code != 500 || strings.Contains(w.Body.String(), "db failed") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
}

func TestUnifiedChannelPoolsRequireParent(t *testing.T) {
	mock := unifiedChannelMock(t)
	mock.ExpectQuery("SELECT id FROM gateway_channels").WithArgs(44).WillReturnError(sql.ErrNoRows)
	router := gin.New()
	router.GET("/channels/:id/pools", UnifiedChannelPools)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/channels/44/pools", nil))
	if w.Code != 404 {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestUnifiedChannelBodyRejectsUnknownFieldsAndMultipleDocuments(t *testing.T) {
	for _, body := range []string{`{"channel_code":"code","display_name":"Name","api_key":"must-not-store"}`, `{} {}`, strings.Repeat("a", 17000)} {
		router := gin.New()
		router.POST("/channels", CreateUnifiedChannel)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("POST", "/channels", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body)
		}
	}
}

func TestUnifiedChannelWriteRequiresActor(t *testing.T) {
	router := gin.New()
	router.POST("/channels", CreateUnifiedChannel)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/channels", strings.NewReader(`{"channel_code":"code","display_name":"Name"}`)))
	if w.Code != 403 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
}
