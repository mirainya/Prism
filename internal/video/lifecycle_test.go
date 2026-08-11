package video

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func TestFailTaskRefundsReservationAndFailsCall(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.Token{}, &model.APICall{}, &model.APICallAttempt{},
		&model.BillingLog{}, &model.BalanceEntry{}, &model.ConversationProjectionOutbox{},
		&VideoTask{},
	); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	user := &model.User{Username: "video_lifecycle_user", Password: "test", Balance: decimal.NewFromInt(10), Status: 1}
	if err := db.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	token := &model.Token{UserID: user.ID, Key: "video_lifecycle_token", Balance: decimal.NewFromInt(10), Status: 1}
	if err := db.Create(token).Error; err != nil {
		t.Fatal(err)
	}

	call, err := service.NewAPICallService().StartCall(&service.StartCallRequest{
		UserID: user.ID, TokenID: token.ID, Endpoint: "/v1/videos/generations",
		Operation: "videos.generate", Model: "seedance-2.0", Background: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := service.NewAPICallService().StartAttempt(&service.StartAttemptRequest{
		CallID: call.ID, RouteKind: model.APICallRouteVideo, Stage: model.APICallStageSubmit,
		Transport: model.UpstreamTransportVideoGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	cost := decimal.NewFromInt(2)
	if err := service.NewBillingService().DeductWithBillingContext(
		token.ID, user.ID, cost, "video-lifecycle:reserve",
		service.BillingContext{CallID: call.ID, AttemptID: attempt.ID, Phase: model.BillingPhaseReserve},
	); err != nil {
		t.Fatal(err)
	}
	task := &VideoTask{
		ID: "video_lifecycle_task", CallID: call.ID, UserID: user.ID, TokenID: token.ID,
		Model: "seedance-2.0", Status: VideoTaskStatusSubmitted, TaskMode: "text",
		AdapterType: "seedance", EstimatedCost: cost, BillingStatus: "reserved",
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}

	committed, err := FailTask(context.Background(), task.ID, "upstream failed")
	if err != nil {
		t.Fatal(err)
	}
	if !committed {
		t.Fatal("failure transition did not commit")
	}
	if err := db.First(task, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != VideoTaskStatusFailed || task.BillingStatus != "refunded" {
		t.Fatalf("task status=%s billing=%s", task.Status, task.BillingStatus)
	}
	if err := db.First(call, "id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if call.Status != model.APICallStatusFailed || !call.RefundedAmount.Equal(cost) {
		t.Fatalf("call status=%s refunded=%s", call.Status, call.RefundedAmount)
	}
	if err := db.First(token, token.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(user, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !token.Balance.Equal(decimal.NewFromInt(10)) || !user.Balance.Equal(decimal.NewFromInt(10)) {
		t.Fatalf("balances token=%s user=%s", token.Balance, user.Balance)
	}
}
