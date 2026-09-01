package video

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
)

// VideoTaskStatus 视频任务状态
type VideoTaskStatus string

const (
	VideoTaskStatusQueued    VideoTaskStatus = "queued"
	VideoTaskStatusSubmitted VideoTaskStatus = "submitted"
	VideoTaskStatusTracking  VideoTaskStatus = "tracking"
	VideoTaskStatusCompleted VideoTaskStatus = "completed"
	VideoTaskStatusFailed    VideoTaskStatus = "failed"
	VideoTaskStatusCancelled VideoTaskStatus = "cancelled"
)

func (s VideoTaskStatus) IsTerminal() bool {
	return s == VideoTaskStatusCompleted || s == VideoTaskStatusFailed || s == VideoTaskStatusCancelled
}

// VideoAssetStatus 素材状态
type VideoAssetStatus string

const (
	VideoAssetStatusUploading VideoAssetStatus = "uploading"
	VideoAssetStatusReady     VideoAssetStatus = "ready"
	VideoAssetStatusExpired   VideoAssetStatus = "expired"
)

// VideoChannel 视频渠道
type VideoChannel struct {
	ID                    uint            `gorm:"primarykey" json:"id"`
	Name                  string          `gorm:"type:varchar(64);not null;comment:渠道名称" json:"name"`
	AdapterType           string          `gorm:"type:varchar(32);not null;comment:协议实现(seedance原生协议/generic声明式协议)" json:"adapter_type"`
	AdapterProfile        string          `gorm:"type:varchar(32);not null;default:'';comment:适配器配置版本" json:"adapter_profile"`
	BaseURL               string          `gorm:"type:varchar(256);not null;comment:上游地址" json:"base_url"`
	Status                string          `gorm:"type:varchar(16);default:'active';comment:状态" json:"status"`
	Priority              int             `gorm:"default:0;comment:选路优先级(降序)" json:"priority"`
	RequestTimeoutSeconds int             `gorm:"column:request_timeout_seconds;not null;default:30;comment:上游请求超时(秒)" json:"request_timeout_seconds"`
	Models                datatypes.JSON  `gorm:"type:json;not null;comment:支持模型列表" json:"models"`
	Capabilities          datatypes.JSON  `gorm:"type:json;comment:能力声明" json:"capabilities"`
	SupportsFirstFrame    *bool           `gorm:"column:supports_first_frame;not null;default:false;comment:支持首帧" json:"supports_first_frame,omitempty"`
	SupportsLastFrame     *bool           `gorm:"column:supports_last_frame;not null;default:false;comment:支持尾帧" json:"supports_last_frame,omitempty"`
	SupportsAudio         *bool           `gorm:"column:supports_audio;not null;default:false;comment:支持生成音频" json:"supports_audio,omitempty"`
	SupportsWebSearch     *bool           `gorm:"column:supports_web_search;not null;default:false;comment:支持联网增强" json:"supports_web_search,omitempty"`
	CancelMode            string          `gorm:"column:cancel_mode;type:varchar(16);not null;default:'disabled';comment:取消策略(disabled/local_only/provider)" json:"cancel_mode"`
	Pricing               datatypes.JSON  `gorm:"type:json;comment:计费配置" json:"pricing"`
	PricingMode           string          `gorm:"column:pricing_mode;type:varchar(24);not null;default:'fixed';comment:计费模式(fixed/upstream_estimate)" json:"pricing_mode"`
	FixedPrice            decimal.Decimal `gorm:"column:fixed_price;type:decimal(10,4);not null;default:0;comment:固定价格" json:"fixed_price"`
	MarkupRatio           decimal.Decimal `gorm:"column:markup_ratio;type:decimal(4,2);not null;default:1;comment:加价系数" json:"markup_ratio"`
	AssetResolver         string          `gorm:"type:varchar(32);default:'direct_url';comment:素材解析器类型" json:"asset_resolver"`
	ResultStorageEnabled  *bool           `gorm:"column:result_storage_enabled;not null;default:false;comment:是否转存生成结果" json:"result_storage_enabled,omitempty"`
	ExtraConfig           datatypes.JSON  `gorm:"type:json;comment:适配器附加配置" json:"extra_config"`
	CreatedAt             time.Time       `gorm:"comment:创建时间" json:"created_at"`
	UpdatedAt             time.Time       `gorm:"comment:更新时间" json:"updated_at"`
}

func (VideoChannel) TableName() string { return "video_channels" }

// VideoChannelKey 视频渠道密钥
type VideoChannelKey struct {
	ID                  uint       `gorm:"primarykey" json:"id"`
	ChannelID           uint       `gorm:"not null;index:idx_channel_status;comment:所属渠道ID" json:"channel_id"`
	APIKey              string     `gorm:"type:varchar(256);not null;comment:API密钥" json:"-"`
	Label               string     `gorm:"type:varchar(64);comment:标签" json:"label"`
	Weight              int        `gorm:"default:1;comment:权重" json:"weight"`
	MaxConcurrency      int        `gorm:"default:0;comment:最大并发(0=不限)" json:"max_concurrency"`
	Status              string     `gorm:"type:varchar(16);default:'active';index:idx_channel_status;comment:状态" json:"status"`
	CurrentConcurrency  int        `gorm:"default:0;comment:当前并发" json:"current_concurrency"`
	TotalCalls          int64      `gorm:"default:0;comment:总调用次数" json:"total_calls"`
	LastUsedAt          *time.Time `gorm:"comment:最后使用时间" json:"last_used_at"`
	CreatedAt           time.Time  `gorm:"comment:创建时间" json:"created_at"`
	ConsecutiveFailures int        `gorm:"default:0;comment:连续失败次数" json:"-"`
}

func (VideoChannelKey) TableName() string { return "video_channel_keys" }

// VideoTask 视频生成任务
type VideoTask struct {
	ID               string          `gorm:"type:varchar(32);primarykey" json:"id"`
	CallID           string          `gorm:"type:varchar(64);not null;default:'';index" json:"call_id"`
	UserID           uint            `gorm:"not null;index:idx_user_status;comment:用户ID" json:"user_id"`
	TokenID          uint            `gorm:"not null;index:idx_token_status;comment:令牌ID" json:"token_id"`
	Model            string          `gorm:"type:varchar(64);not null;comment:模型标识" json:"model"`
	VendorModel      string          `gorm:"type:varchar(120);not null;default:'';comment:上游模型标识快照" json:"vendor_model"`
	Status           VideoTaskStatus `gorm:"type:varchar(16);not null;index:idx_token_status;index:idx_status_created;comment:状态" json:"status"`
	Progress         int             `gorm:"default:0;comment:进度(0-100)" json:"progress"`
	TaskMode         string          `gorm:"type:varchar(16);not null;comment:任务模式" json:"task_mode"`
	ServiceTier      string          `gorm:"type:varchar(24);not null;default:'standard';comment:视频执行档位" json:"service_tier"`
	Prompt           string          `gorm:"type:text;comment:提示词" json:"prompt"`
	Resolution       string          `gorm:"type:varchar(16);comment:分辨率" json:"resolution"`
	Ratio            string          `gorm:"type:varchar(16);comment:比例" json:"ratio"`
	Duration         int             `gorm:"comment:时长(秒)" json:"duration"`
	GenerateAudio    bool            `gorm:"default:false;comment:是否生成音频" json:"generate_audio"`
	ContentJSON      datatypes.JSON  `gorm:"type:json;comment:内容项" json:"content_json"`
	ParamsJSON       datatypes.JSON  `gorm:"type:json;comment:附加参数" json:"params_json"`
	ChannelID        uint            `gorm:"index:idx_channel_key;comment:渠道ID" json:"channel_id"`
	KeyID            uint            `gorm:"index:idx_channel_key;comment:密钥ID" json:"key_id"`
	AdapterType      string          `gorm:"type:varchar(32);not null;comment:任务使用的协议实现快照" json:"adapter_type"`
	RoutePlan        datatypes.JSON  `gorm:"type:json;comment:不可变视频选路快照" json:"route_plan"`
	ProviderTaskID   string          `gorm:"type:varchar(128);comment:上游任务ID" json:"provider_task_id"`
	ProviderResponse datatypes.JSON  `gorm:"type:json;comment:上游原始响应" json:"-"`
	ProviderMetadata datatypes.JSON  `gorm:"type:json;comment:结构化上游任务元数据" json:"provider_metadata,omitempty"`
	EstimatedCost    decimal.Decimal `gorm:"type:decimal(10,4);comment:预估费用" json:"estimated_cost"`
	MarkupRatio      decimal.Decimal `gorm:"type:decimal(4,2);comment:加价系数" json:"markup_ratio"`
	FinalCost        decimal.Decimal `gorm:"type:decimal(10,4);comment:最终费用" json:"final_cost"`
	BillingStatus    string          `gorm:"type:varchar(16);comment:计费状态" json:"billing_status"`
	ResultJSON       datatypes.JSON  `gorm:"type:json;comment:生成结果" json:"result_json"`
	ErrorMessage     string          `gorm:"type:text;comment:错误信息" json:"error_message"`
	WorkerLease      string          `gorm:"type:varchar(64);not null;default:'';comment:Worker租约所有者" json:"-"`
	WorkerLeaseStage string          `gorm:"type:varchar(16);not null;default:'';comment:Worker租约阶段" json:"-"`
	WorkerLeaseUntil *time.Time      `gorm:"column:worker_lease_expires_at;comment:Worker租约过期时间" json:"-"`
	SubmitCheckpoint datatypes.JSON  `gorm:"type:json;comment:提交恢复检查点" json:"-"`
	PollCount        int             `gorm:"default:0;comment:轮询次数" json:"-"`
	CallbackURL      string          `gorm:"type:varchar(512);comment:回调地址" json:"callback_url"`
	CreatedAt        time.Time       `gorm:"not null;index:idx_token_status,sort:desc;index:idx_status_created;comment:创建时间" json:"created_at"`
	SubmittedAt      *time.Time      `gorm:"comment:提交时间" json:"submitted_at"`
	CompletedAt      *time.Time      `gorm:"comment:完成时间" json:"completed_at"`
}

func (VideoTask) TableName() string { return "video_tasks" }

// VideoAsset 视频素材
type VideoAsset struct {
	ID              string           `gorm:"type:varchar(32);primarykey" json:"id"`
	TokenID         uint             `gorm:"not null;index:idx_token_sha256;comment:令牌ID" json:"token_id"`
	SHA256          string           `gorm:"type:char(64);not null;index:idx_token_sha256;comment:文件哈希" json:"sha256"`
	SizeBytes       int64            `gorm:"not null;comment:文件大小" json:"size_bytes"`
	Kind            string           `gorm:"type:varchar(16);not null;comment:类型(image/video/audio)" json:"kind"`
	ContentType     string           `gorm:"type:varchar(64);not null;comment:MIME类型" json:"content_type"`
	DurationSeconds *float64         `gorm:"type:decimal(6,2);comment:时长(秒)" json:"duration_seconds"`
	Status          VideoAssetStatus `gorm:"type:varchar(16);not null;index:idx_token_sha256;index:idx_expires;comment:状态" json:"status"`
	StoragePath     string           `gorm:"type:varchar(1024);comment:存储路径或公共URL" json:"storage_path"`
	UpstreamRefs    datatypes.JSON   `gorm:"type:json;comment:上游引用缓存" json:"-"`
	ExpiresAt       time.Time        `gorm:"not null;index:idx_expires;comment:过期时间" json:"expires_at"`
	CreatedAt       time.Time        `gorm:"not null;comment:创建时间" json:"created_at"`
}

func (VideoAsset) TableName() string { return "video_assets" }
