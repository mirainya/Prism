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

type DetailedEstimator interface {
	EstimateDetailed(ctx context.Context, req *GenerateRequest) (*ProviderEstimate, error)
}

type ProviderEstimate struct {
	EstimatedCost float64 `json:"estimated_cost"`
	ActualCost    float64 `json:"actual_cost,omitempty"`
	UnitCost      float64 `json:"unit_cost,omitempty"`
	Units         float64 `json:"units,omitempty"`
	BillingMode   string  `json:"billing_mode,omitempty"`
	BillingTier   string  `json:"billing_tier,omitempty"`
	PricingSource string  `json:"pricing_source,omitempty"`
	Currency      string  `json:"currency,omitempty"`
}

// Canceller 可选：支持上游取消
type Canceller interface {
	CanCancel(status VideoTaskStatus) bool
	Cancel(ctx context.Context, providerTaskID string) error
}

// Actioner executes a configured provider task action such as queue priority.
type Actioner interface {
	CanAction(action string, status VideoTaskStatus) bool
	Action(ctx context.Context, action, providerTaskID string) (*ProviderMetadata, error)
}

type ActionPricing interface {
	ActionSurchargePercent(action string) float64
}

// LocalCancellationPolicy describes whether a task without an upstream ID can
// be removed from the provider's local queue.
type LocalCancellationPolicy interface {
	CanCancelLocal(task *VideoTask) bool
}

// CapabilityDiscoverer optionally reads the model matrix exposed by an
// upstream. Static channel rules remain the upper bound for routing safety.
type CapabilityDiscoverer interface {
	DiscoverCapabilities(ctx context.Context) (map[string]DiscoveredModelCapabilities, error)
}

type DiscoveredModelCapabilities struct {
	Resolutions                   []string
	Ratios                        []string
	TaskModes                     []string
	DurationOptions               []int
	DurationMin                   int
	DurationMax                   int
	DurationMaxWithVideoReference int
	SupportsSmartDuration         *bool
	AllowGeneratedAudio           *bool
	RequireVisualMediaWithAudio   *bool
	SupportsCancel                *bool
	MaxImages                     int
	MaxVideos                     int
	MaxAudios                     int
	MaxMedia                      int
	MediaDurationMin              float64
	MediaDurationMax              float64
	MaxVideoDuration              float64
	MaxAudioDuration              float64
	ServiceTiers                  []string
}

// RequestPathProvider exposes the actual upstream submit path for call logs.
type RequestPathProvider interface {
	RequestPath() string
}

// GenerateRequest 生成请求
type GenerateRequest struct {
	Model       string
	Prompt      string
	Resolution  string
	Ratio       string
	Duration    int
	Audio       bool
	TaskMode    string // "text" / "references" / provider-configured modes
	ServiceTier string // standard / priority / vip (channel-configured)
	Content     []ContentItem
	Params      map[string]any

	TaskID  string
	TokenID uint
	Channel *VideoChannel
	Key     *VideoChannelKey
}

// ContentItem 内容项
type ContentItem struct {
	ClientRefID     string  `json:"client_ref_id,omitempty"`
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
	Metadata       *ProviderMetadata
}

// Progress 轮询进度
type Progress struct {
	Status   VideoTaskStatus
	Percent  int
	Result   *GenerationResult
	Error    string
	Metadata *ProviderMetadata
}

type ProviderMetadata struct {
	QueueStatus              string  `json:"queue_status,omitempty"`
	QueuePosition            int     `json:"queue_position,omitempty"`
	QueueLimit               int     `json:"queue_limit,omitempty"`
	PriorityQueue            *bool   `json:"priority_queue,omitempty"`
	PointsVIP                *bool   `json:"points_vip,omitempty"`
	PrioritySurchargePercent float64 `json:"priority_surcharge_percent,omitempty"`
	EstimatedCost            float64 `json:"estimated_cost,omitempty"`
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
