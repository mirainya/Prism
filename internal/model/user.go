package model

import "github.com/shopspring/decimal"

type UserRole string

const (
	UserRoleAdmin UserRole = "admin"
	UserRoleUser  UserRole = "user"
)

type User struct {
	BaseModel
	Username       string          `gorm:"type:varchar(50);uniqueIndex;not null;comment:用户名" json:"username"`
	Password       string          `gorm:"type:varchar(100);not null;comment:密码(加密)" json:"-"`
	Role           UserRole        `gorm:"type:varchar(10);default:'user';comment:角色(admin/user)" json:"role"`
	Balance        decimal.Decimal `gorm:"type:decimal(20,8);not null;default:0;comment:账户余额" json:"balance"`
	Status         int8            `gorm:"default:1;comment:状态(1启用/0禁用)" json:"status"`
	SessionVersion uint64          `gorm:"not null;default:0;comment:控制台会话版本" json:"-"`
}

func (User) TableName() string {
	return "users"
}
