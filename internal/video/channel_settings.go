package video

import (
	"encoding/json"
	"strings"
)

const (
	CancelModeDisabled  = "disabled"
	CancelModeLocalOnly = "local_only"
	CancelModeProvider  = "provider"

	PricingModeFixed            = "fixed"
	PricingModeUpstreamEstimate = "upstream_estimate"
)

type legacyVideoChannelExtraConfig struct {
	Adapter struct {
		Profile        string `json:"profile"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		Cancel         struct {
			Enabled bool `json:"enabled"`
		} `json:"cancel"`
		LocalCancel struct {
			Enabled *bool `json:"enabled"`
		} `json:"local_cancel"`
	} `json:"adapter"`
	ResultStorage struct {
		Enabled bool `json:"enabled"`
	} `json:"result_storage"`
}

func (c *VideoChannel) Capability(name string) bool {
	if c == nil {
		return false
	}
	var formal *bool
	switch name {
	case "first_frame":
		formal = c.SupportsFirstFrame
	case "last_frame":
		formal = c.SupportsLastFrame
	case "audio":
		formal = c.SupportsAudio
	case "web_search":
		formal = c.SupportsWebSearch
	case "cancel":
		if mode := strings.TrimSpace(c.CancelMode); mode != "" {
			return mode == CancelModeProvider
		}
		if c.AdapterType == AdapterTypeGeneric || c.AdapterType == AdapterTypeSeedance {
			return c.EffectiveCancelMode() == CancelModeProvider
		}
		return c.legacyCapabilities()[name]
	}
	if formal != nil {
		return *formal
	}
	return c.legacyCapabilities()[name]
}

func (c *VideoChannel) EffectiveCancelMode() string {
	if c == nil {
		return CancelModeDisabled
	}
	mode := strings.ToLower(strings.TrimSpace(c.CancelMode))
	if mode == CancelModeDisabled || mode == CancelModeLocalOnly || mode == CancelModeProvider {
		return mode
	}
	legacy := c.legacyExtraConfig()
	if c.AdapterType == AdapterTypeGeneric {
		if legacy.Adapter.Cancel.Enabled {
			return CancelModeProvider
		}
		if legacy.Adapter.LocalCancel.Enabled == nil || *legacy.Adapter.LocalCancel.Enabled {
			return CancelModeLocalOnly
		}
		return CancelModeDisabled
	}
	if c.AdapterType == AdapterTypeSeedance {
		return CancelModeProvider
	}
	return CancelModeDisabled
}

func (c *VideoChannel) EffectiveAdapterProfile() string {
	if c == nil {
		return ""
	}
	if profile := strings.TrimSpace(c.AdapterProfile); profile != "" {
		return profile
	}
	return strings.TrimSpace(c.legacyExtraConfig().Adapter.Profile)
}

func (c *VideoChannel) EffectiveRequestTimeoutSeconds() int {
	if c == nil {
		return 30
	}
	if c.RequestTimeoutSeconds > 0 {
		return c.RequestTimeoutSeconds
	}
	if timeout := c.legacyExtraConfig().Adapter.TimeoutSeconds; timeout > 0 {
		return timeout
	}
	return 30
}

func (c *VideoChannel) EffectiveResultStorageEnabled() bool {
	if c == nil {
		return false
	}
	if c.ResultStorageEnabled != nil {
		return *c.ResultStorageEnabled
	}
	return c.legacyExtraConfig().ResultStorage.Enabled
}

func (c *VideoChannel) legacyCapabilities() map[string]bool {
	declared := make(map[string]bool)
	if c != nil {
		_ = json.Unmarshal(c.Capabilities, &declared)
	}
	return declared
}

func (c *VideoChannel) legacyExtraConfig() legacyVideoChannelExtraConfig {
	var config legacyVideoChannelExtraConfig
	if c != nil {
		_ = json.Unmarshal(c.ExtraConfig, &config)
	}
	return config
}
