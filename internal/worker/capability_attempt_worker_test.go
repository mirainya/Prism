package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var capabilityWorkerDBCounter int64

type progressReply struct {
	result provider.ProgressResult
	err    error
}

type scriptedCapabilityProvider struct {
	submitResult provider.SubmitResult
	submitErr    error
	submitHook   func()
	submitCalls  int
	progress     []progressReply
	progressHook func()
	progressCall int
}

func (p *scriptedCapabilityProvider) Submit(context.Context, provider.SubmitRequest) (provider.SubmitResult, error) {
	p.submitCalls++
	if p.submitHook != nil {
		p.submitHook()
	}
	return p.submitResult, p.submitErr
}

func (p *scriptedCapabilityProvider) GetProgress(context.Context, string) (provider.ProgressResult, error) {
	if p.progressHook != nil {
		p.progressHook()
	}
	if p.progressCall >= len(p.progress) {
		return provider.ProgressResult{}, errors.New("unexpected progress call")
	}
	reply := p.progress[p.progressCall]
	p.progressCall++
	return reply.result, reply.err
}

func (p *scriptedCapabilityProvider) ParseCallback(context.Context, []byte) (provider.ProgressResult, string, error) {
	return provider.ProgressResult{}, "", nil
}

func TestTaskSubmitAttemptSuccessLinksCallBillingAndRequestLog(t *testing.T) {
	db, task, channel, endpoint := setupCapabilityWorkerTest(t, model.TaskStatusPending, model.ModeSync)
	metadata := provider.RequestMetadata{
		Method: httpMethodPost, RequestPath: "/actual/submit", StatusCode: 201,
		DurationMs: 17, RequestAt: time.Now().Add(-time.Second),
	}
	fake := &scriptedCapabilityProvider{submitResult: provider.SubmitResult{
		RequestMetadata: metadata,
		ProviderTaskID:  "provider-submit-success",
		URLs:            []string{"https://result.example/image.png"},
	}}
	newProvider = func(*model.Channel, *model.ChannelAccount, *model.Endpoint) (provider.Provider, error) {
		return fake, nil
	}

	job, err := NewTaskSubmit(task.ID)
	if err != nil {
		t.Fatalf("build submit job: %v", err)
	}
	if err := HandleTaskSubmit(context.Background(), job); err != nil {
		t.Fatalf("handle submit: %v", err)
	}

	attempts := loadCapabilityAttempts(t, db, task.CallID)
	if len(attempts) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(attempts))
	}
	attempt := attempts[0]
	assertCapabilityAttempt(t, attempt, task, endpoint, model.APICallStageSubmit)
	if attempt.Status != model.APICallAttemptStatusCompleted || attempt.HTTPStatus != 201 ||
		attempt.RequestPath != metadata.RequestPath || attempt.DurationMs != metadata.DurationMs {
		t.Fatalf("completed submit attempt = %#v", attempt)
	}
	assertTaskAndCallTerminal(t, db, task.ID, task.CallID, model.TaskStatusSuccess, model.APICallStatusCompleted, attempt.ID)
	assertWorkerBillingAttempt(t, db, task.TaskNo+":reserve", attempt.ID)
	assertWorkerBillingAttempt(t, db, task.TaskNo+":settle", attempt.ID)
	assertCapabilityRequestLog(t, db, task, channel, attempt, metadata, "")
}

func TestTaskSubmitAttemptFailureEndsAttemptBeforeTask(t *testing.T) {
	db, task, channel, endpoint := setupCapabilityWorkerTest(t, model.TaskStatusPending, model.ModeSync)
	metadata := provider.RequestMetadata{
		Method: httpMethodPost, RequestPath: "/actual/submit", StatusCode: 502,
		DurationMs: 23, RequestAt: time.Now().Add(-time.Second),
	}
	upstreamErr := &domain.UpstreamError{StatusCode: 502, Body: "submit unavailable"}
	newProvider = func(*model.Channel, *model.ChannelAccount, *model.Endpoint) (provider.Provider, error) {
		return &scriptedCapabilityProvider{
			submitResult: provider.SubmitResult{RequestMetadata: metadata},
			submitErr:    upstreamErr,
		}, nil
	}

	job, _ := NewTaskSubmit(task.ID)
	if err := HandleTaskSubmit(context.Background(), job); err != nil {
		t.Fatalf("handle failed submit: %v", err)
	}

	attempt := loadCapabilityAttempts(t, db, task.CallID)[0]
	assertCapabilityAttempt(t, attempt, task, endpoint, model.APICallStageSubmit)
	if attempt.Status != model.APICallAttemptStatusFailed || attempt.HTTPStatus != 502 ||
		!attempt.ErrorRetryable || attempt.ErrorCode != "submit_request_failed" {
		t.Fatalf("failed submit attempt = %#v", attempt)
	}
	assertTaskAndCallTerminal(t, db, task.ID, task.CallID, model.TaskStatusFailed, model.APICallStatusFailed, attempt.ID)
	assertWorkerBillingAttempt(t, db, task.TaskNo+":reserve", attempt.ID)
	assertWorkerBillingAttempt(t, db, task.TaskNo, attempt.ID)
	assertCapabilityRequestLog(t, db, task, channel, attempt, metadata, upstreamErr.Error())
}

func TestTaskSubmitMissingConfigurationFailsAndRefunds(t *testing.T) {
	db, task, _, _ := setupCapabilityWorkerTest(t, model.TaskStatusPending, model.ModeSync)
	if err := db.Delete(&model.ChannelAccount{}, task.AccountID).Error; err != nil {
		t.Fatal(err)
	}

	job, err := NewTaskSubmit(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := HandleTaskSubmit(context.Background(), job); err != nil {
		t.Fatalf("handle task with removed configuration: %v", err)
	}

	if attempts := loadCapabilityAttempts(t, db, task.CallID); len(attempts) != 0 {
		t.Fatalf("attempt count = %d, want 0", len(attempts))
	}
	assertTaskAndCallTerminal(t, db, task.ID, task.CallID, model.TaskStatusFailed, model.APICallStatusFailed, 0)
	assertWorkerBillingAttempt(t, db, task.TaskNo, 0)
	var deletedAccount model.ChannelAccount
	if err := db.Unscoped().First(&deletedAccount, task.AccountID).Error; err != nil {
		t.Fatal(err)
	}
	if deletedAccount.CurrentTasks != 0 {
		t.Fatalf("deleted account current_tasks = %d, want 0", deletedAccount.CurrentTasks)
	}
}

func TestTaskSubmitLedgerFinishFailureDoesNotRepeatUpstreamSubmit(t *testing.T) {
	db, task, channel, endpoint := setupCapabilityWorkerTest(t, model.TaskStatusPending, model.ModeSync)
	actualCost := decimal.RequireFromString("0.375")
	endpoint.InputPrice = actualCost
	if err := db.Model(endpoint).UpdateColumn("input_price", actualCost).Error; err != nil {
		t.Fatal(err)
	}
	fake := &scriptedCapabilityProvider{submitResult: provider.SubmitResult{
		RequestMetadata: provider.RequestMetadata{
			Method: httpMethodPost, RequestPath: "/actual/submit", StatusCode: 201, DurationMs: 5,
		},
		ProviderTaskID: "provider-submit-once",
		URLs:           []string{"https://result.example/once.png"},
	}}
	newProvider = func(*model.Channel, *model.ChannelAccount, *model.Endpoint) (provider.Provider, error) {
		return fake, nil
	}
	finishCalls := 0
	finishCapabilityAttempt = func(
		task *model.Task,
		channel *model.Channel,
		endpoint *model.Endpoint,
		attempt *model.APICallAttempt,
		stage string,
		metadata service.CapabilityAttemptMetadata,
		requestErr error,
	) error {
		finishCalls++
		if finishCalls == 1 {
			return errors.New("temporary ledger write failure")
		}
		return service.FinishCapabilityAttempt(task, channel, endpoint, attempt, stage, metadata, requestErr)
	}

	job, _ := NewTaskSubmit(task.ID)
	if err := HandleTaskSubmit(context.Background(), job); err == nil {
		t.Fatal("first submit unexpectedly ignored ledger failure")
	}
	if fake.submitCalls != 1 {
		t.Fatalf("upstream submit calls = %d, want 1", fake.submitCalls)
	}
	var pending model.Task
	if err := db.First(&pending, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if pending.Status != model.TaskStatusPending || len(pending.SubmitCheckpoint) == 0 {
		t.Fatalf("checkpointed task = status %s checkpoint %q", pending.Status, pending.SubmitCheckpoint)
	}
	checkpoint, err := service.DecodeTaskSubmitCheckpoint(pending.SubmitCheckpoint)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.FinalCost != actualCost.String() {
		t.Fatalf("checkpoint final cost = %q, want %q", checkpoint.FinalCost, actualCost.String())
	}
	if !checkpoint.IsSucceeded() {
		t.Fatalf("checkpoint state = %q, want succeeded", checkpoint.State)
	}
	attempt := loadCapabilityAttempts(t, db, task.CallID)[0]
	if attempt.Status != model.APICallAttemptStatusStarted {
		t.Fatalf("attempt after ledger failure = %s, want started", attempt.Status)
	}

	if err := HandleTaskSubmit(context.Background(), job); err != nil {
		t.Fatalf("resume submit checkpoint: %v", err)
	}
	if fake.submitCalls != 1 {
		t.Fatalf("upstream submit calls after recovery = %d, want 1", fake.submitCalls)
	}
	attempt = loadCapabilityAttempts(t, db, task.CallID)[0]
	if attempt.Status != model.APICallAttemptStatusCompleted || finishCalls != 2 {
		t.Fatalf("recovered attempt = status %s finish calls %d", attempt.Status, finishCalls)
	}
	assertTaskAndCallTerminal(t, db, task.ID, task.CallID, model.TaskStatusSuccess, model.APICallStatusCompleted, attempt.ID)
	assertCapabilityRequestLog(t, db, task, channel, attempt, fake.submitResult.RequestMetadata, "")
	var completed model.Task
	if err := db.First(&completed, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if len(completed.SubmitCheckpoint) != 0 {
		t.Fatalf("completed task retained submit checkpoint: %q", completed.SubmitCheckpoint)
	}
	var call model.APICall
	if err := db.First(&call, "id = ?", task.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if !completed.Cost.Equal(actualCost) || !call.FinalCost.Equal(actualCost) {
		t.Fatalf("recovered settlement cost = task %s call %s, want %s", completed.Cost, call.FinalCost, actualCost)
	}
}

func TestTaskSubmitUnknownOutcomeDoesNotRepeatUpstream(t *testing.T) {
	db, task, _, _ := setupCapabilityWorkerTest(t, model.TaskStatusPending, model.ModeSync)
	fake := &scriptedCapabilityProvider{
		submitResult: provider.SubmitResult{RequestMetadata: provider.RequestMetadata{
			Method: httpMethodPost, RequestPath: "/actual/submit",
		}},
		submitErr: errors.New("connection closed before a response was received"),
	}
	newProvider = func(*model.Channel, *model.ChannelAccount, *model.Endpoint) (provider.Provider, error) {
		return fake, nil
	}
	job, _ := NewTaskSubmit(task.ID)

	if err := HandleTaskSubmit(context.Background(), job); err != nil {
		t.Fatalf("handle ambiguous submit: %v", err)
	}
	if err := HandleTaskSubmit(context.Background(), job); err != nil {
		t.Fatalf("repeat ambiguous submit: %v", err)
	}
	if fake.submitCalls != 1 {
		t.Fatalf("upstream submit calls = %d, want 1", fake.submitCalls)
	}

	var failed model.Task
	if err := db.First(&failed, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if failed.Status != model.TaskStatusFailed ||
		!strings.Contains(failed.ErrorMessage, service.TaskSubmitOutcomeUnknownMessage) ||
		len(failed.SubmitCheckpoint) != 0 {
		t.Fatalf("ambiguous submit task = status %s error %q checkpoint %q",
			failed.Status, failed.ErrorMessage, failed.SubmitCheckpoint)
	}
}

func TestTaskSubmitInFlightRecoveryDoesNotRequireDeletedRoutingConfig(t *testing.T) {
	db, task, channel, endpoint := setupCapabilityWorkerTest(t, model.TaskStatusPending, model.ModeSync)
	fake := &scriptedCapabilityProvider{submitResult: provider.SubmitResult{
		RequestMetadata: provider.RequestMetadata{
			Method: httpMethodPost, RequestPath: "/actual/submit", StatusCode: 201,
		},
		URLs: []string{"https://result.example/unconfirmed.png"},
	}}
	newProvider = func(*model.Channel, *model.ChannelAccount, *model.Endpoint) (provider.Provider, error) {
		return fake, nil
	}
	saveTaskSubmitCheckpoint = func(taskID uint, leaseOwner string, checkpoint *service.TaskSubmitCheckpoint) error {
		if checkpoint.IsSucceeded() {
			return errors.New("simulated successful checkpoint write failure")
		}
		return taskService.SaveTaskSubmitCheckpoint(taskID, leaseOwner, checkpoint)
	}
	job, _ := NewTaskSubmit(task.ID)

	if err := HandleTaskSubmit(context.Background(), job); err == nil {
		t.Fatal("successful checkpoint write failure was ignored")
	}
	if fake.submitCalls != 1 {
		t.Fatalf("upstream submit calls after checkpoint failure = %d, want 1", fake.submitCalls)
	}
	var checkpointed model.Task
	if err := db.First(&checkpointed, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	checkpoint, err := service.DecodeTaskSubmitCheckpoint(checkpointed.SubmitCheckpoint)
	if err != nil || checkpoint == nil || !checkpoint.IsInFlight() {
		t.Fatalf("in-flight checkpoint = %#v err=%v", checkpoint, err)
	}

	if err := db.Delete(&model.Endpoint{}, endpoint.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&model.ChannelAccount{}, task.AccountID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&model.Channel{}, channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := HandleTaskSubmit(context.Background(), job); err != nil {
		t.Fatalf("recover in-flight submit without routing config: %v", err)
	}
	if fake.submitCalls != 1 {
		t.Fatalf("upstream submit calls after recovery = %d, want 1", fake.submitCalls)
	}
	var failed model.Task
	if err := db.First(&failed, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if failed.Status != model.TaskStatusFailed ||
		!strings.Contains(failed.ErrorMessage, service.TaskSubmitOutcomeUnknownMessage) {
		t.Fatalf("recovered task = status %s error %q", failed.Status, failed.ErrorMessage)
	}
}

func TestTaskSubmitSucceededCheckpointCompletesWithoutDeletedRoutingConfig(t *testing.T) {
	db, task, channel, endpoint := setupCapabilityWorkerTest(t, model.TaskStatusPending, model.ModeSync)
	fake := &scriptedCapabilityProvider{submitResult: provider.SubmitResult{
		RequestMetadata: provider.RequestMetadata{
			Method: httpMethodPost, RequestPath: "/actual/submit", StatusCode: 201,
		},
		ProviderTaskID: "provider-config-deleted",
		URLs:           []string{"https://result.example/config-deleted.png"},
	}}
	newProvider = func(*model.Channel, *model.ChannelAccount, *model.Endpoint) (provider.Provider, error) {
		return fake, nil
	}
	finishCalls := 0
	finishCapabilityAttempt = func(
		task *model.Task,
		channel *model.Channel,
		endpoint *model.Endpoint,
		attempt *model.APICallAttempt,
		stage string,
		metadata service.CapabilityAttemptMetadata,
		requestErr error,
	) error {
		finishCalls++
		if finishCalls == 1 {
			return errors.New("simulated ledger write failure")
		}
		return service.FinishCapabilityAttempt(task, channel, endpoint, attempt, stage, metadata, requestErr)
	}
	job, _ := NewTaskSubmit(task.ID)
	if err := HandleTaskSubmit(context.Background(), job); err == nil {
		t.Fatal("initial ledger failure was ignored")
	}
	if fake.submitCalls != 1 {
		t.Fatalf("upstream submit calls = %d, want 1", fake.submitCalls)
	}
	var checkpointed model.Task
	if err := db.First(&checkpointed, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	checkpoint, err := service.DecodeTaskSubmitCheckpoint(checkpointed.SubmitCheckpoint)
	if err != nil || checkpoint == nil || !checkpoint.IsSucceeded() || checkpoint.InteractionMode != model.ModeSync {
		t.Fatalf("successful checkpoint = %#v err=%v", checkpoint, err)
	}

	if err := db.Delete(&model.Endpoint{}, endpoint.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&model.ChannelAccount{}, task.AccountID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&model.Channel{}, channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := HandleTaskSubmit(context.Background(), job); err != nil {
		t.Fatalf("recover successful submit without routing config: %v", err)
	}
	if fake.submitCalls != 1 {
		t.Fatalf("upstream submit calls after recovery = %d, want 1", fake.submitCalls)
	}
	assertTaskAndCallTerminal(t, db, task.ID, task.CallID, model.TaskStatusSuccess, model.APICallStatusCompleted, checkpoint.AttemptID)
	var completed model.Task
	if err := db.First(&completed, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if len(completed.SubmitCheckpoint) != 0 || !strings.Contains(string(completed.Result), "config-deleted.png") {
		t.Fatalf("completed task = checkpoint %q result %s", completed.SubmitCheckpoint, completed.Result)
	}
}

func TestTaskSubmitConcurrentDeliveryCallsUpstreamOnce(t *testing.T) {
	_, task, _, _ := setupCapabilityWorkerTest(t, model.TaskStatusPending, model.ModeSync)
	started := make(chan struct{})
	release := make(chan struct{})
	fake := &scriptedCapabilityProvider{submitResult: provider.SubmitResult{
		RequestMetadata: provider.RequestMetadata{Method: httpMethodPost, RequestPath: "/actual/submit", StatusCode: 201},
		URLs:            []string{"https://result.example/concurrent.png"},
	}}
	fake.submitHook = func() {
		close(started)
		<-release
	}
	newProvider = func(*model.Channel, *model.ChannelAccount, *model.Endpoint) (provider.Provider, error) {
		return fake, nil
	}
	job, _ := NewTaskSubmit(task.ID)
	firstResult := make(chan error, 1)
	go func() { firstResult <- HandleTaskSubmit(context.Background(), job) }()
	<-started
	secondResult := make(chan error, 1)
	go func() { secondResult <- HandleTaskSubmit(context.Background(), job) }()
	select {
	case err := <-secondResult:
		if err != nil {
			t.Fatalf("second delivery: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second delivery did not observe the active lease")
	}
	close(release)
	if err := <-firstResult; err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if fake.submitCalls != 1 {
		t.Fatalf("upstream submit calls = %d, want 1", fake.submitCalls)
	}
}

func TestTaskSubmitInitialPollQueueFailureResumesFromCheckpoint(t *testing.T) {
	db, task, _, endpoint := setupCapabilityWorkerTest(t, model.TaskStatusPending, model.ModePoll)
	if err := db.Model(endpoint).UpdateColumn("poll_interval", 0).Error; err != nil {
		t.Fatal(err)
	}
	task.VendorTaskID = ""
	if err := db.Model(task).UpdateColumn("vendor_task_id", "").Error; err != nil {
		t.Fatal(err)
	}
	fake := &scriptedCapabilityProvider{submitResult: provider.SubmitResult{
		RequestMetadata: provider.RequestMetadata{
			Method: httpMethodPost, RequestPath: "/actual/submit", StatusCode: 201,
		},
		ProviderTaskID: "provider-poll-recovery",
	}}
	newProvider = func(*model.Channel, *model.ChannelAccount, *model.Endpoint) (provider.Provider, error) {
		return fake, nil
	}
	queueErr := errors.New("queue unavailable")
	requeueCalls := 0
	requeueTaskPoll = func(taskID uint, pollCount int, intervalSeconds int) error {
		requeueCalls++
		if taskID != task.ID || pollCount != 0 || intervalSeconds != DefaultPollInterval {
			t.Fatalf("requeue = task %d count %d interval %d", taskID, pollCount, intervalSeconds)
		}
		if requeueCalls == 1 {
			return queueErr
		}
		return nil
	}

	job, _ := NewTaskSubmit(task.ID)
	if err := HandleTaskSubmit(context.Background(), job); !errors.Is(err, queueErr) {
		t.Fatalf("first submit error = %v, want %v", err, queueErr)
	}
	var checkpointed model.Task
	if err := db.First(&checkpointed, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if checkpointed.Status != model.TaskStatusProcessing || len(checkpointed.SubmitCheckpoint) == 0 ||
		checkpointed.VendorTaskID != fake.submitResult.ProviderTaskID {
		t.Fatalf("checkpointed poll task = status %s vendor %q checkpoint %q",
			checkpointed.Status, checkpointed.VendorTaskID, checkpointed.SubmitCheckpoint)
	}

	if err := HandleTaskSubmit(context.Background(), job); err != nil {
		t.Fatalf("resume initial poll enqueue: %v", err)
	}
	if fake.submitCalls != 1 || requeueCalls != 2 {
		t.Fatalf("recovery calls = upstream %d queue %d, want 1/2", fake.submitCalls, requeueCalls)
	}
	var resumed model.Task
	if err := db.First(&resumed, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if resumed.Status != model.TaskStatusProcessing || len(resumed.SubmitCheckpoint) != 0 || resumed.PollCursor != 0 {
		t.Fatalf("resumed poll task = status %s cursor %d checkpoint %q",
			resumed.Status, resumed.PollCursor, resumed.SubmitCheckpoint)
	}
}

func TestTaskSubmitCallbackFailureTerminalizesInFlightAttempt(t *testing.T) {
	db, task, _, _ := setupCapabilityWorkerTest(t, model.TaskStatusPending, model.ModeCallback)
	previousConfig := config.C
	config.C = &config.Config{Server: config.ServerConfig{PublicURL: "https://prism.test"}}
	t.Cleanup(func() { config.C = previousConfig })
	fake := &scriptedCapabilityProvider{submitResult: provider.SubmitResult{
		RequestMetadata: provider.RequestMetadata{Method: httpMethodPost, RequestPath: "/actual/submit", StatusCode: 201},
		ProviderTaskID:  "provider-callback-race",
	}}
	fake.submitHook = func() {
		if _, err := taskService.UpdateTaskFail(task.ID, "callback reported failure"); err != nil {
			t.Fatalf("callback failure transition: %v", err)
		}
	}
	newProvider = func(*model.Channel, *model.ChannelAccount, *model.Endpoint) (provider.Provider, error) {
		return fake, nil
	}
	job, _ := NewTaskSubmit(task.ID)
	if err := HandleTaskSubmit(context.Background(), job); err != nil {
		t.Fatalf("submit after callback failure: %v", err)
	}
	attempts := loadCapabilityAttempts(t, db, task.CallID)
	if len(attempts) != 1 || attempts[0].Status != model.APICallAttemptStatusFailed || attempts[0].CompletedAt == nil {
		t.Fatalf("in-flight attempt was not terminalized: %#v", attempts)
	}
	assertTaskAndCallTerminal(t, db, task.ID, task.CallID, model.TaskStatusFailed, model.APICallStatusFailed, attempts[0].ID)
}

func TestTaskPollTransientErrorThenSuccessCreatesIndependentAttempts(t *testing.T) {
	db, task, channel, endpoint := setupCapabilityWorkerTest(t, model.TaskStatusProcessing, model.ModePoll)
	submitAttempt := seedCompletedSubmitAttempt(t, task, channel, endpoint)
	transientMetadata := provider.RequestMetadata{
		Method: httpMethodGet, RequestPath: "/actual/poll/provider-task", StatusCode: 503,
		DurationMs: 11, RequestAt: time.Now().Add(-2 * time.Second),
	}
	successMetadata := provider.RequestMetadata{
		Method: httpMethodGet, RequestPath: "/actual/poll/provider-task", StatusCode: 200,
		DurationMs: 19, RequestAt: time.Now().Add(-time.Second),
	}
	transientErr := &domain.UpstreamError{StatusCode: 503, Body: "temporary unavailable"}
	fake := &scriptedCapabilityProvider{progress: []progressReply{
		{
			result: provider.ProgressResult{RequestMetadata: transientMetadata},
			err:    transientErr,
		},
		{
			result: provider.ProgressResult{
				RequestMetadata: successMetadata,
				Status:          provider.StatusSuccess,
				URLs:            []string{"https://result.example/final.png"},
			},
		},
	}}
	newProvider = func(*model.Channel, *model.ChannelAccount, *model.Endpoint) (provider.Provider, error) {
		return fake, nil
	}
	requeueCalls := 0
	requeueTaskPoll = func(taskID uint, pollCount int, intervalSeconds int) error {
		requeueCalls++
		if taskID != task.ID || pollCount != 1 {
			t.Fatalf("requeue = task %d count %d", taskID, pollCount)
		}
		return nil
	}
	enqueueUpload = func(taskID uint, originURL string, urls []string, revisedPrompt ...string) error {
		current, err := taskService.GetTaskByID(taskID)
		if err != nil {
			return err
		}
		_, err = taskService.CompleteTaskUpload(taskID, map[string]any{"url": originURL, "urls": urls}, current.Cost)
		return err
	}

	if err := HandleTaskPoll(context.Background(), newTaskPollJob(task.ID, 0)); err != nil {
		t.Fatalf("handle transient poll: %v", err)
	}
	var afterTransient model.Task
	if err := db.First(&afterTransient, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if afterTransient.Status != model.TaskStatusProcessing || requeueCalls != 1 {
		t.Fatalf("transient poll changed task: status=%s requeues=%d", afterTransient.Status, requeueCalls)
	}
	if err := HandleTaskPoll(context.Background(), newTaskPollJob(task.ID, 1)); err != nil {
		t.Fatalf("handle successful poll: %v", err)
	}

	attempts := loadCapabilityAttempts(t, db, task.CallID)
	if len(attempts) != 3 {
		t.Fatalf("attempt count = %d, want submit + 2 polls", len(attempts))
	}
	failedPoll := attempts[1]
	successPoll := attempts[2]
	if failedPoll.Status != model.APICallAttemptStatusFailed || failedPoll.HTTPStatus != 503 || !failedPoll.ErrorRetryable {
		t.Fatalf("transient poll attempt = %#v", failedPoll)
	}
	if successPoll.Status != model.APICallAttemptStatusCompleted || successPoll.HTTPStatus != 200 {
		t.Fatalf("successful poll attempt = %#v", successPoll)
	}
	assertCapabilityAttempt(t, failedPoll, task, endpoint, model.APICallStagePoll)
	assertCapabilityAttempt(t, successPoll, task, endpoint, model.APICallStagePoll)
	assertTaskAndCallTerminal(t, db, task.ID, task.CallID, model.TaskStatusSuccess, model.APICallStatusCompleted, successPoll.ID)
	assertWorkerBillingAttempt(t, db, task.TaskNo+":reserve", submitAttempt.ID)
	assertWorkerBillingAttempt(t, db, task.TaskNo+":settle", successPoll.ID)
	assertCapabilityRequestLog(t, db, task, channel, failedPoll, transientMetadata, transientErr.Error())
	assertCapabilityRequestLog(t, db, task, channel, successPoll, successMetadata, "")
}

func TestTaskPollAdoptsEnqueuedRoundAfterCursorWriteLoss(t *testing.T) {
	db, task, channel, endpoint := setupCapabilityWorkerTest(t, model.TaskStatusProcessing, model.ModePoll)
	seedCompletedSubmitAttempt(t, task, channel, endpoint)
	fake := &scriptedCapabilityProvider{progress: []progressReply{{result: provider.ProgressResult{
		RequestMetadata: provider.RequestMetadata{
			Method: httpMethodGet, RequestPath: "/actual/poll/provider-task", StatusCode: 200,
		},
		Status: provider.StatusProcessing, Progress: 40,
	}}}}
	newProvider = func(*model.Channel, *model.ChannelAccount, *model.Endpoint) (provider.Provider, error) {
		return fake, nil
	}
	requeueCalls := 0
	requeueTaskPoll = func(taskID uint, pollCount int, _ int) error {
		requeueCalls++
		if taskID != task.ID || pollCount != 2 {
			t.Fatalf("requeue = task %d count %d", taskID, pollCount)
		}
		return nil
	}

	if err := HandleTaskPoll(context.Background(), newTaskPollJob(task.ID, 1)); err != nil {
		t.Fatalf("handle adopted poll: %v", err)
	}
	var current model.Task
	if err := db.First(&current, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.PollCursor != 2 || fake.progressCall != 1 || requeueCalls != 1 {
		t.Fatalf("adopted poll cursor=%d progress_calls=%d requeues=%d", current.PollCursor, fake.progressCall, requeueCalls)
	}
}

func TestTaskPollBusySubmitLeaseIsRetried(t *testing.T) {
	db, task, _, _ := setupCapabilityWorkerTest(t, model.TaskStatusProcessing, model.ModePoll)
	checkpoint := datatypes.JSON(`{"version":1,"lease_owner":"submit-owner"}`)
	if err := db.Model(task).UpdateColumn("submit_checkpoint", checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	lease, acquired, err := service.AcquireTaskWorkerLease(context.Background(), task.ID, service.TaskWorkerStageSubmit)
	if err != nil || !acquired {
		t.Fatalf("submit lease acquired=%v err=%v", acquired, err)
	}
	defer func() { _ = lease.Stop() }()

	err = HandleTaskPoll(context.Background(), newTaskPollJob(task.ID, 0))
	if !errors.Is(err, service.ErrTaskWorkerLeaseBusy) {
		t.Fatalf("busy poll error = %v", err)
	}
}

func TestTaskPollFinalFailureEndsAttemptAndCall(t *testing.T) {
	db, task, channel, endpoint := setupCapabilityWorkerTest(t, model.TaskStatusProcessing, model.ModePoll)
	seedCompletedSubmitAttempt(t, task, channel, endpoint)
	metadata := provider.RequestMetadata{
		Method: httpMethodGet, RequestPath: "/actual/poll/provider-task", StatusCode: 200,
		DurationMs: 13, RequestAt: time.Now().Add(-time.Second),
	}
	newProvider = func(*model.Channel, *model.ChannelAccount, *model.Endpoint) (provider.Provider, error) {
		return &scriptedCapabilityProvider{progress: []progressReply{{result: provider.ProgressResult{
			RequestMetadata: metadata,
			Status:          provider.StatusFail,
			Error:           "render failed",
		}}}}, nil
	}

	if err := HandleTaskPoll(context.Background(), newTaskPollJob(task.ID, 0)); err != nil {
		t.Fatalf("handle final poll failure: %v", err)
	}

	attempts := loadCapabilityAttempts(t, db, task.CallID)
	if len(attempts) != 2 {
		t.Fatalf("attempt count = %d, want 2", len(attempts))
	}
	finalAttempt := attempts[1]
	if finalAttempt.Status != model.APICallAttemptStatusFailed || finalAttempt.HTTPStatus != 200 ||
		finalAttempt.ErrorRetryable || finalAttempt.ErrorMessage != "render failed" {
		t.Fatalf("final poll attempt = %#v", finalAttempt)
	}
	assertTaskAndCallTerminal(t, db, task.ID, task.CallID, model.TaskStatusFailed, model.APICallStatusFailed, finalAttempt.ID)
	assertWorkerBillingAttempt(t, db, task.TaskNo, finalAttempt.ID)
	assertCapabilityRequestLog(t, db, task, channel, finalAttempt, metadata, "render failed")
}

func TestTaskPollNested451FailureEndsImmediately(t *testing.T) {
	db, task, channel, endpoint := setupCapabilityWorkerTest(t, model.TaskStatusProcessing, model.ModePoll)
	seedCompletedSubmitAttempt(t, task, channel, endpoint)
	metadata := provider.RequestMetadata{
		Method: httpMethodGet, RequestPath: "/actual/poll/provider-task", StatusCode: 200,
		DurationMs: 13, RequestAt: time.Now().Add(-time.Second),
	}
	message := `API Error: openai returned 451: {"error":{"message":"unsafe image"}}`
	newProvider = func(*model.Channel, *model.ChannelAccount, *model.Endpoint) (provider.Provider, error) {
		return &scriptedCapabilityProvider{progress: []progressReply{{result: provider.ProgressResult{
			RequestMetadata: metadata,
			Status:          provider.StatusFail,
			Error:           message,
		}}}}, nil
	}
	requeueCalls := 0
	requeueTaskPoll = func(uint, int, int) error {
		requeueCalls++
		return nil
	}

	if err := HandleTaskPoll(context.Background(), newTaskPollJob(task.ID, 0)); err != nil {
		t.Fatalf("handle 451 poll failure: %v", err)
	}

	var failed model.Task
	if err := db.First(&failed, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if failed.Status != model.TaskStatusFailed || failed.ErrorMessage != message || requeueCalls != 0 {
		t.Fatalf("task = status %s error %q requeues %d", failed.Status, failed.ErrorMessage, requeueCalls)
	}
	attempts := loadCapabilityAttempts(t, db, task.CallID)
	finalAttempt := attempts[len(attempts)-1]
	if finalAttempt.Status != model.APICallAttemptStatusFailed || finalAttempt.HTTPStatus != 451 ||
		finalAttempt.ErrorRetryable || finalAttempt.ErrorMessage != message {
		t.Fatalf("final poll attempt = %#v", finalAttempt)
	}
}

func TestTaskPollStopsWhenCallbackClaimsFinalization(t *testing.T) {
	db, task, channel, endpoint := setupCapabilityWorkerTest(t, model.TaskStatusProcessing, model.ModeCallback)
	seedCompletedSubmitAttempt(t, task, channel, endpoint)
	fake := &scriptedCapabilityProvider{progress: []progressReply{{result: provider.ProgressResult{
		RequestMetadata: provider.RequestMetadata{
			Method: httpMethodGet, RequestPath: "/actual/poll/provider-task", StatusCode: 200,
		},
		Status: provider.StatusProcessing, Progress: 60,
	}}}}
	fake.progressHook = func() {
		ready, err := taskService.BeginTaskFinalization(task.ID)
		if err != nil || !ready {
			t.Fatalf("callback finalization claim: ready=%v err=%v", ready, err)
		}
	}
	newProvider = func(*model.Channel, *model.ChannelAccount, *model.Endpoint) (provider.Provider, error) {
		return fake, nil
	}
	requeueCalls := 0
	requeueTaskPoll = func(uint, int, int) error {
		requeueCalls++
		return nil
	}

	if err := HandleTaskPoll(context.Background(), newTaskPollJob(task.ID, 0)); err != nil {
		t.Fatalf("poll after callback finalization: %v", err)
	}
	var current model.Task
	if err := db.First(&current, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.Status != model.TaskStatusFinalizing || current.PollCursor != 0 || requeueCalls != 0 {
		t.Fatalf("callback-owned task = status %s cursor %d requeues %d",
			current.Status, current.PollCursor, requeueCalls)
	}
}

func TestTimeoutCheckerRecoversLostPendingTaskWithoutRefundingReservation(t *testing.T) {
	db, task, _, _ := setupCapabilityWorkerTest(t, model.TaskStatusPending, model.ModePoll)
	if err := db.Model(&model.Task{}).Where("id = ?", task.ID).
		UpdateColumn("updated_at", time.Now().Add(-31*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	previousRecover := recoverTaskSubmit
	var recovered []uint
	recoverTaskSubmit = func(taskID uint) error {
		recovered = append(recovered, taskID)
		return nil
	}
	t.Cleanup(func() { recoverTaskSubmit = previousRecover })

	if err := HandleTaskTimeoutCheck(context.Background(), NewTimeoutCheckTask()); err != nil {
		t.Fatalf("handle timeout check: %v", err)
	}
	if len(recovered) != 1 || recovered[0] != task.ID {
		t.Fatalf("recovered task IDs = %v, want [%d]", recovered, task.ID)
	}
	var current model.Task
	if err := db.First(&current, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.Status != model.TaskStatusPending || current.CompletedAt != nil {
		t.Fatalf("recovered task = status %s completed_at %v", current.Status, current.CompletedAt)
	}
	assertWorkerBillingAttempt(t, db, task.TaskNo+":reserve", 0)
	var refundCount int64
	if err := db.Model(&model.BillingLog{}).Where("idempotent_key = ?", task.TaskNo).Count(&refundCount).Error; err != nil {
		t.Fatal(err)
	}
	if refundCount != 0 {
		t.Fatalf("refund billing log count = %d, want 0", refundCount)
	}
}

const (
	httpMethodGet  = "GET"
	httpMethodPost = "POST"
)

func setupCapabilityWorkerTest(
	t *testing.T,
	status model.TaskStatus,
	mode model.InteractionMode,
) (*gorm.DB, *model.Task, *model.Channel, *model.Endpoint) {
	t.Helper()
	dsn := fmt.Sprintf("file:capability-worker-%d?mode=memory&cache=shared", atomic.AddInt64(&capabilityWorkerDBCounter, 1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.Token{}, &model.Channel{}, &model.ChannelAccount{}, &model.Endpoint{},
		&model.APICall{}, &model.APICallAttempt{}, &model.BillingLog{}, &model.BalanceEntry{}, &model.Task{}, &model.ChannelRequestLog{},
	); err != nil {
		t.Fatalf("migrate worker ledger: %v", err)
	}
	model.SetDB(db)
	previousLogger := logger.L
	previousProvider := newProvider
	previousFinishAttempt := finishCapabilityAttempt
	previousSaveCheckpoint := saveTaskSubmitCheckpoint
	previousRequeue := requeueTaskPoll
	previousUpload := enqueueUpload
	logger.L = zap.NewNop()
	t.Cleanup(func() {
		logger.L = previousLogger
		newProvider = previousProvider
		finishCapabilityAttempt = previousFinishAttempt
		saveTaskSubmitCheckpoint = previousSaveCheckpoint
		requeueTaskPoll = previousRequeue
		enqueueUpload = previousUpload
	})

	unique := fmt.Sprintf("%d", atomic.LoadInt64(&capabilityWorkerDBCounter))
	user := &model.User{Username: "worker-user-" + unique, Balance: decimal.NewFromInt(10), Status: 1}
	if err := db.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	token := &model.Token{UserID: user.ID, Key: "worker-token-" + unique, Balance: decimal.NewFromInt(10), Status: 1}
	if err := db.Create(token).Error; err != nil {
		t.Fatal(err)
	}
	channel := &model.Channel{Type: "worker-channel-" + unique, BaseURL: "https://upstream.example", Status: 1}
	if err := db.Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	account := &model.ChannelAccount{ChannelID: channel.ID, Name: "worker-account", Status: 1, CurrentTasks: 1}
	if err := db.Create(account).Error; err != nil {
		t.Fatal(err)
	}
	endpoint := &model.Endpoint{
		ModelCode: "worker-model", ChannelID: channel.ID, AccountID: account.ID,
		VendorModel: "vendor-worker-model", InteractionMode: mode, Status: 1,
		RequestPath: "/configured/submit", RequestMethod: httpMethodPost,
		PollPath: "/configured/poll/{task_id}", PollMethod: httpMethodGet,
		PollInterval: 1, PollMaxAttempts: 5, InputPrice: decimal.NewFromInt(1),
	}
	if err := db.Create(endpoint).Error; err != nil {
		t.Fatal(err)
	}
	taskNo := "task_worker_" + unique
	call, err := service.NewAPICallService().StartCall(&service.StartCallRequest{
		ID: "call_worker_" + unique, RequestID: "request-worker-" + unique,
		UserID: user.ID, TokenID: token.ID, Endpoint: "/v1/capabilities/worker-model",
		Operation: "capability.invoke", Model: endpoint.ModelCode,
		Background: true, ResourceType: "task", ResourceID: taskNo,
	})
	if err != nil {
		t.Fatal(err)
	}
	cost := decimal.NewFromInt(1)
	if err := service.NewBillingService().DeductWithBillingContext(
		token.ID, user.ID, cost, taskNo+":reserve",
		service.BillingContext{CallID: call.ID, Phase: model.BillingPhaseReserve},
	); err != nil {
		t.Fatal(err)
	}
	params := datatypes.JSON(`{"prompt":"worker test"}`)
	task := &model.Task{
		TaskNo: taskNo, CallID: call.ID, UserID: user.ID, TokenID: token.ID,
		ModelCode: endpoint.ModelCode, ChannelID: channel.ID, EndpointID: endpoint.ID, AccountID: account.ID,
		VendorTaskID: "provider-task", Status: status, RequestParams: params, MappedParams: params, Cost: cost,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	return db, task, channel, endpoint
}

func seedCompletedSubmitAttempt(
	t *testing.T,
	task *model.Task,
	channel *model.Channel,
	endpoint *model.Endpoint,
) *model.APICallAttempt {
	t.Helper()
	attempt, err := service.StartCapabilityAttempt(task, endpoint, model.APICallStageSubmit)
	if err != nil {
		t.Fatalf("start seed submit attempt: %v", err)
	}
	metadata := provider.RequestMetadata{
		Method: httpMethodPost, RequestPath: "/actual/submit", StatusCode: 201,
		DurationMs: 7, RequestAt: time.Now().Add(-3 * time.Second),
	}
	if err := service.FinishCapabilityAttempt(
		task, channel, endpoint, attempt, model.APICallStageSubmit, metadata, nil,
	); err != nil {
		t.Fatalf("finish seed submit attempt: %v", err)
	}
	return attempt
}

func newTaskPollJob(taskID uint, pollCount int) *asynq.Task {
	payload, _ := json.Marshal(TaskPollPayload{TaskID: taskID, PollCount: pollCount})
	return asynq.NewTask(TypeTaskPoll, payload)
}

func loadCapabilityAttempts(t *testing.T, db *gorm.DB, callID string) []model.APICallAttempt {
	t.Helper()
	var attempts []model.APICallAttempt
	if err := db.Where("call_id = ?", callID).Order("attempt_no ASC").Find(&attempts).Error; err != nil {
		t.Fatalf("load attempts: %v", err)
	}
	return attempts
}

func assertCapabilityAttempt(
	t *testing.T,
	attempt model.APICallAttempt,
	task *model.Task,
	endpoint *model.Endpoint,
	stage string,
) {
	t.Helper()
	if attempt.RouteKind != model.APICallRouteCapability || attempt.Stage != stage ||
		attempt.ChannelID != 0 || attempt.KeyID != 0 || attempt.EndpointID != endpoint.ID ||
		attempt.AccountID != task.AccountID || attempt.VendorModel != endpoint.VendorModel {
		t.Fatalf("attempt routing = %#v", attempt)
	}
}

func assertTaskAndCallTerminal(
	t *testing.T,
	db *gorm.DB,
	taskID uint,
	callID string,
	taskStatus model.TaskStatus,
	callStatus model.APICallStatus,
	finalAttemptID uint,
) {
	t.Helper()
	var task model.Task
	var call model.APICall
	if err := db.First(&task, taskID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&call, "id = ?", callID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != taskStatus || call.Status != callStatus || call.FinalAttemptID != finalAttemptID {
		t.Fatalf("terminal state = task %s call %s final attempt %d", task.Status, call.Status, call.FinalAttemptID)
	}
}

func assertWorkerBillingAttempt(t *testing.T, db *gorm.DB, idempotentKey string, attemptID uint) {
	t.Helper()
	var log model.BillingLog
	if err := db.Where("idempotent_key = ?", idempotentKey).First(&log).Error; err != nil {
		t.Fatalf("load billing log %q: %v", idempotentKey, err)
	}
	if log.AttemptID != attemptID {
		t.Fatalf("billing log %q attempt = %d, want %d", idempotentKey, log.AttemptID, attemptID)
	}
}

func assertCapabilityRequestLog(
	t *testing.T,
	db *gorm.DB,
	task *model.Task,
	channel *model.Channel,
	attempt model.APICallAttempt,
	metadata provider.RequestMetadata,
	errorMessage string,
) {
	t.Helper()
	var log model.ChannelRequestLog
	if err := db.Where("attempt_id = ?", attempt.ID).First(&log).Error; err != nil {
		t.Fatalf("load request log for attempt %d: %v", attempt.ID, err)
	}
	wantType := model.RequestTypeSubmit
	if attempt.Stage == model.APICallStagePoll {
		wantType = model.RequestTypePoll
	}
	if log.CallID != task.CallID || log.TaskID != task.ID || log.ChannelID != channel.ID ||
		log.AttemptID != attempt.ID || log.RequestType != wantType || log.RequestPath != metadata.RequestPath ||
		log.Method != metadata.Method || log.StatusCode != metadata.StatusCode ||
		log.DurationMs != metadata.DurationMs || log.ErrorMessage != errorMessage {
		t.Fatalf("request log = %#v", log)
	}
}
