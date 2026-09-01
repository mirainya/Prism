// Package prismv1 implements the strict public Prism V1 video request codec.
package prismv1

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/internal/video/canonical"
)

func Decode(body io.Reader) (*canonical.VideoSpec, error) {
	var spec canonical.VideoSpec
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return nil, fmt.Errorf("invalid video request: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("invalid video request: request body must contain one JSON object")
	}
	if err := validate(&spec); err != nil {
		return nil, err
	}
	return &spec, nil
}

func validate(spec *canonical.VideoSpec) error {
	if spec == nil || strings.TrimSpace(spec.Model) == "" {
		return errors.New("invalid video request: model is required")
	}
	spec.Model = strings.TrimSpace(spec.Model)
	spec.ServiceTier = strings.TrimSpace(spec.ServiceTier)
	if spec.ServiceTier != "" && spec.ServiceTier != "standard" && spec.ServiceTier != "priority" && spec.ServiceTier != "vip" {
		return errors.New("invalid video request: service_tier must be standard, priority, or vip")
	}
	if strings.TrimSpace(spec.Prompt) == "" && len(spec.References) == 0 {
		return errors.New("invalid video request: prompt or references is required")
	}
	if spec.Duration != nil && *spec.Duration <= 0 {
		return errors.New("invalid video request: duration must be positive")
	}
	firstFrames, lastFrames, sourceVideos, editSources, videoReferences := 0, 0, 0, 0, 0
	referenceIDs := make(map[string]struct{}, len(spec.References))
	for index := range spec.References {
		reference := &spec.References[index]
		reference.ID = strings.TrimSpace(reference.ID)
		reference.Type = strings.TrimSpace(reference.Type)
		reference.Role = strings.TrimSpace(reference.Role)
		reference.AssetID = strings.TrimSpace(reference.AssetID)
		reference.URL = strings.TrimSpace(reference.URL)
		if reference.Type != "image" && reference.Type != "video" && reference.Type != "audio" {
			return fmt.Errorf("invalid video request: references[%d].type must be image, video, or audio", index)
		}
		if (reference.AssetID == "") == (reference.URL == "") {
			return fmt.Errorf("invalid video request: references[%d] requires exactly one of asset_id or url", index)
		}
		if reference.DurationSeconds != nil && *reference.DurationSeconds < 0 {
			return fmt.Errorf("invalid video request: references[%d].duration_seconds must not be negative", index)
		}
		switch reference.Role {
		case "first_frame", "last_frame", "reference_image":
			if reference.Type != "image" {
				return fmt.Errorf("invalid video request: references[%d].role %q requires an image", index, reference.Role)
			}
			if reference.Role == "first_frame" {
				firstFrames++
			}
			if reference.Role == "last_frame" {
				lastFrames++
			}
		case "reference_video", "source_video", "edit_source":
			if reference.Type != "video" {
				return fmt.Errorf("invalid video request: references[%d].role %q requires a video", index, reference.Role)
			}
			videoReferences++
			if reference.Role == "source_video" {
				sourceVideos++
			}
			if reference.Role == "edit_source" {
				editSources++
			}
		case "reference_audio":
			if reference.Type != "audio" {
				return fmt.Errorf("invalid video request: references[%d].role %q requires audio", index, reference.Role)
			}
		default:
			return fmt.Errorf("invalid video request: references[%d].role is invalid", index)
		}
		if reference.ID != "" {
			if _, exists := referenceIDs[reference.ID]; exists {
				return fmt.Errorf("invalid video request: references[%d].id is duplicated", index)
			}
			referenceIDs[reference.ID] = struct{}{}
		}
	}
	if firstFrames > 1 || lastFrames > 1 {
		return errors.New("invalid video request: first_frame and last_frame may each appear once")
	}
	if lastFrames > 0 && firstFrames == 0 {
		return errors.New("invalid video request: last_frame requires first_frame")
	}
	if sourceVideos > 0 && (sourceVideos != 1 || editSources > 0 || videoReferences != 1) {
		return errors.New("invalid video request: video extension requires exactly one source_video and no other video reference")
	}
	if editSources > 0 && (editSources != 1 || sourceVideos > 0 || videoReferences != 1) {
		return errors.New("invalid video request: video edit requires exactly one edit_source and no other video reference")
	}
	return nil
}

// ToTaskRequest is a temporary bridge to the existing asynchronous video engine.
// It deliberately exposes only canonical semantics to the engine boundary.
func ToTaskRequest(spec *canonical.VideoSpec, userID, tokenID uint) (*video.CreateTaskRequest, error) {
	if err := validate(spec); err != nil {
		return nil, err
	}
	references := make([]video.ContentItem, 0, len(spec.References))
	for _, reference := range spec.References {
		contentType := reference.Type + "_url"
		item := video.ContentItem{
			ClientRefID: reference.ID, Type: contentType, Role: reference.Role,
			AssetID: reference.AssetID, URL: reference.URL,
		}
		if reference.DurationSeconds != nil {
			item.DurationSeconds = *reference.DurationSeconds
		}
		references = append(references, item)
	}
	params := make(map[string]any)
	if options := spec.ProviderOptions.Seedance; options != nil {
		if options.CameraFixed != nil {
			params["camera_fixed"] = *options.CameraFixed
		}
		if options.ReturnLastFrame != nil {
			params["return_last_frame"] = *options.ReturnLastFrame
		}
		if options.WebSearch != nil {
			params["web_search"] = *options.WebSearch
		}
	}
	var audio bool
	if spec.GenerateAudio != nil {
		audio = *spec.GenerateAudio
	}
	duration := 0
	if spec.Duration != nil {
		duration = *spec.Duration
	}
	taskMode := "text"
	switch spec.InferredTaskKind() {
	case canonical.TaskKindVideoEdit:
		taskMode = "video_edit"
	case canonical.TaskKindVideoExtension:
		taskMode = "video_extension"
	case canonical.TaskKindFirstFrame:
		taskMode = "first_frame"
	case canonical.TaskKindFirstLastFrame:
		taskMode = "first_last_frame"
	case canonical.TaskKindMultimodal:
		taskMode = "multimodal"
	}
	return &video.CreateTaskRequest{
		UserID: userID, TokenID: tokenID, Model: spec.Model, Prompt: spec.Prompt,
		Resolution: spec.Resolution, Ratio: spec.AspectRatio, Duration: duration,
		Audio: audio, TaskMode: taskMode, Content: references, Params: params,
		ServiceTier: spec.ServiceTier,
		Callback:    spec.CallbackURL,
	}, nil
}
