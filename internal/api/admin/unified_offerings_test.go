package admin

import (
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

// offeringRuntimeStateRouter installs the handler behind an authenticated admin,
// because the endpoint's whole purpose is attributing an emergency change to a
// person.
func offeringRuntimeStateRouter(actor uint) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if actor != 0 {
			c.Set("user_id", actor)
		}
	})
	router.PATCH("/offerings/:offering_id/runtime-state", SetUnifiedOfferingRuntimeState)
	return router
}

func TestOfferingRuntimeStateEndpointDisablesPublishedOffering(t *testing.T) {
	mock := unifiedChannelMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT r.status FROM gw_offerings o").WithArgs(1201).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("published"))
	mock.ExpectQuery("SELECT release_id FROM gw_offerings").WithArgs(1201).WillReturnRows(sqlmock.NewRows([]string{"release_id"}).AddRow(4))
	mock.ExpectQuery("SELECT state,state_version FROM gw_offering_runtime_state").WithArgs(4, 1201).WillReturnRows(sqlmock.NewRows([]string{"state", "version"}).AddRow("active", 3))
	mock.ExpectExec("UPDATE gw_offering_runtime_state").WithArgs("disabled", 4, "upstream_outage", sqlmock.AnyArg(), 4, 1201, 3).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO gw_offering_state_events").WillReturnResult(sqlmock.NewResult(9, 1))
	mock.ExpectExec("INSERT INTO audit_events").WillReturnResult(sqlmock.NewResult(10, 1))
	mock.ExpectCommit()
	response := httptest.NewRecorder()
	offeringRuntimeStateRouter(7).ServeHTTP(response, httptest.NewRequest("PATCH", "/offerings/1201/runtime-state",
		strings.NewReader(`{"state":"disabled","reason_code":"upstream_outage","expected_version":3}`)))
	if response.Code != 200 {
		t.Fatalf("code=%d body=%s", response.Code, response.Body)
	}
}

// A draft's offerings are not routable, so a runtime-state change against one
// would record an event that means nothing and sidestep the draft's own
// config_version discipline.
func TestOfferingRuntimeStateEndpointRejectsUnpublishedRelease(t *testing.T) {
	for _, status := range []string{"draft", "retired"} {
		t.Run(status, func(t *testing.T) {
			mock := unifiedChannelMock(t)
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT r.status FROM gw_offerings o").WithArgs(1201).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(status))
			mock.ExpectRollback()
			response := httptest.NewRecorder()
			offeringRuntimeStateRouter(7).ServeHTTP(response, httptest.NewRequest("PATCH", "/offerings/1201/runtime-state",
				strings.NewReader(`{"state":"disabled","reason_code":"upstream_outage","expected_version":3}`)))
			if response.Code != 409 {
				t.Fatalf("code=%d body=%s", response.Code, response.Body)
			}
		})
	}
}

func TestOfferingRuntimeStateEndpointReportsMissingOffering(t *testing.T) {
	mock := unifiedChannelMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT r.status FROM gw_offerings o").WithArgs(4242).WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()
	response := httptest.NewRecorder()
	offeringRuntimeStateRouter(7).ServeHTTP(response, httptest.NewRequest("PATCH", "/offerings/4242/runtime-state",
		strings.NewReader(`{"state":"disabled","reason_code":"upstream_outage","expected_version":3}`)))
	if response.Code != 404 {
		t.Fatalf("code=%d body=%s", response.Code, response.Body)
	}
}

// Bad input is refused before any transaction opens, so a malformed emergency
// disable cannot leave a lock waiting on the runtime-state row.
func TestOfferingRuntimeStateEndpointValidatesBody(t *testing.T) {
	for _, body := range []string{
		`{"state":"paused","reason_code":"x","expected_version":1}`,
		`{"state":"disabled","expected_version":1}`,
		`{"state":"disabled","reason_code":"x"}`,
		`{"state":"disabled","reason_code":"bad code","expected_version":1}`,
		`{"state":"disabled","reason_code":"x","expected_version":1,"extra":1}`,
		`{}`, `{} {}`, `null`,
	} {
		response := httptest.NewRecorder()
		offeringRuntimeStateRouter(7).ServeHTTP(response, httptest.NewRequest("PATCH", "/offerings/1201/runtime-state", strings.NewReader(body)))
		if response.Code != 400 {
			t.Fatalf("body=%s code=%d response=%s", body, response.Code, response.Body)
		}
	}
}

func TestOfferingRuntimeStateEndpointRequiresAdmin(t *testing.T) {
	response := httptest.NewRecorder()
	offeringRuntimeStateRouter(0).ServeHTTP(response, httptest.NewRequest("PATCH", "/offerings/1201/runtime-state",
		strings.NewReader(`{"state":"disabled","reason_code":"upstream_outage","expected_version":3}`)))
	if response.Code != 403 {
		t.Fatalf("code=%d body=%s", response.Code, response.Body)
	}
}
