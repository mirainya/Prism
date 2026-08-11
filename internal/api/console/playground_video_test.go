package console

import (
	"reflect"
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/video"
)

func TestVideoTaskTypesForChannel(t *testing.T) {
	tests := []struct {
		name         string
		channel      video.VideoChannel
		rule         playgroundVideoModelValidation
		wantTaskType []string
	}{
		{
			name:    "official channel supports frame modes",
			channel: video.VideoChannel{Capabilities: []byte(`{"first_frame":true,"last_frame":true}`)},
			wantTaskType: []string{
				"text", "first_frame", "first_last_frame", "multimodal",
			},
		},
		{
			name:    "h channel supports text and references",
			channel: video.VideoChannel{Capabilities: []byte(`{"audio":true}`)},
			wantTaskType: []string{
				"text", "multimodal",
			},
		},
		{
			name:    "seedance 2.5 requires multimodal references",
			channel: video.VideoChannel{Capabilities: []byte(`{"audio":false}`)},
			rule: playgroundVideoModelValidation{
				TaskModes:    []string{"references"},
				RequireMedia: true,
				AllowedRoles: []string{"reference_image", "reference_video", "reference_audio"},
			},
			wantTaskType: []string{"multimodal"},
		},
		{
			name:    "h seedance 2.5 supports video extension",
			channel: video.VideoChannel{Capabilities: []byte(`{"audio":true}`)},
			rule: playgroundVideoModelValidation{
				TaskModes:    []string{"text", "references", "video_extension"},
				AllowedRoles: []string{"reference_image", "reference_video", "reference_audio"},
			},
			wantTaskType: []string{"text", "multimodal", "video_extension"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := videoTaskTypesForChannel(tt.channel, tt.rule)
			if !reflect.DeepEqual(got, tt.wantTaskType) {
				t.Fatalf("task types = %#v, want %#v", got, tt.wantTaskType)
			}
		})
	}
}

func TestBuildPlaygroundVideoRequestUsesExplicitChannel(t *testing.T) {
	req, err := buildPlaygroundVideoRequest(map[string]any{
		"model":      "seedance-2.0",
		"prompt":     "test",
		"channel_id": float64(2),
	}, &model.Token{BaseModel: model.BaseModel{ID: 7}, UserID: 9})
	if err != nil {
		t.Fatal(err)
	}
	if req.ChannelID != 2 {
		t.Fatalf("channel id = %d, want 2", req.ChannelID)
	}
	if _, exists := req.Params["channel_id"]; exists {
		t.Fatal("channel_id must not be forwarded as an upstream parameter")
	}
}

func TestBuildPlaygroundVideoRequestRejectsInvalidChannel(t *testing.T) {
	_, err := buildPlaygroundVideoRequest(map[string]any{
		"model":      "seedance-2.0",
		"prompt":     "test",
		"channel_id": 1.5,
	}, &model.Token{BaseModel: model.BaseModel{ID: 7}, UserID: 9})
	if err == nil {
		t.Fatal("expected invalid channel_id error")
	}
}

func TestPlaygroundVideoOptionsUseEmptyCancelStatusesWhenCancelIsDisabled(t *testing.T) {
	options := playgroundVideoOptionsForChannel(
		video.VideoChannel{AdapterType: "generic"},
		"video-model",
		playgroundVideoModelValidation{},
		playgroundVideoAdapterSettings{},
	)
	if options.CancelStatuses == nil {
		t.Fatal("cancel statuses must encode as an empty array, not null")
	}
}
