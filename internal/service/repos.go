package service

import (
	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/repository"
	"gorm.io/gorm"
)

// Repos 全局 Repository 注册表，供 service 层使用
var Repos *repository.Registry

// InitRepos 初始化全局 Repository（在 main 中调用）
func InitRepos(db *gorm.DB) {
	Repos = repository.NewRegistry(db)
}

// 便捷方法：获取各 Repository 接口
func ChannelRepo() domain.ChannelRepository        { return Repos.Channel }
func AccountRepo() domain.ChannelAccountRepository { return Repos.ChannelAccount }
func TokenRepo() domain.TokenRepository            { return Repos.Token }
func UserRepo() domain.UserRepository              { return Repos.User }
func TaskRepo() domain.TaskRepository              { return Repos.Task }
func BillingRepo() domain.BillingRepository        { return Repos.Billing }
func LogRepo() domain.RequestLogRepository         { return Repos.RequestLog }
func ConvRepo() domain.ConversationRepository      { return Repos.Conversation }
func ModelRepo() *repository.ModelRepo             { return Repos.Model }
func EndpointRepo() *repository.EndpointRepo       { return Repos.Endpoint }
