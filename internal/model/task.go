package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
)

type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusProcessing TaskStatus = "processing"
	TaskStatusFinalizing TaskStatus = "finalizing"
	TaskStatusSuccess    TaskStatus = "success"
	TaskStatusFailed     TaskStatus = "failed"
	TaskStatusCancelled  TaskStatus = "cancelled"
)

func (s TaskStatus) IsTerminal() bool {
	return s == TaskStatusSuccess || s == TaskStatusFailed || s == TaskStatusCancelled
}

// Public hides the internal result-finalization state from API clients.
func (s TaskStatus) Public() TaskStatus {
	if s == TaskStatusFinalizing {
		return TaskStatusProcessing
	}
	return s
}

// Task 任务记录
type Task struct {
	BaseModel
	TaskNo     string `gorm:"type:varchar(32);uniqueIndex;not null;comment:任务编号" json:"task_no"`
	CallID     string `gorm:"type:varchar(64);not null;default:'';index;comment:关联调用ID" json:"call_id"`
	UserID     uint   `gorm:"index;comment:用户ID" json:"user_id"`
	TokenID    uint   `gorm:"index;comment:令牌ID" json:"token_id"`
	ModelCode  string `gorm:"type:varchar(80);index;comment:模型标识" json:"model_code"`
	ChannelID  uint   `gorm:"index;comment:渠道ID" json:"channel_id"`
	EndpointID uint   `gorm:"comment:端点配置ID" json:"endpoint_id"`
	AccountID  uint   `gorm:"comment:渠道账号ID" json:"account_id"`

	VendorTaskID string `gorm:"type:varchar(100);index;comment:供应商任务ID" json:"vendor_task_id"`

	Status   TaskStatus `gorm:"type:varchar(20);index;default:'pending';comment:任务状态" json:"status"`
	Progress int        `gorm:"default:0;comment:进度(0-100)" json:"progress"`

	CallbackURL      string         `gorm:"type:varchar(500);comment:回调地址" json:"callback_url"`
	CallbackStatus   CallbackStatus `gorm:"type:varchar(20);comment:回调状态" json:"callback_status"`
	CallbackAttempts int            `gorm:"default:0;comment:回调尝试次数" json:"callback_attempts"`

	RequestParams    datatypes.JSON `gorm:"type:json;comment:原始请求参数" json:"request_params"`
	MappedParams     datatypes.JSON `gorm:"type:json;comment:映射后参数" json:"mapped_params"`
	SubmitCheckpoint datatypes.JSON `gorm:"type:json;comment:上游提交恢复检查点" json:"-"`
	VendorResponse   datatypes.JSON `gorm:"type:json;comment:供应商原始响应" json:"vendor_response"`
	Result           datatypes.JSON `gorm:"type:json;comment:统一结果" json:"result"`
	ErrorMessage     string         `gorm:"type:text;comment:错误信息" json:"error_message"`

	Cost                 decimal.Decimal `gorm:"type:decimal(20,8);not null;default:0;comment:费用" json:"cost"`
	Refunded             bool            `gorm:"default:false;comment:是否已退款" json:"refunded"`
	AccountSlotReleased  bool            `gorm:"default:false;comment:账号并发槽位是否已释放" json:"-"`
	WorkerLeaseOwner     string          `gorm:"type:varchar(64);not null;default:'';comment:任务执行租约所有者" json:"-"`
	WorkerLeaseStage     string          `gorm:"type:varchar(24);not null;default:'';comment:任务执行租约阶段" json:"-"`
	WorkerLeaseExpiresAt *time.Time      `gorm:"index;comment:任务执行租约过期时间" json:"-"`
	PollCursor           int             `gorm:"not null;default:0;comment:下一轮轮询序号" json:"-"`
	StartedAt            *time.Time      `gorm:"comment:开始时间" json:"started_at"`
	CompletedAt          *time.Time      `gorm:"comment:完成时间" json:"completed_at"`

	// 关联
	Channel  *Channel  `gorm:"foreignKey:ChannelID" json:"channel,omitempty"`
	Endpoint *Endpoint `gorm:"foreignKey:EndpointID" json:"endpoint,omitempty"`
}

func (Task) TableName() string {
	return "tasks"
}

// CallbackStatus 回调状态
type CallbackStatus string

const (
	CallbackStatusPending CallbackStatus = "pending"
	CallbackStatusSuccess CallbackStatus = "success"
	CallbackStatusFailed  CallbackStatus = "failed"
)
