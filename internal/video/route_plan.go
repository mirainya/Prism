package video

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const VideoRoutePlanVersion = 1

type VideoRoutePlan struct {
	Version      int                       `json:"version"`
	PublicModel  string                    `json:"public_model"`
	VendorModel  string                    `json:"vendor_model"`
	Channel      VideoRouteChannelSnapshot `json:"channel"`
	KeyID        uint                      `json:"key_id"`
}

type VideoRouteChannelSnapshot struct {
	ID            uint           `json:"id"`
	Name          string         `json:"name"`
	AdapterType   string         `json:"adapter_type"`
	BaseURL       string         `json:"base_url"`
	AssetResolver string         `json:"asset_resolver"`
	ExtraConfig   datatypes.JSON `json:"extra_config,omitempty"`
}

func BuildVideoRoutePlan(channel *VideoChannel, key *VideoChannelKey, publicModel, vendorModel string) (datatypes.JSON, error) {
	if channel == nil || key == nil || channel.ID == 0 || key.ID == 0 {
		return nil, errors.New("video route requires a channel and key")
	}
	publicModel = strings.TrimSpace(publicModel)
	vendorModel = strings.TrimSpace(vendorModel)
	if publicModel == "" || vendorModel == "" {
		return nil, errors.New("video route requires public and vendor models")
	}
	plan := VideoRoutePlan{
		Version: VideoRoutePlanVersion, PublicModel: publicModel, VendorModel: vendorModel, KeyID: key.ID,
		Channel: VideoRouteChannelSnapshot{
			ID: channel.ID, Name: channel.Name, AdapterType: channel.AdapterType,
			BaseURL: channel.BaseURL, AssetResolver: channel.AssetResolver,
			ExtraConfig: append(datatypes.JSON(nil), channel.ExtraConfig...),
		},
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("encode video route plan: %w", err)
	}
	return datatypes.JSON(encoded), nil
}

func DecodeVideoRoutePlan(raw []byte) (*VideoRoutePlan, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var plan VideoRoutePlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return nil, fmt.Errorf("decode video route plan: %w", err)
	}
	if plan.Version != VideoRoutePlanVersion || plan.Channel.ID == 0 || plan.KeyID == 0 ||
		strings.TrimSpace(plan.Channel.AdapterType) == "" || strings.TrimSpace(plan.VendorModel) == "" {
		return nil, errors.New("invalid video route plan")
	}
	return &plan, nil
}

// LoadVideoTaskRoute restores the immutable channel snapshot and the live key
// credential. Tasks created before route snapshots fall back to the old lookup.
func LoadVideoTaskRoute(db *gorm.DB, task *VideoTask) (*VideoChannel, *VideoChannelKey, string, error) {
	if db == nil || task == nil {
		return nil, nil, "", errors.New("video task route context is required")
	}
	plan, err := DecodeVideoRoutePlan(task.RoutePlan)
	if err != nil {
		return nil, nil, "", err
	}
	var channel VideoChannel
	keyID := task.KeyID
	vendorModel := strings.TrimSpace(task.VendorModel)
	if plan != nil {
		channel = VideoChannel{
			ID: plan.Channel.ID, Name: plan.Channel.Name, AdapterType: plan.Channel.AdapterType,
			BaseURL: plan.Channel.BaseURL, AssetResolver: plan.Channel.AssetResolver,
			ExtraConfig: append(datatypes.JSON(nil), plan.Channel.ExtraConfig...),
		}
		keyID = plan.KeyID
		vendorModel = plan.VendorModel
	} else if err := db.First(&channel, task.ChannelID).Error; err != nil {
		return nil, nil, "", fmt.Errorf("load video channel: %w", err)
	}
	var key VideoChannelKey
	if err := db.First(&key, keyID).Error; err != nil {
		return nil, nil, "", fmt.Errorf("load video channel key: %w", err)
	}
	if key.ChannelID != channel.ID {
		return nil, nil, "", errors.New("video route key does not belong to the selected channel")
	}
	if vendorModel == "" {
		vendorModel = task.Model
	}
	return &channel, &key, vendorModel, nil
}
