package admin

import (
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

func modelMetaRouter(actor uint) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if actor != 0 {
			c.Set("user_id", actor)
		}
	})
	router.PATCH("/model-meta/:model_name", UpdateUnifiedModelMeta)
	return router
}

const modelMetaBody = `{"display_name":"Doubao Seed 1.6","group_name":"火山","sort":20,"status":1,"expected_version":3}`

func TestModelMetaEndpointUpdatesPresentation(t *testing.T) {
	mock := unifiedChannelMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT display_name,group_name,sort,status,config_version FROM gw_model_meta").WithArgs("doubao-seed-1.6").
		WillReturnRows(sqlmock.NewRows([]string{"display_name", "group_name", "sort", "status", "config_version"}).AddRow("豆包 Seed 1.6", "火山", 10, 1, 3))
	mock.ExpectExec("UPDATE gw_model_meta").WithArgs("Doubao Seed 1.6", "火山", 20, 1, sqlmock.AnyArg(), "doubao-seed-1.6", 3).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO audit_events").WillReturnResult(sqlmock.NewResult(4, 1))
	mock.ExpectCommit()
	response := httptest.NewRecorder()
	modelMetaRouter(7).ServeHTTP(response, httptest.NewRequest("PATCH", "/model-meta/doubao-seed-1.6", strings.NewReader(modelMetaBody)))
	// The response reports the new token so the console can issue a second edit
	// without re-reading the list.
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"config_version":4`) {
		t.Fatalf("code=%d body=%s", response.Code, response.Body)
	}
}

func TestModelMetaEndpointReportsMissingModel(t *testing.T) {
	mock := unifiedChannelMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT display_name,group_name,sort,status,config_version FROM gw_model_meta").WithArgs("gpt-5").WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()
	response := httptest.NewRecorder()
	modelMetaRouter(7).ServeHTTP(response, httptest.NewRequest("PATCH", "/model-meta/gpt-5", strings.NewReader(modelMetaBody)))
	if response.Code != 404 {
		t.Fatalf("code=%d body=%s", response.Code, response.Body)
	}
}

func TestModelMetaEndpointRejectsStaleVersion(t *testing.T) {
	mock := unifiedChannelMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT display_name,group_name,sort,status,config_version FROM gw_model_meta").WithArgs("doubao-seed-1.6").
		WillReturnRows(sqlmock.NewRows([]string{"display_name", "group_name", "sort", "status", "config_version"}).AddRow("豆包 Seed 1.6", "火山", 10, 1, 9))
	mock.ExpectRollback()
	response := httptest.NewRecorder()
	modelMetaRouter(7).ServeHTTP(response, httptest.NewRequest("PATCH", "/model-meta/doubao-seed-1.6", strings.NewReader(modelMetaBody)))
	if response.Code != 409 {
		t.Fatalf("code=%d body=%s", response.Code, response.Body)
	}
}

func TestModelMetaEndpointValidatesBody(t *testing.T) {
	for _, body := range []string{
		`{"display_name":"X","status":1}`,
		`{"status":1,"expected_version":3}`,
		// Trimmed to empty by the handler, so it is a missing name rather than a
		// padded one. Padding itself is accepted and stripped, matching every other
		// display name in this API.
		`{"display_name":"   ","status":1,"expected_version":3}`,
		`{"display_name":"X\nY","status":1,"expected_version":3}`,
		`{"display_name":"X","status":2,"expected_version":3}`,
		`{"display_name":"X","sort":-1,"status":1,"expected_version":3}`,
		`{"display_name":"X","status":1,"expected_version":3,"thinking_config":{}}`,
		`{}`, `{} {}`, `null`,
	} {
		response := httptest.NewRecorder()
		modelMetaRouter(7).ServeHTTP(response, httptest.NewRequest("PATCH", "/model-meta/doubao-seed-1.6", strings.NewReader(body)))
		if response.Code != 400 {
			t.Fatalf("body=%s code=%d response=%s", body, response.Code, response.Body)
		}
	}
}

// thinking_config, max_tokens and features are deliberately not editable here:
// they change what a request sends upstream, so they are not presentation. The
// unknown-field rejection above is what keeps them out.
func TestModelMetaEndpointRequiresAdmin(t *testing.T) {
	response := httptest.NewRecorder()
	modelMetaRouter(0).ServeHTTP(response, httptest.NewRequest("PATCH", "/model-meta/doubao-seed-1.6", strings.NewReader(modelMetaBody)))
	if response.Code != 403 {
		t.Fatalf("code=%d body=%s", response.Code, response.Body)
	}
}
