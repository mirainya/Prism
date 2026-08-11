package video

import "context"

const (
	AdapterTypeSeedance = "seedance"
	AdapterTypeGeneric  = "generic"
)

// Adapter 视频生成适配器接口
type Adapter interface {
	BuildRequest(ctx context.Context, req *GenerateRequest) (*ProviderRequest, error)
	Submit(ctx context.Context, pr *ProviderRequest) (*SubmitResult, error)
	Poll(ctx context.Context, providerTaskID string) (*Progress, error)
}

// RequestValidator validates provider-specific request limits after routing.
type RequestValidator interface {
	ValidateRequest(ctx context.Context, req *GenerateRequest) error
}

// Estimator 可选：支持上游估价
type Estimator interface {
	Estimate(ctx context.Context, req *GenerateRequest) (float64, error)
}

// Canceller 可选：支持上游取消
type Canceller interface {
	CanCancel(status VideoTaskStatus) bool
	Cancel(ctx context.Context, providerTaskID string) error
}

// LocalCancellationPolicy describes whether a task without an upstream ID can
// be removed from the provider's local queue.
type LocalCancellationPolicy interface {
	CanCancelLocal(task *VideoTask) bool
}

// RequestPathProvider exposes the actual upstream submit path for call logs.
type RequestPathProvider interface {
	RequestPath() string
}

// GenerateRequest 生成请求
type GenerateRequest struct {
	Model      string
	Prompt     string
	Resolution string
	Ratio      string
	Duration   int
	Audio      bool
	TaskMode   string // "text" / "references"
	Content    []ContentItem
	Params     map[string]any

	TaskID  string
	TokenID uint
	Channel *VideoChannel
	Key     *VideoChannelKey
}

// ContentItem 内容项
type ContentItem struct {
	Type            string  `json:"type"`
	Role            string  `json:"role,omitempty"`
	Text            string  `json:"text,omitempty"`
	AssetID         string  `json:"asset_id,omitempty"`
	URL             string  `json:"url,omitempty"`
	StorageObjectID string  `json:"storage_object_id,omitempty"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
}

// ProviderRequest 构建完成的上游请求
type ProviderRequest struct {
	Body    map[string]any
	Headers map[string]string
}

// SubmitResult 提交结果
type SubmitResult struct {
	ProviderTaskID string
	Status         VideoTaskStatus
	Result         *GenerationResult
}

// Progress 轮询进度
type Progress struct {
	Status  VideoTaskStatus
	Percent int
	Result  *GenerationResult
	Error   string
}

// GenerationResult 生成结果
type GenerationResult struct {
	VideoURL     string  `json:"video_url"`
	ThumbnailURL string  `json:"thumbnail_url,omitempty"`
	Duration     float64 `json:"duration,omitempty"`
}

// RequiredCaps 请求所需能力
type RequiredCaps struct {
	FirstFrame bool
	LastFrame  bool
	Cancel     bool
	Audio      bool
	WebSearch  bool
}
