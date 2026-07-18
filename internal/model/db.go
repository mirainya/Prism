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
