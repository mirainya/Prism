package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// Channel 渠道
type Channel struct {
	ID             uint
	Type           string
	Name           string
	BaseURL        string
	CallbackSecret string
	Config         map[string]any
	Status         int8
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ChannelAccount 渠道账号
type ChannelAccount struct {
	ID           uint
	ChannelID    uint
	Name         string
	APIKey       string
	Config       map[string]any
	Weight       int
	MaxTasks     int
	CurrentTasks int
	Status       int8
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ChannelCapability 渠道能力配置
type ChannelCapability struct {
	ID              uint
	ChannelID       uint
	CapabilityCode  string
	Model           string
	Name            string
	Price           decimal.Decimal
	PriceUnit       string
	ResultMode      string
	RequestPath     string
	RequestMethod   string
	ContentType     string
	AuthLocation    string
	AuthKey         string
	AuthValuePrefix string
	PollPath        string
	PollMethod      string
	PollInterval    int
	PollMaxAttempts int
	ParamMapping    map[string]any
	ResponseMapping map[string]any
	CallbackMapping map[string]any
	ExtraConfig     map[string]any
	Status          int8
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Capability 能力定义
type Capability struct {
	Code             string
	Name             string
	Type             string
	StandardParams   map[string]any
	StandardResponse map[string]any
	Status           int8
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ChatModel 聊天模型
type ChatModel struct {
	Code         string
	Name         string
	Provider     string
	Description  string
	Status       int8
	MaxTokens    int
	Features     []string
	DiscoveredAt *time.Time
	LastSyncAt   *time.Time
	Deprecated   bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ChatModelChannel 模型-渠道映射
type ChatModelChannel struct {
	ID            uint
	ModelCode     string
	ChannelID     uint
	VendorModel   string
	Priority      int
	PriceMode     string
	InputPrice    decimal.Decimal
	OutputPrice   decimal.Decimal
	RequestPath   string
	Timeout       int
	SupportsStream *bool
	DefaultStream  *bool
	ExtraHeaders  map[string]string
	ExtraConfig   map[string]any
	Status        int8
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Token API 令牌
type Token struct {
	ID        uint
	UserID    uint
	Name      string
	Key       string
	KeyHint   string
	PlainKey  string
	Balance   decimal.Decimal
	TotalUsed decimal.Decimal
	RateLimit int
	Status    int8
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TokenChannelPriority 令牌渠道优先级
type TokenChannelPriority struct {
	ID        uint
	TokenID   uint
	ChannelID uint
	Priority  int
}

// User 用户
type User struct {
	ID        uint
	Username  string
	Password  string
	Role      string
	Status    int8
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TaskStatus 任务状态
type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusProcessing TaskStatus = "processing"
	TaskStatusSuccess    TaskStatus = "success"
	TaskStatusFailed     TaskStatus = "failed"
	TaskStatusCancelled  TaskStatus = "cancelled"
)

// Task 异步任务
type Task struct {
	ID                  uint
	TaskNo              string
	UserID              uint
	TokenID             uint
	CapabilityCode      string
	ChannelID           uint
	ChannelCapabilityID uint
	AccountID           uint
	VendorTaskID        string
	Status              TaskStatus
	Progress            int
	CallbackURL         string
	CallbackStatus      string
	CallbackAttempts    int
	RequestParams       map[string]any
	MappedParams        map[string]any
	VendorResponse      map[string]any
	Result              map[string]any
	ErrorMessage        string
	Cost                decimal.Decimal
	Refunded            bool
	StartedAt           *time.Time
	CompletedAt         *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// Conversation 对话
type Conversation struct {
	ID               uint
	UserID           uint
	TokenID          uint
	Title            string
	Model            string
	SystemPrompt     string
	LastRequestLogID uint
	LastStatus       string
	TotalTokens      int
	MessageCount     int
	Status           int8
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Message 消息
type Message struct {
	ID               uint
	ConversationID   uint
	RequestLogID     uint
	Role             string
	Content          string
	ReasoningContent string
	FinishReason     string
	InputTokens      int
	OutputTokens     int
	Model            string
	ChannelID        uint
	AccountID        uint
	LatencyMs        int
	Cost             decimal.Decimal
	CreatedAt        time.Time
}

// BillingType 计费类型
type BillingType string

const (
	BillingTypeDeduct BillingType = "deduct"
	BillingTypeRefund BillingType = "refund"
)

// BillingLog 计费日志
type BillingLog struct {
	ID            uint
	IdempotentKey string
	TokenID       uint
	UserID        uint
	Amount        decimal.Decimal
	Type          BillingType
	Status        string
	Remark        string
	CreatedAt     time.Time
}

// ChannelRequestLog 请求日志
type ChannelRequestLog struct {
	ID                    uint
	TaskID                uint
	TaskNo                string
	ConversationID        uint
	ChannelID             uint
	AccountID             uint
	CapabilityCode        string
	RequestType           string
	IsStream              bool
	ModelCode             string
	VendorModel           string
	RequestPath           string
	FinishReason          string
	ResponsePreview       string
	UsagePromptTokens     int
	UsageCompletionTokens int
	UsageTotalTokens      int
	Method                string
	URL                   string
	RequestHeaders        string
	RequestBody           string
	StatusCode            int
	ResponseBody          string
	DurationMs            int64
	ErrorMessage          string
	RequestAt             time.Time
	CreatedAt             time.Time
}

// --- 查询过滤器 ---

type ChannelFilter struct {
	Status *int8
	Type   string
	Page   int
	Size   int
}

type ChannelCapabilityFilter struct {
	ChannelID      uint
	CapabilityCode string
	Status         *int8
	Page           int
	Size           int
}

type ChatModelFilter struct {
	Provider string
	Status   *int8
	Keyword  string
	Page     int
	Size     int
}

type TokenFilter struct {
	UserID uint
	Status *int8
	Page   int
	Size   int
}

type UserFilter struct {
	Role    string
	Keyword string
	Page    int
	Size    int
}

type TaskFilter struct {
	TokenID        uint
	CapabilityCode string
	Status         TaskStatus
	Page           int
	Size           int
}

type RequestLogFilter struct {
	ChannelID      uint
	CapabilityCode string
	TokenID        uint
	Status         string
	StartTime      *time.Time
	EndTime        *time.Time
	Page           int
	Size           int
}

// --- 路由相关 ---

type RoutingQuery struct {
	TokenID        uint
	Model          string
	CapabilityCode string
	ExcludeIDs     []uint
}

type RoutingResult struct {
	Channel           *Channel
	Account           *ChannelAccount
	ChannelCapability *ChannelCapability
}

// --- 统计相关 ---

type DashboardOverview struct {
	TodayRequests   int64
	TodayTokens     int64
	TodaySuccessRate float64
	ActiveChannels  int64
}

type DailyStats struct {
	Date     string
	Requests int64
	Tokens   int64
}

type ChannelStats struct {
	ChannelID   uint
	ChannelName string
	Count       int64
}
