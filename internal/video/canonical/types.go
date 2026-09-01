// Package canonical contains the protocol-neutral video request semantics.
package canonical

// TaskKind describes the product-level video generation mode.
type TaskKind string

const (
	TaskKindTextToVideo    TaskKind = "text_to_video"
	TaskKindFirstFrame     TaskKind = "first_frame_to_video"
	TaskKindFirstLastFrame TaskKind = "first_last_frame_to_video"
	TaskKindMultimodal     TaskKind = "multimodal_video"
	TaskKindVideoEdit      TaskKind = "video_edit"
	TaskKindVideoExtension TaskKind = "video_extension"
)

// Reference is a public, provider-neutral media reference.
// Exactly one of AssetID and URL is expected at the API boundary.
type Reference struct {
	ID              string   `json:"id,omitempty"`
	Type            string   `json:"type"`
	Role            string   `json:"role"`
	AssetID         string   `json:"asset_id,omitempty"`
	URL             string   `json:"url,omitempty"`
	DurationSeconds *float64 `json:"duration_seconds,omitempty"`
}

type SeedanceOptions struct {
	CameraFixed     *bool `json:"camera_fixed,omitempty"`
	ReturnLastFrame *bool `json:"return_last_frame,omitempty"`
	WebSearch       *bool `json:"web_search,omitempty"`
}

type ProviderOptions struct {
	Seedance *SeedanceOptions `json:"seedance,omitempty"`
}

// VideoSpec is the semantic request accepted by Prism V1.
// Pointer fields preserve whether an optional value was supplied by the caller.
type VideoSpec struct {
	Model           string          `json:"model"`
	Prompt          string          `json:"prompt,omitempty"`
	Duration        *int            `json:"duration,omitempty"`
	Resolution      string          `json:"resolution,omitempty"`
	AspectRatio     string          `json:"aspect_ratio,omitempty"`
	GenerateAudio   *bool           `json:"generate_audio,omitempty"`
	ServiceTier     string          `json:"service_tier,omitempty"`
	References      []Reference     `json:"references,omitempty"`
	ProviderOptions ProviderOptions `json:"provider_options,omitempty"`
	CallbackURL     string          `json:"callback_url,omitempty"`
}

// TaskKind infers the product task from reference roles.
func (s *VideoSpec) InferredTaskKind() TaskKind {
	if s == nil || len(s.References) == 0 {
		return TaskKindTextToVideo
	}
	hasFirst, hasLast, hasEditSource, hasExtensionSource := false, false, false, false
	for _, reference := range s.References {
		switch reference.Role {
		case "first_frame":
			hasFirst = true
		case "last_frame":
			hasLast = true
		case "edit_source":
			hasEditSource = true
		case "source_video":
			hasExtensionSource = true
		}
	}
	if hasEditSource {
		return TaskKindVideoEdit
	}
	if hasExtensionSource {
		return TaskKindVideoExtension
	}
	if hasFirst && hasLast && len(s.References) == 2 {
		return TaskKindFirstLastFrame
	}
	if hasFirst && len(s.References) == 1 {
		return TaskKindFirstFrame
	}
	return TaskKindMultimodal
}
