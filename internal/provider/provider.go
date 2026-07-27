package provider

import (
	"context"
	"encoding/json"
	"time"
)

type ResultMode string

const (
	ResultModePoll     ResultMode = "poll"
	ResultModeCallback ResultMode = "callback"
	ResultModeSync     ResultMode = "sync"
)

type TaskStatus string

const (
	StatusPending    TaskStatus = "PENDING"
	StatusSubmitted  TaskStatus = "SUBMITTED"
	StatusProcessing TaskStatus = "PROCESSING"
	StatusSuccess    TaskStatus = "SUCCESS"
	StatusFail       TaskStatus = "FAIL"
)

type SubmitRequest struct {
	TaskNo      string
	Params      map[string]any
	CallbackURL string
}

// RequestMetadata describes the concrete HTTP request executed by a provider.
// StatusCode remains zero when no HTTP response was received.
type RequestMetadata struct {
	Method      string
	RequestPath string
	StatusCode  int
	DurationMs  int64
	RequestAt   time.Time
}

type SubmitResult struct {
	RequestMetadata RequestMetadata
	ProviderTaskID  string
	Status          TaskStatus
	Progress        int
	URLs            []string
	B64Data         []string // base64 图片数据(如 OpenAI images b64_json),需转存为 URL
	RevisedPrompt   string   // 上游优化后的提示词(如 OpenAI images revised_prompt)
}

type ProgressResult struct {
	RequestMetadata RequestMetadata
	RawResponse     json.RawMessage
	Status          TaskStatus
	Progress        int
	URLs            []string
	B64Data         []string // base64 图片数据,需转存为 URL
	RevisedPrompt   string
	Error           string
}

type Provider interface {
	Submit(ctx context.Context, req SubmitRequest) (SubmitResult, error)
	GetProgress(ctx context.Context, providerTaskID string) (ProgressResult, error)
	ParseCallback(ctx context.Context, body []byte) (ProgressResult, string, error)
}
