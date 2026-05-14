package repository

import "gorm.io/gorm"

// Registry 持有所有 Repository 实例，供 service 层注入使用
type Registry struct {
	Channel        *ChannelRepo
	ChannelAccount *ChannelAccountRepo
	Token          *TokenRepo
	User           *UserRepo
	Task           *TaskRepo
	Billing        *BillingRepo
	RequestLog     *RequestLogRepo
	Conversation   *ConversationRepo
	Model          *ModelRepo
	Endpoint       *EndpointRepo
}

// NewRegistry 创建所有 Repository 实例
func NewRegistry(db *gorm.DB) *Registry {
	return &Registry{
		Channel:        NewChannelRepo(db),
		ChannelAccount: NewChannelAccountRepo(db),
		Token:          NewTokenRepo(db),
		User:           NewUserRepo(db),
		Task:           NewTaskRepo(db),
		Billing:        NewBillingRepo(db),
		RequestLog:     NewRequestLogRepo(db),
		Conversation:   NewConversationRepo(db),
		Model:          NewModelRepo(db),
		Endpoint:       NewEndpointRepo(db),
	}
}
