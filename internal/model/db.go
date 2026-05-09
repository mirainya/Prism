package model

import "gorm.io/gorm"

var db *gorm.DB

// DB 返回一个新的数据库 session，避免 session 污染
func DB() *gorm.DB {
	return db.Session(&gorm.Session{})
}

// SetDB 设置数据库连接
func SetDB(d *gorm.DB) {
	db = d
}

// AutoMigrate 自动迁移数据库表结构
func AutoMigrate() error {
	return db.AutoMigrate(
		&User{},
		&Token{},
		&Channel{},
		&ChannelAccount{},
		&Capability{},
		&ChannelCapability{},
		&Task{},
		&ChannelRequestLog{},
		&TokenChannelPriority{},
		&ChatModel{},
		&ChatModelChannel{},
		&Conversation{},
		&Message{},
		&BillingLog{},
	)
}
