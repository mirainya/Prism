package open

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
)

func TestGetImageTaskReturnsOwnedQueuedProjection(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	previousStore := unifiedVideoStore
	unifiedVideoStore = store
	t.Cleanup(func() { unifiedVideoStore = previousStore })

	now := time.Date(2026, 9, 20, 3, 20, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT c.id,c.status,t.status,t.progress,t.parameter_summary,t.created_at,t.updated_at").
		WithArgs("task-public-id", uint(11), uint(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "call_status", "task_status", "progress", "summary", "created_at", "updated_at"}).
			AddRow(uint64(31), "in_progress", "queued", uint8(0), []byte(`{"model":"image-model","operation":"images.generate","response_format":"url"}`), now, now))

	context, recorder := newImageTaskTestContext("task-public-id")
	GetImageTask(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	for _, expected := range []string{`"id":"task-public-id"`, `"status":"queued"`, `"model":"image-model"`, `"operation":"images.generate"`} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Fatalf("body = %s, want %s", recorder.Body.String(), expected)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetImageTaskDoesNotExposeOtherCapabilityKinds(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	previousStore := unifiedVideoStore
	unifiedVideoStore = store
	t.Cleanup(func() { unifiedVideoStore = previousStore })

	now := time.Now().UTC()
	mock.ExpectQuery("SELECT c.id,c.status,t.status,t.progress,t.parameter_summary,t.created_at,t.updated_at").
		WithArgs("other-capability", uint(11), uint(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "call_status", "task_status", "progress", "summary", "created_at", "updated_at"}).
			AddRow(uint64(32), "in_progress", "queued", uint8(0), []byte(`{"model":"other-model","operation":"audio.generate","response_format":"url"}`), now, now))

	context, recorder := newImageTaskTestContext("other-capability")
	GetImageTask(context)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func newImageTaskTestContext(id string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/v1/images/tasks/"+id, nil)
	context.Params = gin.Params{{Key: "id", Value: id}}
	context.Set(middleware.ContextKeyToken, &model.Token{BaseModel: model.BaseModel{ID: 7}, UserID: 11})
	return context, recorder
}
