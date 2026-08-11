package service

import (
	"encoding/json"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/datatypes"
)

type endpointOriginSnapshot struct {
	ChannelID        uint   `json:"channel_id,omitempty"`
	ChannelName      string `json:"channel_name,omitempty"`
	ChannelType      string `json:"channel_type,omitempty"`
	AccountID        uint   `json:"account_id,omitempty"`
	AccountName      string `json:"account_name,omitempty"`
	VendorModel      string `json:"vendor_model,omitempty"`
	Adapter          string `json:"adapter,omitempty"`
	SourceEndpointID uint   `json:"source_endpoint_id,omitempty"`
}

func buildEndpointOriginSnapshot(
	channel *model.Channel,
	account *model.ChannelAccount,
	vendorModel string,
	adapter string,
	sourceEndpointID uint,
) datatypes.JSON {
	snapshot := endpointOriginSnapshot{
		VendorModel:      vendorModel,
		Adapter:          adapter,
		SourceEndpointID: sourceEndpointID,
	}
	if channel != nil {
		snapshot.ChannelID = channel.ID
		snapshot.ChannelName = channel.Name
		snapshot.ChannelType = channel.Type
	}
	if account != nil {
		snapshot.AccountID = account.ID
		snapshot.AccountName = account.Name
	}
	encoded, _ := json.Marshal(snapshot)
	return datatypes.JSON(encoded)
}
