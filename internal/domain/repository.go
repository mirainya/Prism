package domain

import (
	"context"

	"github.com/shopspring/decimal"
)

// ChannelRepository 渠道数据访问
type ChannelRepository interface {
	FindByID(ctx context.Context, id uint) (*Channel, error)
	FindByType(ctx context.Context, channelType string) (*Channel, error)
	List(ctx context.Context, filter ChannelFilter) ([]Channel, int64, error)
	Create(ctx context.Context, channel *Channel) error
	Update(ctx context.Context, channel *Channel) error
	Delete(ctx context.Context, id uint) error
}

// ChannelAccountRepository 渠道账号数据访问
type ChannelAccountRepository interface {
	FindByID(ctx context.Context, id uint) (*ChannelAccount, error)
	ListByChannel(ctx context.Context, channelID uint) ([]ChannelAccount, error)
	Create(ctx context.Context, account *ChannelAccount) error
	Update(ctx context.Context, account *ChannelAccount) error
	Delete(ctx context.Context, id uint) error
	// SelectAvailable 选择可用账号（带行锁+递增 current_tasks）
	SelectAvailable(ctx context.Context, channelID uint) (*ChannelAccount, error)
	IncrementTasks(ctx context.Context, id uint) error
	DecrementTasks(ctx context.Context, id uint) error
}

// ChannelCapabilityRepository 渠道能力配置数据访问
type ChannelCapabilityRepository interface {
	FindByID(ctx context.Context, id uint) (*ChannelCapability, error)
	FindByModel(ctx context.Context, model string) ([]ChannelCapability, error)
	List(ctx context.Context, filter ChannelCapabilityFilter) ([]ChannelCapability, int64, error)
	Create(ctx context.Context, cc *ChannelCapability) error
	Update(ctx context.Context, cc *ChannelCapability) error
	Delete(ctx context.Context, id uint) error
	// RouteByModel 单次查询完成路由（JOIN channel + account，排除指定渠道）
	RouteByModel(ctx context.Context, req RoutingQuery) (*RoutingResult, error)
}

// ChatModelRepository 聊天模型数据访问
type ChatModelRepository interface {
	FindByCode(ctx context.Context, code string) (*ChatModel, error)
	List(ctx context.Context, filter ChatModelFilter) ([]ChatModel, int64, error)
	ListActive(ctx context.Context) ([]ChatModel, error)
	Create(ctx context.Context, model *ChatModel) error
	Update(ctx context.Context, model *ChatModel) error
	Delete(ctx context.Context, code string) error
	// BulkUpsert 批量更新或插入（模型发现用）
	BulkUpsert(ctx context.Context, models []ChatModel) error
}

// ChatModelChannelRepository 模型-渠道映射
type ChatModelChannelRepository interface {
	FindByID(ctx context.Context, id uint) (*ChatModelChannel, error)
	ListByModel(ctx context.Context, modelCode string) ([]ChatModelChannel, error)
	ListByChannel(ctx context.Context, channelID uint) ([]ChatModelChannel, error)
	Create(ctx context.Context, mc *ChatModelChannel) error
	Update(ctx context.Context, mc *ChatModelChannel) error
	Delete(ctx context.Context, id uint) error
	// SelectForChat 为聊天请求选择最优渠道映射（含 token 优先级）
	SelectForChat(ctx context.Context, tokenID uint, modelCode string) (*ChatModelChannel, error)
}

// TokenRepository API 令牌数据访问
type TokenRepository interface {
	FindByID(ctx context.Context, id uint) (*Token, error)
	FindByKey(ctx context.Context, key string) (*Token, error)
	List(ctx context.Context, filter TokenFilter) ([]Token, int64, error)
	Create(ctx context.Context, token *Token) error
	Update(ctx context.Context, token *Token) error
	Delete(ctx context.Context, id uint) error
	UpdateBalance(ctx context.Context, id uint, delta decimal.Decimal) error
}

// UserRepository 用户数据访问
type UserRepository interface {
	FindByID(ctx context.Context, id uint) (*User, error)
	FindByUsername(ctx context.Context, username string) (*User, error)
	List(ctx context.Context, filter UserFilter) ([]User, int64, error)
	Create(ctx context.Context, user *User) error
	Update(ctx context.Context, user *User) error
	Delete(ctx context.Context, id uint) error
}

// TaskRepository 异步任务数据访问
type TaskRepository interface {
	FindByID(ctx context.Context, id uint) (*Task, error)
	FindByTaskNo(ctx context.Context, taskNo string) (*Task, error)
	List(ctx context.Context, filter TaskFilter) ([]Task, int64, error)
	Create(ctx context.Context, task *Task) error
	UpdateStatus(ctx context.Context, id uint, status TaskStatus, updates map[string]any) error
}

// BillingRepository 计费数据访问
type BillingRepository interface {
	Deduct(ctx context.Context, log *BillingLog) error
	Refund(ctx context.Context, log *BillingLog) error
	ListByToken(ctx context.Context, tokenID uint, limit int) ([]BillingLog, error)
}

// ConversationRepository 对话数据访问
type ConversationRepository interface {
	FindByID(ctx context.Context, id uint) (*Conversation, error)
	ListByUser(ctx context.Context, userID uint, limit, offset int) ([]Conversation, int64, error)
	Create(ctx context.Context, conv *Conversation) error
	Delete(ctx context.Context, id uint) error
	AddMessage(ctx context.Context, msg *Message) error
	ListMessages(ctx context.Context, conversationID uint) ([]Message, error)
}

// RequestLogRepository 请求日志数据访问
type RequestLogRepository interface {
	Create(ctx context.Context, log *ChannelRequestLog) error
	Update(ctx context.Context, log *ChannelRequestLog) error
	FindByID(ctx context.Context, id uint) (*ChannelRequestLog, error)
	List(ctx context.Context, filter RequestLogFilter) ([]ChannelRequestLog, int64, error)
	// Stats 统计查询
	CountToday(ctx context.Context) (int64, error)
	CountByChannel(ctx context.Context, days int) ([]ChannelStats, error)
}

// CapabilityRepository 能力定义数据访问
type CapabilityRepository interface {
	FindByCode(ctx context.Context, code string) (*Capability, error)
	List(ctx context.Context) ([]Capability, error)
	Create(ctx context.Context, cap *Capability) error
	Update(ctx context.Context, cap *Capability) error
	Delete(ctx context.Context, code string) error
}

// DashboardRepository 仪表盘统计
type DashboardRepository interface {
	TodayOverview(ctx context.Context) (*DashboardOverview, error)
	WeeklyTrend(ctx context.Context) ([]DailyStats, error)
	ChannelDistribution(ctx context.Context, days int) ([]ChannelStats, error)
}
