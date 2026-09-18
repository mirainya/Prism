package model

import "time"

type AIFile struct {
	ID        string    `gorm:"primaryKey;type:varchar(64)" json:"id"`
	UserID    uint      `gorm:"not null;index:idx_gw_file_resources_user_created,priority:1" json:"-"`
	TokenID   uint      `gorm:"not null;index:idx_gw_file_resources_token_created,priority:1" json:"-"`
	Filename  string    `gorm:"type:varchar(255);not null" json:"filename"`
	Purpose   string    `gorm:"type:varchar(40);not null;index:idx_gw_file_resources_purpose" json:"purpose"`
	Bytes     int64     `gorm:"not null" json:"bytes"`
	MimeType  string    `gorm:"type:varchar(120);not null" json:"mime_type,omitempty"`
	Status    string    `gorm:"type:varchar(24);not null;default:'uploading'" json:"status"`
	CreatedAt time.Time `gorm:"index:idx_gw_file_resources_user_created,priority:2;index:idx_gw_file_resources_token_created,priority:2" json:"-"`
	UpdatedAt time.Time `json:"-"`

	// Content and ObjectURL are transient values loaded from object storage.
	// File bytes are never persisted in the application database.
	Content   []byte `gorm:"-" json:"-"`
	ObjectURL string `gorm:"-" json:"-"`
}

func (AIFile) TableName() string { return "gw_file_resources" }

type MediaAsset struct {
	ID             uint64     `gorm:"primaryKey;autoIncrement;uniqueIndex:uq_gw_media_assets_id_owner,priority:1"`
	UserID         uint       `gorm:"not null;uniqueIndex:uq_gw_media_assets_id_owner,priority:2"`
	TokenID        uint       `gorm:"not null;uniqueIndex:uq_gw_media_assets_id_owner,priority:3"`
	AttemptID      *uint64    `gorm:"index" json:"-"`
	Purpose        string     `gorm:"type:varchar(16);not null"`
	ObjectKey      string     `gorm:"type:varchar(512);not null;uniqueIndex"`
	StorageLocator string     `gorm:"type:varchar(2048)"`
	ObjectVersion  string     `gorm:"type:varchar(255)"`
	ContentType    string     `gorm:"type:varchar(128);not null"`
	ContentLength  uint64     `gorm:"not null"`
	SHA256         string     `gorm:"type:char(64);not null"`
	State          string     `gorm:"type:varchar(16);not null"`
	StateVersion   uint64     `gorm:"not null;default:1"`
	RetentionUntil *time.Time `json:"-"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (MediaAsset) TableName() string { return "gw_media_assets" }

type MediaAssetRef struct {
	ID               uint64  `gorm:"primaryKey;autoIncrement"`
	MediaAssetID     uint64  `gorm:"not null"`
	UserID           uint    `gorm:"not null"`
	TokenID          uint    `gorm:"not null"`
	Role             string  `gorm:"type:varchar(16);not null;uniqueIndex:uq_gw_media_asset_refs_file_role_ordinal,priority:2"`
	Ordinal          uint32  `gorm:"not null;default:0;uniqueIndex:uq_gw_media_asset_refs_file_role_ordinal,priority:3"`
	AIFileID         *string `gorm:"type:varchar(64);uniqueIndex:uq_gw_media_asset_refs_file_role_ordinal,priority:1"`
	CallID           *uint64
	ResultDeliveryID *uint64
	CreatedAt        time.Time
}

func (MediaAssetRef) TableName() string { return "gw_media_asset_refs" }

type MediaAssetStateEvent struct {
	ID           uint64  `gorm:"primaryKey;autoIncrement"`
	MediaAssetID uint64  `gorm:"not null;uniqueIndex:uq_gw_media_asset_state_events_version,priority:1"`
	OldState     *string `gorm:"type:varchar(16)"`
	NewState     string  `gorm:"type:varchar(16);not null"`
	StateVersion uint64  `gorm:"not null;uniqueIndex:uq_gw_media_asset_state_events_version,priority:2"`
	ReasonCode   string  `gorm:"type:varchar(64);not null"`
	CreatedAt    time.Time
}

func (MediaAssetStateEvent) TableName() string { return "gw_media_asset_state_events" }
