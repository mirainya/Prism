package generic

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mirainya/Prism/internal/video"
)

func (a *Adapter) ValidateRequest(_ context.Context, request *video.GenerateRequest) error {
	if err := a.ready(); err != nil {
		return err
	}
	if request == nil {
		return errors.New("generic video request is required")
	}
	if len(a.config.Validation.Models) == 0 {
		return nil
	}
	rule, exists := a.config.Validation.Models[request.Model]
	if !exists {
		return fmt.Errorf("generic adapter does not support model %q", request.Model)
	}
	return validateRequestRule(request, rule)
}

func validateRequestRule(request *video.GenerateRequest, rule validationRule) error {
	if rule.DurationMin > 0 && request.Duration < rule.DurationMin || rule.DurationMax > 0 && request.Duration > rule.DurationMax {
		return fmt.Errorf("duration must be between %d and %d seconds", rule.DurationMin, rule.DurationMax)
	}
	if request.Resolution != "" && len(rule.Resolutions) > 0 && !contains(rule.Resolutions, request.Resolution) {
		return fmt.Errorf("resolution %q is not supported", request.Resolution)
	}
	if request.Ratio != "" && len(rule.Ratios) > 0 && !contains(rule.Ratios, request.Ratio) {
		return fmt.Errorf("ratio %q is not supported", request.Ratio)
	}
	if request.TaskMode != "" && len(rule.TaskModes) > 0 && !contains(rule.TaskModes, request.TaskMode) {
		return fmt.Errorf("task mode %q is not supported", request.TaskMode)
	}
	if rule.AllowGeneratedAudio != nil && !*rule.AllowGeneratedAudio && request.Audio {
		return errors.New("generated audio is not supported")
	}

	counts := map[string]int{}
	totals := map[string]float64{}
	mediaCount := 0
	for _, item := range request.Content {
		kind := contentKind(item.Type)
		if kind == "" {
			continue
		}
		mediaCount++
		counts[kind]++
		if len(rule.AllowedRoles) > 0 && !contains(rule.AllowedRoles, item.Role) {
			return fmt.Errorf("content role %q is not supported", item.Role)
		}
		if kind == "video" || kind == "audio" {
			if rule.MediaDurationMin > 0 && item.DurationSeconds < rule.MediaDurationMin {
				return fmt.Errorf("%s reference duration must be at least %.0f seconds", kind, rule.MediaDurationMin)
			}
			if rule.MediaDurationMax > 0 && item.DurationSeconds > rule.MediaDurationMax {
				return fmt.Errorf("%s reference duration cannot exceed %.0f seconds", kind, rule.MediaDurationMax)
			}
			totals[kind] += item.DurationSeconds
		}
	}
	if rule.RequireMedia && mediaCount == 0 {
		return errors.New("at least one reference media item is required")
	}
	if rule.MaxImages > 0 && counts["image"] > rule.MaxImages ||
		rule.MaxVideos > 0 && counts["video"] > rule.MaxVideos ||
		rule.MaxAudios > 0 && counts["audio"] > rule.MaxAudios ||
		rule.MaxMedia > 0 && mediaCount > rule.MaxMedia {
		return errors.New("reference media limit exceeded")
	}
	if rule.MaxVideoDuration > 0 && totals["video"] > rule.MaxVideoDuration {
		return fmt.Errorf("video reference duration total cannot exceed %.0f seconds", rule.MaxVideoDuration)
	}
	if rule.MaxAudioDuration > 0 && totals["audio"] > rule.MaxAudioDuration {
		return fmt.Errorf("audio reference duration total cannot exceed %.0f seconds", rule.MaxAudioDuration)
	}
	return nil
}

func contentKind(contentType string) string {
	switch strings.TrimSpace(contentType) {
	case "image_url":
		return "image"
	case "video_url":
		return "video"
	case "audio_url":
		return "audio"
	default:
		return ""
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
