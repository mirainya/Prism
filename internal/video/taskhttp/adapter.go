package taskhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mirainya/Prism/internal/video"
)

// Codec owns only one upstream protocol's wire representation. HTTP execution,
// retries and asynchronous task operations stay in Adapter.
type Codec interface {
	BuildRequest(context.Context, *video.GenerateRequest) (*video.ProviderRequest, error)
	DecodeSubmit([]byte) (*video.SubmitResult, error)
	DecodePoll([]byte) (*video.Progress, error)
}

type RequestValidator interface {
	ValidateRequest(context.Context, *video.GenerateRequest) error
}

type AdapterConfig struct {
	Client              *Client
	Codec               Codec
	Submit              Operation
	Poll                Operation
	Cancel              Operation
	CancelStatuses      []video.VideoTaskStatus
	AllowLocalCancel    bool
	InitializationError error
}

// Adapter combines a native protocol codec with the common asynchronous HTTP
// task executor. It is provider-neutral and reusable by other native protocols.
type Adapter struct {
	client              *Client
	codec               Codec
	submit              Operation
	poll                Operation
	cancel              Operation
	cancelStatuses      map[video.VideoTaskStatus]struct{}
	allowLocalCancel    bool
	initializationError error
}

var (
	_ video.Adapter                 = (*Adapter)(nil)
	_ video.RequestValidator        = (*Adapter)(nil)
	_ video.Canceller               = (*Adapter)(nil)
	_ video.LocalCancellationPolicy = (*Adapter)(nil)
	_ video.RequestPathProvider     = (*Adapter)(nil)
)

func NewAdapter(config AdapterConfig) *Adapter {
	cancelStatuses := make(map[video.VideoTaskStatus]struct{}, len(config.CancelStatuses))
	for _, status := range config.CancelStatuses {
		cancelStatuses[status] = struct{}{}
	}
	return &Adapter{
		client: config.Client, codec: config.Codec,
		submit: config.Submit, poll: config.Poll, cancel: config.Cancel,
		cancelStatuses: cancelStatuses, allowLocalCancel: config.AllowLocalCancel,
		initializationError: config.InitializationError,
	}
}

func (a *Adapter) ValidateRequest(ctx context.Context, request *video.GenerateRequest) error {
	if err := a.ready(); err != nil {
		return err
	}
	if validator, ok := a.codec.(RequestValidator); ok {
		return validator.ValidateRequest(ctx, request)
	}
	return nil
}

func (a *Adapter) BuildRequest(ctx context.Context, request *video.GenerateRequest) (*video.ProviderRequest, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	return a.codec.BuildRequest(ctx, request)
}

func (a *Adapter) Submit(ctx context.Context, request *video.ProviderRequest) (*video.SubmitResult, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	if request == nil {
		return nil, errors.New("provider request is required")
	}
	body, err := json.Marshal(request.Body)
	if err != nil {
		return nil, fmt.Errorf("marshal provider request: %w", err)
	}
	responseBody, err := a.client.Do(ctx, "submit", a.submit, body, request.Headers)
	if err != nil {
		return nil, err
	}
	result, err := a.codec.DecodeSubmit(responseBody)
	if err != nil {
		return nil, video.NewAmbiguousProviderError("decode submit response", err)
	}
	return result, nil
}

func (a *Adapter) Poll(ctx context.Context, providerTaskID string) (*video.Progress, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	providerTaskID = strings.TrimSpace(providerTaskID)
	if providerTaskID == "" {
		return nil, errors.New("provider task id is required")
	}
	body, err := a.client.Do(ctx, "poll", a.poll, nil, nil, providerTaskID)
	if err != nil {
		return nil, err
	}
	progress, err := a.codec.DecodePoll(body)
	if err != nil {
		return nil, video.NewRetryableProviderError("decode poll response", err)
	}
	return progress, nil
}

func (a *Adapter) CanCancel(status video.VideoTaskStatus) bool {
	if a == nil || strings.TrimSpace(a.cancel.Path) == "" {
		return false
	}
	_, ok := a.cancelStatuses[status]
	return ok
}

func (a *Adapter) Cancel(ctx context.Context, providerTaskID string) error {
	if err := a.ready(); err != nil {
		return err
	}
	if strings.TrimSpace(a.cancel.Path) == "" {
		return video.ErrCancelNotSupported
	}
	providerTaskID = strings.TrimSpace(providerTaskID)
	if providerTaskID == "" {
		return errors.New("provider task id is required")
	}
	_, err := a.client.Do(ctx, "cancel", a.cancel, nil, nil, providerTaskID)
	return err
}

func (a *Adapter) CanCancelLocal(task *video.VideoTask) bool {
	return a != nil && a.allowLocalCancel && task != nil && task.Status == video.VideoTaskStatusQueued
}

func (a *Adapter) RequestPath() string {
	if a == nil {
		return ""
	}
	return a.submit.Path
}

func (a *Adapter) ready() error {
	if a == nil {
		return errors.New("task HTTP adapter is nil")
	}
	if a.initializationError != nil {
		return a.initializationError
	}
	if a.codec == nil {
		return errors.New("task HTTP adapter codec is required")
	}
	return a.client.Ready()
}
