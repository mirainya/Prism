package generic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
		if len(request.Params) > 0 {
			return errors.New("video extension parameters are not declared for this channel")
		}
		return nil
	}
	rule, exists := a.config.Validation.Models[request.Model]
	if !exists {
		return fmt.Errorf("generic adapter does not support model %q", request.Model)
	}
	if rule.AvailableUntil != "" {
		until, err := time.Parse(time.RFC3339, rule.AvailableUntil)
		if err != nil || !time.Now().Before(until) {
			return fmt.Errorf("model %q is no longer available", request.Model)
		}
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
	forbidden := forbiddenParameterSet(rule.ForbiddenParameters)
	for name := range forbidden {
		if _, exists := request.Params[name]; exists {
			return fmt.Errorf("parameter %q is not supported", name)
		}
	}
	declared := make(map[string]struct{}, len(rule.Parameters))
	for _, parameter := range rule.Parameters {
		declared[parameter.Name] = struct{}{}
	}
	for name := range request.Params {
		if _, exists := forbidden[name]; exists {
			continue
		}
		if _, exists := declared[name]; !exists {
			return fmt.Errorf("parameter %q is not declared for model %q", name, request.Model)
		}
	}
	for _, parameter := range rule.Parameters {
		value, exists := request.Params[parameter.Name]
		if !exists {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("parameter %q has an invalid value", parameter.Name)
		}
		valid := false
		for _, option := range parameter.Options {
			optionValue, optionErr := json.Marshal(option.Value)
			if optionErr == nil && string(optionValue) == string(encoded) {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("parameter %q is not supported", parameter.Name)
		}
	}

	counts := map[string]int{}
	roleCounts := map[string]int{}
	totals := map[string]float64{}
	mediaCount := 0
	for _, item := range request.Content {
		kind := contentKind(item.Type)
		if kind == "" {
			continue
		}
		mediaCount++
		counts[kind]++
		roleCounts[item.Role]++
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
	if rule.RequireVisualMediaWithAudio && counts["audio"] > 0 && counts["image"] == 0 && counts["video"] == 0 {
		return errors.New("audio references require image or video media")
	}
	if modeRule, exists := rule.TaskModeRules[request.TaskMode]; exists {
		if modeRule.MinMedia > 0 && mediaCount < modeRule.MinMedia {
			return fmt.Errorf("task mode %q requires at least %d reference items", request.TaskMode, modeRule.MinMedia)
		}
		if modeRule.MaxMedia > 0 && mediaCount > modeRule.MaxMedia {
			return fmt.Errorf("task mode %q allows at most %d reference items", request.TaskMode, modeRule.MaxMedia)
		}
		for role := range roleCounts {
			if len(modeRule.AllowedRoles) > 0 && !contains(modeRule.AllowedRoles, role) {
				return fmt.Errorf("content role %q is not supported for task mode %q", role, request.TaskMode)
			}
		}
		for role, expected := range modeRule.ExactRoleCounts {
			if roleCounts[role] != expected {
				return fmt.Errorf("task mode %q requires exactly %d %s items", request.TaskMode, expected, role)
			}
		}
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

func forbiddenParameterSet(names []string) map[string]struct{} {
	result := make(map[string]struct{}, len(names))
	for _, name := range names {
		result[name] = struct{}{}
	}
	return result
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
