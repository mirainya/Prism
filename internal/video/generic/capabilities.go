package generic

import (
	"context"
	"errors"
	"strings"

	"github.com/mirainya/Prism/internal/video"
	"github.com/tidwall/gjson"
)

func (a *Adapter) DiscoverCapabilities(ctx context.Context) (map[string]video.DiscoveredModelCapabilities, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	config := a.config.Capabilities
	if !config.Enabled {
		return nil, errors.New("generic video capability discovery is not configured")
	}
	body, err := a.do(ctx, "capabilities", operationConfig{
		Enabled: true, Method: config.Method, Path: config.Path,
	}, nil, nil)
	if err != nil {
		return nil, err
	}
	payload, err := a.responsePayload(body)
	if err != nil {
		return nil, err
	}
	items := gjson.GetBytes(payload, config.Root)
	if !items.IsArray() {
		return nil, errors.New("generic capability response root is not an array")
	}
	platform := strings.TrimSpace(gjson.GetBytes(payload, config.PlatformPath).String())
	result := make(map[string]video.DiscoveredModelCapabilities)
	for _, item := range items.Array() {
		if platform != "" {
			itemPlatform := strings.TrimSpace(discoveryField(item, config, "platform").String())
			if itemPlatform != "" && itemPlatform != platform {
				continue
			}
		}
		model := strings.TrimSpace(discoveryField(item, config, "model").String())
		if model == "" {
			continue
		}
		capability := video.DiscoveredModelCapabilities{
			Resolutions:      discoveryStrings(discoveryField(item, config, "resolutions")),
			Ratios:           discoveryStrings(discoveryField(item, config, "ratios")),
			DurationOptions:  discoveryInts(discoveryField(item, config, "duration_options")),
			DurationMin:      int(discoveryField(item, config, "duration_min").Int()),
			DurationMax:      int(discoveryField(item, config, "duration_max").Int()),
			MaxImages:        int(discoveryField(item, config, "max_images").Int()),
			MaxVideos:        int(discoveryField(item, config, "max_videos").Int()),
			MaxAudios:        int(discoveryField(item, config, "max_audios").Int()),
			MaxMedia:         int(discoveryField(item, config, "max_media").Int()),
			MediaDurationMin: discoveryField(item, config, "media_duration_min").Float(),
			MediaDurationMax: discoveryField(item, config, "media_duration_max").Float(),
			MaxVideoDuration: discoveryField(item, config, "max_video_duration_total").Float(),
			MaxAudioDuration: discoveryField(item, config, "max_audio_duration_total").Float(),
		}
		capability.AllowGeneratedAudio = discoveryBool(discoveryField(item, config, "allow_generated_audio"))
		capability.SupportsSmartDuration = discoveryBool(discoveryField(item, config, "supports_smart_duration"))
		capability.RequireVisualMediaWithAudio = discoveryBool(discoveryField(item, config, "require_visual_media_with_audio"))
		capability.SupportsCancel = discoveryBool(discoveryField(item, config, "supports_cancel"))
		upstreamModes := discoveryField(item, config, "supported_modes")
		if upstreamModes.IsArray() && len(upstreamModes.Array()) > 0 {
			for _, upstreamMode := range discoveryStrings(upstreamModes) {
				capability.TaskModes = appendUniqueStrings(capability.TaskModes, config.TaskModeMap[upstreamMode]...)
			}
		} else {
			// Some upstream models omit supported_modes. In that case the
			// channel's configured defaults remain the fallback.
			capability.TaskModes = append([]string(nil), config.DefaultTaskModes...)
		}
		result[model] = capability
	}
	return result, nil
}

func discoveryField(item gjson.Result, config capabilityDiscoveryConfig, name string) gjson.Result {
	path := strings.TrimSpace(config.Fields[name])
	if path == "" {
		return gjson.Result{}
	}
	return item.Get(path)
}

func discoveryStrings(result gjson.Result) []string {
	if !result.IsArray() {
		return nil
	}
	values := make([]string, 0, len(result.Array()))
	for _, item := range result.Array() {
		value := strings.TrimSpace(item.String())
		if value != "" {
			values = appendUniqueStrings(values, value)
		}
	}
	return values
}

func discoveryBool(result gjson.Result) *bool {
	if !result.Exists() || (result.Type != gjson.True && result.Type != gjson.False) {
		return nil
	}
	value := result.Bool()
	return &value
}

func discoveryInts(result gjson.Result) []int {
	if !result.IsArray() {
		return nil
	}
	values := make([]int, 0, len(result.Array()))
	seen := make(map[int]struct{}, len(result.Array()))
	for _, item := range result.Array() {
		value := int(item.Int())
		if value <= 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

func appendUniqueStrings(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
		if _, exists := seen[value]; exists || value == "" {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

var _ video.CapabilityDiscoverer = (*Adapter)(nil)
