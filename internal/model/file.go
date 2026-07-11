package model

import "time"

type AIFile struct {
	ID        string    `gorm:"primaryKey;type:varchar(64)" json:"id"`
	UserID    uint      `gorm:"not null;index:idx_ai_files_user" json:"-"`
	TokenID   uint      `gorm:"not null;index:idx_ai_files_token" json:"-"`
	Filename  string    `gorm:"type:varchar(255);not null" json:"filename"`
	Purpose   string    `gorm:"type:varchar(40);not null;index:idx_ai_files_purpose" json:"purpose"`
	Bytes     int64     `gorm:"not null" json:"bytes"`
	MimeType  string    `gorm:"type:varchar(120);not null;default:''" json:"mime_type,omitempty"`
	Content   []byte    `gorm:"type:longblob;not null" json:"-"`
	Status    string    `gorm:"type:varchar(24);not null;default:'processed'" json:"status"`
	CreatedAt time.Time `json:"-"`
}

func (AIFile) TableName() string { return "ai_files" }
