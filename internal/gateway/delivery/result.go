package delivery

import (
	"encoding/json"
	"strings"
)

const (
	ResourceCapabilityTask = "capability_task"
	ResourceVideoTask      = "video_task"
	ImageResultKind        = "image"
	ResultSchemaVersion    = 1
	maxImageResults        = 16
	maxRevisedPromptBytes  = 16 << 10
)

type ImageOutput struct {
	DeliveryID    uint64 `json:"delivery_id,string,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// ImageResult is the persisted canonical result for an image capability task.
// Provider URLs and inline bytes are deliberately absent; each output points
// to a separately protected ResultDelivery.
type ImageResult struct {
	SchemaVersion uint32        `json:"schema_version"`
	Kind          string        `json:"kind"`
	Created       int64         `json:"created,omitempty"`
	Images        []ImageOutput `json:"images"`
}

func ValidateSourcesForResource(resourceKind string, sources []RemoteResult) error {
	switch resourceKind {
	case ResourceVideoTask:
		return ValidateVideoSources(sources)
	case ResourceCapabilityTask:
		if len(sources) == 0 || len(sources) > maxImageResults {
			return ErrInvalidResult
		}
		for _, source := range sources {
			if source.Role != "image" || !ValidSource(source) {
				return ErrInvalidResult
			}
		}
		return nil
	default:
		return ErrInvalidResult
	}
}

func ValidateResult(resourceKind string, plaintext []byte, sources []RemoteResult) error {
	if len(plaintext) == 0 || ValidateSourcesForResource(resourceKind, sources) != nil {
		return ErrInvalidResult
	}
	switch resourceKind {
	case ResourceVideoTask:
		var result VideoResult
		if json.Unmarshal(plaintext, &result) != nil || result.SchemaVersion != ResultSchemaVersion || result.VideoDeliveryID != 0 || result.ThumbnailDeliveryID != 0 {
			return ErrInvalidResult
		}
		return nil
	case ResourceCapabilityTask:
		var result ImageResult
		if json.Unmarshal(plaintext, &result) != nil || result.SchemaVersion != ResultSchemaVersion || result.Kind != ImageResultKind || result.Created < 0 || len(result.Images) != len(sources) {
			return ErrInvalidResult
		}
		for _, image := range result.Images {
			if image.DeliveryID != 0 || len(image.RevisedPrompt) > maxRevisedPromptBytes || strings.ContainsRune(image.RevisedPrompt, '\x00') {
				return ErrInvalidResult
			}
		}
		return nil
	default:
		return ErrInvalidResult
	}
}

// BindResult returns the only result representation that may be persisted on
// a Call. Delivery IDs must follow the exact source order observed upstream.
func BindResult(resourceKind string, plaintext []byte, sources []RemoteResult, deliveryIDs []uint64) ([]byte, error) {
	if len(deliveryIDs) != len(sources) || ValidateResult(resourceKind, plaintext, sources) != nil {
		return nil, ErrInvalidResult
	}
	for _, id := range deliveryIDs {
		if id == 0 {
			return nil, ErrInvalidResult
		}
	}
	switch resourceKind {
	case ResourceVideoTask:
		var result VideoResult
		if err := json.Unmarshal(plaintext, &result); err != nil {
			return nil, ErrInvalidResult
		}
		for index, source := range sources {
			switch source.Role {
			case "video":
				result.VideoDeliveryID = deliveryIDs[index]
			case "thumbnail":
				result.ThumbnailDeliveryID = deliveryIDs[index]
			default:
				return nil, ErrInvalidResult
			}
		}
		return json.Marshal(result)
	case ResourceCapabilityTask:
		var result ImageResult
		if err := json.Unmarshal(plaintext, &result); err != nil {
			return nil, ErrInvalidResult
		}
		for index := range result.Images {
			result.Images[index].DeliveryID = deliveryIDs[index]
		}
		return json.Marshal(result)
	default:
		return nil, ErrInvalidResult
	}
}
