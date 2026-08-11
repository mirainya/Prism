package model

import (
	"time"

	"gorm.io/datatypes"
)

// EndpointAdapter binds a protocol adapter to one Endpoint. The binding is
// separate from the Endpoint routing row so adapter revisions can be rotated
// without changing the selected model or account.
type EndpointAdapter struct {
	BaseModel
	EndpointID       uint           `gorm:"not null;uniqueIndex:idx_endpoint_adapter;index" json:"endpoint_id"`
	Code             string         `gorm:"type:varchar(64);not null;uniqueIndex:idx_endpoint_adapter" json:"code"`
	ActiveRevisionID uint           `gorm:"not null;index" json:"active_revision_id"`
	Status           int8           `gorm:"not null;default:1;index" json:"status"`
	Config           datatypes.JSON `gorm:"type:json" json:"config"`

	Endpoint       *Endpoint                 `gorm:"foreignKey:EndpointID" json:"endpoint,omitempty"`
	ActiveRevision *EndpointAdapterRevision  `gorm:"foreignKey:ActiveRevisionID" json:"active_revision,omitempty"`
	Revisions      []EndpointAdapterRevision `gorm:"foreignKey:AdapterID" json:"revisions,omitempty"`
}

func (EndpointAdapter) TableName() string { return "endpoint_adapters" }

// EndpointAdapterRevision is an immutable adapter configuration snapshot.
type EndpointAdapterRevision struct {
	BaseModel
	AdapterID uint           `gorm:"not null;uniqueIndex:idx_endpoint_adapter_revision;index" json:"adapter_id"`
	Version   int            `gorm:"not null;uniqueIndex:idx_endpoint_adapter_revision" json:"version"`
	Digest    string         `gorm:"type:char(64);not null;index" json:"digest"`
	Config    datatypes.JSON `gorm:"type:json;not null" json:"config"`
	CreatedBy uint           `gorm:"not null;default:0;index" json:"created_by"`

	Adapter *EndpointAdapter `gorm:"foreignKey:AdapterID" json:"adapter,omitempty"`
}

func (EndpointAdapterRevision) TableName() string { return "endpoint_adapter_revisions" }

// EndpointRouteState stores discovery and temporary availability state for a
// concrete endpoint/account/operation tuple.
type EndpointRouteState struct {
	ID             uint           `gorm:"primaryKey" json:"id"`
	EndpointID     uint           `gorm:"not null;uniqueIndex:idx_endpoint_route_state;index" json:"endpoint_id"`
	AccountID      uint           `gorm:"not null;uniqueIndex:idx_endpoint_route_state;index" json:"account_id"`
	RouteOperation string         `gorm:"type:varchar(40);not null;uniqueIndex:idx_endpoint_route_state" json:"route_operation"`
	AdapterID      uint           `gorm:"not null;index" json:"adapter_id"`
	RevisionID     uint           `gorm:"not null;index" json:"revision_id"`
	Status         int8           `gorm:"not null;default:1;index" json:"status"`
	DisabledUntil  *time.Time     `gorm:"index" json:"disabled_until"`
	StatusCode     int            `gorm:"default:0" json:"status_code"`
	FailCount      int            `gorm:"default:0" json:"fail_count"`
	LastDiscovery  *time.Time     `gorm:"column:last_discovery_at" json:"last_discovery_at"`
	LastSuccess    *time.Time     `gorm:"column:last_success_at" json:"last_success_at"`
	LastError      string         `gorm:"type:varchar(1000);default:''" json:"last_error"`
	Discovered     datatypes.JSON `gorm:"column:discovered_models;type:json" json:"discovered_models"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

func (EndpointRouteState) TableName() string { return "endpoint_route_states" }
