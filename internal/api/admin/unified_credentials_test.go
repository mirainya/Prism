package admin

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

func TestCredentialPurposesAreBoundedToPageAndErrorsPropagate(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	items := []gin.H{{"id": int64(4)}, {"id": int64(9)}}
	mock.ExpectQuery("SELECT credential_id,purpose FROM gw_credential_purpose_grants WHERE credential_id IN \\(\\?,\\?\\)").WithArgs(4, 9).WillReturnRows(sqlmock.NewRows([]string{"id", "purpose"}).AddRow(4, "catalog_discovery").AddRow(4, "execution"))
	if err := appendUnifiedCredentialPurposes(context.Background(), db, items); err != nil {
		t.Fatal(err)
	}
	if len(items[0]["purposes"].([]string)) != 2 || len(items[1]["purposes"].([]string)) != 0 {
		t.Fatal(items)
	}
	mock.ExpectQuery("SELECT credential_id,purpose").WillReturnError(errors.New("query failed"))
	if err := appendUnifiedCredentialPurposes(context.Background(), db, items); err == nil {
		t.Fatal("grant query error hidden")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUnifiedCredentialWriteValidation(t *testing.T) {
	for _, body := range []string{
		`{"credential_code":"test","secret":"private","weight":1,"purposes":[]}`,
		`{"credential_code":"test","secret":"private","weight":1,"purposes":["execution","execution"]}`,
		`{"credential_code":"test","secret":"private\r\nInjected: yes","weight":1,"purposes":["execution"]}`,
		`{"credential_code":"test","secret":"private","weight":0,"purposes":["execution"]}`,
		`{"credential_code":"test","secret":"private","weight":1,"purposes":["execution"],"extra":"private"}`,
		`null`, `{}`, `{} {}`,
	} {
		router := gin.New()
		router.POST("/pools/:id", CreateUnifiedCredential)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("POST", "/pools/1", strings.NewReader(body)))
		if response.Code != 400 || strings.Contains(response.Body.String(), "private") {
			t.Fatalf("code=%d body=%s", response.Code, response.Body)
		}
	}
}

func TestUnifiedCredentialWritesRequireAdmin(t *testing.T) {
	router := gin.New()
	router.POST("/pools/:id", CreateUnifiedCredential)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("POST", "/pools/1", strings.NewReader(`{"credential_code":"test","secret":"private","weight":1,"purposes":["execution"]}`)))
	if response.Code != 403 {
		t.Fatalf("code=%d", response.Code)
	}
}

func TestUnifiedCredentialListFiltersPoolAndRejectsInjection(t *testing.T) {
	mock := unifiedChannelMock(t)
	mock.ExpectQuery("SELECT COUNT.*FROM gw_credentials c WHERE c.credential_pool_id=\\?").WithArgs(9).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT c.id,c.channel_id").WithArgs(9, 50, 50).WillReturnRows(sqlmock.NewRows([]string{"id", "channel_id", "pool_id", "code", "state", "version", "requests", "tasks", "weight", "current_version", "pool_code", "pool_name"}))
	router := gin.New()
	router.GET("/credentials", UnifiedGatewayCredentials)
	for _, query := range []string{"pool_id=9&page=2&page_size=50", "pool_id=0", "pool_id=1%20OR%201=1", "pool_id=-1"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/credentials?"+query, nil))
		want := 400
		if strings.HasPrefix(query, "pool_id=9") {
			want = 200
		}
		if response.Code != want {
			t.Fatalf("query=%s code=%d", query, response.Code)
		}
	}
}

func TestUnifiedCredentialKeyringErrorIsActionableAndSafe(t *testing.T) {
	router := gin.New()
	router.GET("/", func(c *gin.Context) { unifiedChannelError(c, repository.ErrCredentialEncryptionUnavailable) })
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/", nil))
	if response.Code != 503 || strings.Contains(response.Body.String(), "PRISM_GATEWAY") {
		t.Fatalf("code=%d body=%s", response.Code, response.Body)
	}
}
