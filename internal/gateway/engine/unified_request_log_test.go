package engine

import (
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/routing"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/gateway/transport"
)

func TestUnifiedCapacityIsNotReportedAsUpstreamFailure(t *testing.T) {
	err := unifiedAdmissionError(repository.ErrConcurrencyLimit)
	app, ok := domain.IsAppError(err)
	if !ok || app.HTTPStatus != 429 || app.Code != "credential_capacity_exhausted" || !errors.Is(err, repository.ErrConcurrencyLimit) {
		t.Fatalf("capacity error=%v", err)
	}
}

func TestUnifiedLoggingRequiresFixedAttempt(t *testing.T) {
	route := &routing.RouteResult{ReleaseID: 1, OperationContractID: 1, ModelOperationID: 1, SKUID: 1, RouteID: 1, OfferingID: 1, ProductTransportID: 1, CredentialPoolID: 1, CredentialID: 1, CredentialVersionID: 1, PurposeGrantID: 1}
	if _, err := StartRequestLog(route, transport.PreparedRequest{}, transport.OperationChat); err == nil {
		t.Fatal("unified request bypassed capacity ownership")
	}
}

func TestUnifiedRequestCompletionReleasesHTTPNotTaskCapacity(t *testing.T) {
	for _, tc := range []struct {
		name        string
		hasResponse bool
		err         error
		status      string
	}{
		{"completed", true, nil, "response_recorded"},
		{"billing_failed_after_response", true, errors.New("billing failure"), "response_recorded"},
		{"connection_lost", false, errors.New("connection lost"), "unknown"},
		{"explicit_http_rejection", false, statusError{}, "response_recorded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, _ := sqlmock.New()
			defer db.Close()
			store, _ := repository.New(db)
			runtime, _ := gatewayruntime.New(store)
			log := &unifiedRequestLog{service: runtime, id: 8}
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT status FROM gw_channel_request_logs").WithArgs(uint64(8)).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("dispatching"))
			if tc.status == "response_recorded" {
				mock.ExpectQuery("SELECT status FROM gw_channel_request_logs").WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("dispatching"))
				mock.ExpectExec("UPDATE gw_channel_request_logs").WillReturnResult(sqlmock.NewResult(0, 1))
			}
			current := "dispatching"
			if tc.status == "response_recorded" {
				current = "sent"
			}
			mock.ExpectQuery("SELECT status FROM gw_channel_request_logs").WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(current))
			mock.ExpectExec("UPDATE gw_channel_request_logs").WithArgs(tc.status, nil, false, false, sqlmock.AnyArg(), uint64(10), sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(8)).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT id,state FROM gw_credential_slots WHERE request_log_id").WithArgs(uint64(8)).WillReturnRows(sqlmock.NewRows([]string{"id", "state"}).AddRow(9, "active"))
			mock.ExpectQuery("SELECT state,scope FROM gw_credential_slots").WithArgs(uint64(9)).WillReturnRows(sqlmock.NewRows([]string{"state", "scope"}).AddRow("active", "request"))
			mock.ExpectExec("UPDATE gw_credential_slots").WithArgs("released", sqlmock.AnyArg(), uint64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			if err := log.finish(tc.hasResponse, 0, tc.err, 10*time.Millisecond); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
