package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

type deliveryReconcilerFunc func(context.Context, repository.OutboxItem) error

func (f deliveryReconcilerFunc) ReconcileDelivery(ctx context.Context, item repository.OutboxItem) error {
	return f(ctx, item)
}

func TestProcessDeliveryOneDeadLettersOnlyPermanentEvidenceFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	service, _ := NewWithReadiness(store, func(context.Context) error { return nil })
	available := time.Now().UTC().Add(-time.Second)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id,result_delivery_id,action_seq,action,state_version,attempt_count,available_at,status,lease_expires_at FROM gw_async_outbox").
		WillReturnRows(sqlmock.NewRows([]string{"id", "result_delivery_id", "action_seq", "action", "state_version", "attempt_count", "available_at", "status", "lease_expires_at"}).AddRow(11, 7, 2, "reconcile_delivery", 4, 0, available, "pending", nil))
	mock.ExpectExec("UPDATE gw_async_outbox SET status='dispatching'").
		WithArgs("delivery-worker", sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(11), uint64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT 1 FROM gw_async_outbox").
		WithArgs(uint64(11), "delivery-worker", uint64(1), uint64(2), uint64(4), "reconcile_delivery", nil, nil, nil, nil, uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectExec("UPDATE gw_async_outbox SET status='dead_letter'").
		WithArgs("source_expiry_unknown", sqlmock.AnyArg(), uint64(11), "delivery-worker", uint64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE gw_result_deliveries SET retry_at=NULL").
		WithArgs(sqlmock.AnyArg(), uint64(7), uint64(4), uint64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	worked, err := service.ProcessDeliveryOne(context.Background(), "delivery-worker", time.Minute, time.Second, deliveryReconcilerFunc(func(_ context.Context, item repository.OutboxItem) error {
		if item.ResultDeliveryID != 7 || item.Action != "reconcile_delivery" {
			t.Fatalf("item=%+v", item)
		}
		return &PermanentDispatchError{Code: "source_expiry_unknown"}
	}))
	if !worked || err == nil || err.Error() != "source_expiry_unknown" {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
