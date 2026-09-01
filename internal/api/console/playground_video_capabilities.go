package console

import (
	"context"
	"math"
	"time"

	"github.com/mirainya/Prism/internal/video"
)

func discoverPlaygroundVideoCapabilities(
	ctx context.Context,
	channels []video.VideoChannel,
	keys map[uint][]*video.VideoChannelKey,
) map[uint]map[string]video.DiscoveredModelCapabilities {
	type discoveryResult struct {
		channelID    uint
		capabilities map[string]video.DiscoveredModelCapabilities
	}
	results := make(chan discoveryResult, len(channels))
	pending := 0
	for index := range channels {
		channel := &channels[index]
		channelKeys := keys[channel.ID]
		if len(channelKeys) == 0 || playgroundVideoEngine == nil {
			continue
		}
		pending++
		go func(channelID uint, channel video.VideoChannel, channelKeys []*video.VideoChannelKey) {
			discoveryContext, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			merged := make(map[string]video.DiscoveredModelCapabilities)
			for _, key := range channelKeys {
				adapter := playgroundVideoEngine.Registry().Get(channel.AdapterType, &channel, key)
				discoverer, ok := adapter.(video.CapabilityDiscoverer)
				if !ok {
					continue
				}
				capabilities, err := discoverer.DiscoverCapabilities(discoveryContext)
				if err != nil {
					continue
				}
				for model, capability := range capabilities {
					merged[model] = mergeDiscoveredModelCapabilities(merged[model], capability)
				}
			}
			results <- discoveryResult{channelID: channelID, capabilities: merged}
		}(channel.ID, *channel, channelKeys)
	}
	discovered := make(map[uint]map[string]video.DiscoveredModelCapabilities, pending)
	for range pending {
		result := <-results
		if len(result.capabilities) > 0 {
			discovered[result.channelID] = result.capabilities
		}
	}
	return discovered
}

func mergeDiscoveredModelCapabilities(current, next video.DiscoveredModelCapabilities) video.DiscoveredModelCapabilities {
	current.Resolutions = appendUnique(current.Resolutions, next.Resolutions...)
	current.Ratios = appendUnique(current.Ratios, next.Ratios...)
	current.TaskModes = appendUnique(current.TaskModes, next.TaskModes...)
	current.DurationOptions = appendUniqueInts(current.DurationOptions, next.DurationOptions...)
	if current.DurationMin == 0 || (next.DurationMin > 0 && next.DurationMin < current.DurationMin) {
		current.DurationMin = next.DurationMin
	}
	if current.DurationMax == 0 || (next.DurationMax > 0 && next.DurationMax > current.DurationMax) {
		current.DurationMax = next.DurationMax
	}
	if current.DurationMaxWithVideoReference == 0 || (next.DurationMaxWithVideoReference > 0 && next.DurationMaxWithVideoReference > current.DurationMaxWithVideoReference) {
		current.DurationMaxWithVideoReference = next.DurationMaxWithVideoReference
	}
	current.ServiceTiers = appendUnique(current.ServiceTiers, next.ServiceTiers...)
	return current
}

func appendUnique(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
		if value != "" {
			if _, ok := seen[value]; !ok {
				values = append(values, value)
				seen[value] = struct{}{}
			}
		}
	}
	return values
}

func appendUniqueInts(values []int, additions ...int) []int {
	seen := make(map[int]struct{}, len(values)+len(additions))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
		if value > 0 {
			if _, ok := seen[value]; !ok {
				values = append(values, value)
				seen[value] = struct{}{}
			}
		}
	}
	return values
}

func playgroundVideoModelAvailable(availableUntil string) bool {
	if availableUntil == "" {
		return true
	}
	until, err := time.Parse(time.RFC3339, availableUntil)
	return err == nil && time.Now().Before(until)
}

func restrictPlaygroundVideoOptions(
	options playgroundVideoModelOptions,
	capability video.DiscoveredModelCapabilities,
) playgroundVideoModelOptions {
	options.Resolutions = intersectOptionalStrings(options.Resolutions, capability.Resolutions)
	options.Ratios = intersectOptionalStrings(options.Ratios, capability.Ratios)
	options.TaskTypes = restrictPlaygroundTaskTypes(options.TaskTypes, capability.TaskModes)
	// Service tiers are channel execution controls, not model format
	// capabilities. Keep the channel's configured choices even when an
	// upstream capability response omits the optional queue fields.
	options.DurationMin = stricterMinimum(options.DurationMin, capability.DurationMin)
	options.DurationMax = stricterMaximum(options.DurationMax, capability.DurationMax)
	options.DurationMaxWithVideoReference = stricterMaximum(options.DurationMaxWithVideoReference, capability.DurationMaxWithVideoReference)
	options.DurationOptions = filterDurationOptions(capability.DurationOptions, options.DurationMin, options.DurationMax)
	if len(options.DurationOptions) == 0 && capability.SupportsSmartDuration != nil && *capability.SupportsSmartDuration &&
		options.DurationMin > 0 && options.DurationMax >= options.DurationMin {
		for duration := options.DurationMin; duration <= options.DurationMax; duration++ {
			options.DurationOptions = append(options.DurationOptions, duration)
		}
	}
	options.MaxImages = stricterMaximum(options.MaxImages, capability.MaxImages)
	options.MaxVideos = stricterMaximum(options.MaxVideos, capability.MaxVideos)
	options.MaxAudios = stricterMaximum(options.MaxAudios, capability.MaxAudios)
	options.MaxMedia = stricterMaximum(options.MaxMedia, capability.MaxMedia)
	options.MediaDurationMin = stricterMinimumFloat(options.MediaDurationMin, capability.MediaDurationMin)
	options.MediaDurationMax = stricterMaximumFloat(options.MediaDurationMax, capability.MediaDurationMax)
	options.MaxVideoDuration = stricterMaximumFloat(options.MaxVideoDuration, capability.MaxVideoDuration)
	options.MaxAudioDuration = stricterMaximumFloat(options.MaxAudioDuration, capability.MaxAudioDuration)
	if capability.AllowGeneratedAudio != nil {
		if options.AllowGeneratedAudio == nil {
			options.AllowGeneratedAudio = cloneBool(capability.AllowGeneratedAudio)
		} else {
			value := *options.AllowGeneratedAudio && *capability.AllowGeneratedAudio
			options.AllowGeneratedAudio = &value
		}
	}
	if capability.RequireVisualMediaWithAudio != nil {
		options.RequireVisualMediaWithAudio = options.RequireVisualMediaWithAudio || *capability.RequireVisualMediaWithAudio
	}
	if capability.SupportsCancel != nil && !*capability.SupportsCancel {
		options.CancelStatuses = []string{}
	}
	return options
}

func filterDurationOptions(values []int, minimum, maximum int) []int {
	if len(values) == 0 {
		return nil
	}
	result := make([]int, 0, len(values))
	seen := make(map[int]struct{}, len(values))
	for _, value := range values {
		if value <= 0 || (minimum > 0 && value < minimum) || (maximum > 0 && value > maximum) {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func restrictPlaygroundTaskTypes(configured, discovered []string) []string {
	if len(configured) == 0 || len(discovered) == 0 {
		return append([]string(nil), configured...)
	}
	allowed := make(map[string]struct{}, len(discovered))
	for _, mode := range discovered {
		allowed[mode] = struct{}{}
	}
	result := make([]string, 0, len(configured))
	for _, taskType := range configured {
		mode := taskType
		if taskType == "first_frame" || taskType == "first_last_frame" || taskType == "multimodal" {
			mode = "references"
		}
		if _, exists := allowed[mode]; exists {
			result = append(result, taskType)
		}
	}
	return result
}

func intersectOptionalStrings(configured, discovered []string) []string {
	if len(configured) == 0 {
		return append([]string(nil), discovered...)
	}
	if len(discovered) == 0 {
		return append([]string(nil), configured...)
	}
	allowed := make(map[string]struct{}, len(discovered))
	for _, value := range discovered {
		allowed[value] = struct{}{}
	}
	result := make([]string, 0, len(configured))
	for _, value := range configured {
		if _, exists := allowed[value]; exists {
			result = append(result, value)
		}
	}
	return result
}

func stricterMinimum(configured, discovered int) int {
	if configured == 0 {
		return discovered
	}
	if discovered == 0 {
		return configured
	}
	return max(configured, discovered)
}

func stricterMaximum(configured, discovered int) int {
	if configured == 0 {
		return discovered
	}
	if discovered == 0 {
		return configured
	}
	return min(configured, discovered)
}

func stricterMinimumFloat(configured, discovered float64) float64 {
	if configured == 0 {
		return discovered
	}
	if discovered == 0 {
		return configured
	}
	return math.Max(configured, discovered)
}

func stricterMaximumFloat(configured, discovered float64) float64 {
	if configured == 0 {
		return discovered
	}
	if discovered == 0 {
		return configured
	}
	return math.Min(configured, discovered)
}
