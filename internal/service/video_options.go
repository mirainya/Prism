package service

import (
	"encoding/json"
	"strings"
)

// Only downstream-facing video constraints are exposed. Vendor adapter
// mappings and transport details may coexist in capability_constraints.
var publicVideoOptionKeys = []string{
	"resolutions", "ratios", "duration_min", "duration_max",
	"duration_max_with_video_reference", "duration_options", "task_types",
	"require_visual_media_with_audio", "allow_generated_audio", "allowed_roles",
	"max_images", "max_videos", "max_audios", "max_media", "media_duration_min",
	"media_duration_max", "max_video_duration_total", "max_audio_duration_total",
}

func publicVideoConstraints(raw []byte) (map[string]any, error) {
	result := make(map[string]any)
	if len(raw) == 0 {
		return result, nil
	}
	var source map[string]json.RawMessage
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, err
	}
	for _, key := range publicVideoOptionKeys {
		if raw, exists := source[key]; exists {
			if value, safe := safeVideoField(key, raw); safe {
				result[key] = value
			}
		}
	}
	if rawParameters, exists := source["parameters"]; exists {
		var parameters []map[string]json.RawMessage
		if err := json.Unmarshal(rawParameters, &parameters); err != nil {
			return nil, err
		}
		publicParameters := make([]map[string]any, 0, len(parameters))
		for _, parameter := range parameters {
			publicParameter := copyVideoOptionFields(parameter, "name", "label", "type", "min", "max", "task_modes", "conflicts_with")
			if value, exists := safeVideoOptionValue(parameter["default"]); exists {
				publicParameter["default"] = value
			}
			var options []map[string]json.RawMessage
			if rawOptions, exists := parameter["options"]; exists {
				if err := json.Unmarshal(rawOptions, &options); err != nil {
					return nil, err
				}
			}
			publicOptions := make([]map[string]any, 0, len(options))
			for _, option := range options {
				publicOption := copyVideoOptionFields(option, "label", "adds_resolutions")
				if value, exists := safeVideoOptionValue(option["value"]); exists {
					publicOption["value"] = value
				}
				publicOptions = append(publicOptions, publicOption)
			}
			publicParameter["options"] = publicOptions
			publicParameters = append(publicParameters, publicParameter)
		}
		result["parameters"] = publicParameters
	}
	if rawTiers, exists := source["service_tier_options"]; exists {
		var tiers []map[string]json.RawMessage
		if err := json.Unmarshal(rawTiers, &tiers); err != nil {
			return nil, err
		}
		publicTiers := make([]map[string]any, 0, len(tiers))
		for _, tier := range tiers {
			publicTiers = append(publicTiers, copyVideoOptionFields(tier, "value", "label", "adds_resolutions", "surcharge_percent"))
		}
		result["service_tier_options"] = publicTiers
	}
	return result, nil
}

func copyVideoOptionFields(source map[string]json.RawMessage, keys ...string) map[string]any {
	result := make(map[string]any)
	for _, key := range keys {
		if raw, exists := source[key]; exists {
			if value, safe := safeVideoField(key, raw); safe {
				result[key] = value
			}
		}
	}
	return result
}

func safeVideoField(key string, raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil, false
	}
	switch key {
	case "resolutions", "ratios", "task_types", "allowed_roles", "task_modes", "conflicts_with", "adds_resolutions":
		items, ok := value.([]any)
		if !ok {
			return nil, false
		}
		for _, item := range items {
			text, ok := item.(string)
			if !ok || strings.Contains(text, "://") {
				return nil, false
			}
		}
		return value, true
	case "duration_options":
		items, ok := value.([]any)
		if !ok {
			return nil, false
		}
		for _, item := range items {
			if _, ok := item.(float64); !ok {
				return nil, false
			}
		}
		return value, true
	case "require_visual_media_with_audio", "allow_generated_audio":
		_, ok := value.(bool)
		return value, ok
	case "duration_min", "duration_max", "duration_max_with_video_reference", "max_images", "max_videos", "max_audios", "max_media", "media_duration_min", "media_duration_max", "max_video_duration_total", "max_audio_duration_total", "min", "max", "surcharge_percent":
		_, ok := value.(float64)
		return value, ok
	case "name", "label", "type", "value":
		text, ok := value.(string)
		return value, ok && !strings.Contains(text, "://")
	}
	return nil, false
}
func safeVideoOptionValue(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil, false
	}
	switch typed := value.(type) {
	case string:
		if strings.Contains(typed, "://") {
			return nil, false
		}
		return typed, true
	case bool, float64:
		return value, true
	}
	return nil, false
}
