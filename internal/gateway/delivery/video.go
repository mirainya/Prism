package delivery

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// RemoteResult is an adapter observation, never a persisted public payload.
// Unknown expiry stays unknown; it must not be replaced by a local default TTL.
type RemoteResult struct {
	Role        string
	URL         string
	InlineData  []byte
	ContentType string
	ExpiresAt   *time.Time
}

type VideoResult struct {
	SchemaVersion       uint32 `json:"schema_version"`
	Duration            string `json:"duration,omitempty"`
	VideoDeliveryID     uint64 `json:"video_delivery_id,string,omitempty"`
	ThumbnailDeliveryID uint64 `json:"thumbnail_delivery_id,string,omitempty"`
}

var ErrInvalidResult = errors.New("delivery: invalid video result")

const (
	ManagedCopyUploadFailed       = "managed_copy_upload_failed"
	ManagedCopyVerificationFailed = "managed_copy_verification_failed"
	ManagedCopySizeExceeded       = "managed_copy_size_exceeded"
	ManagedCopyEmpty              = "managed_copy_empty"
	ManagedCopyContentTypeInvalid = "managed_copy_content_type_invalid"
)

func RetryableManagedCopyFailure(reason string) bool {
	switch reason {
	case ManagedCopyUploadFailed, ManagedCopyVerificationFailed:
		return true
	default:
		return false
	}
}

func ValidateVideoSources(sources []RemoteResult) error {
	if len(sources) < 1 || len(sources) > 2 || sources[0].Role != "video" {
		return ErrInvalidResult
	}
	for index, source := range sources {
		if index == 1 && source.Role != "thumbnail" || !ValidSource(source) {
			return ErrInvalidResult
		}
	}
	return nil
}

func ValidSource(source RemoteResult) bool {
	hasURL := source.URL != ""
	hasInline := len(source.InlineData) != 0
	if hasURL == hasInline {
		return false
	}
	if hasURL {
		return source.ContentType == "" && ValidRemoteURL(source.URL)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(source.ContentType, ";")[0]))
	return source.ExpiresAt == nil && len(source.InlineData) <= 64<<20 && strings.HasPrefix(contentType, "image/")
}

func SourceKind(source RemoteResult) string {
	if source.URL != "" && len(source.InlineData) == 0 {
		return "remote_url"
	}
	if source.URL == "" && len(source.InlineData) != 0 {
		return "inline_response"
	}
	return ""
}

func ValidRemoteURL(value string) bool {
	if len(value) == 0 || len(value) > 8192 || strings.TrimSpace(value) != value {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != "" && parsed.User == nil && parsed.Fragment == ""
}
