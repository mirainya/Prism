package model

import "gorm.io/gorm"

var db *gorm.DB

// DB 返回一个新的数据库 session，避免 session 污染
func DB() *gorm.DB {
	return db.Session(&gorm.Session{NewDB: true})
}

func HasDB() bool { return db != nil }

// SetDB 设置数据库连接
func SetDB(d *gorm.DB) {
	db = d
}

// AutoMigrate 自动迁移数据库表结构
func AutoMigrate() error {
	if err := db.AutoMigrate(
		&User{},
		&Token{},
		&Channel{},
		&ChannelAccount{},
		&Model{},
		&Endpoint{},
		&Task{},
		&ChannelRequestLog{},
		&TokenChannelPriority{},
		&Conversation{},
		&Message{},
		&ConversationTurn{},
		&ConversationItem{},
		&ConversationProjectionOutbox{},
		&BillingLog{},
		&APICall{},
		&APICallAttempt{},
		&APICallPayload{},
		&BalanceEntry{},
		&APIAccessLog{},
		&AuditEvent{},
		&AccountModelState{},
		&AccountModel{},
		// 聊天网关路由表(与老表并存)
		&GwChannel{},
		&GwChannelKey{},
		&GwAbility{},
		&GwAbilityTransport{},
		&GwRouteState{},
		&GwModelMeta{},
		&AIResponse{},
		&AIResponseIdempotencyCache{},
		&AIFile{},
	); err != nil {
		return err
	}
	migrator := db.Migrator()
	if migrator.HasIndex(&Conversation{}, conversationCanonicalMatchIndexName) {
		return nil
	}
	return migrator.CreateIndex(&conversationCanonicalMatchIndex{}, conversationCanonicalMatchIndexName)
}
