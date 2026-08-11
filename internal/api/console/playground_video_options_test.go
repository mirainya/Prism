package console

import (
	"slices"
	"testing"

	"github.com/mirainya/Prism/internal/video"
	"gorm.io/datatypes"
)

func TestPlaygroundVideoOptionsForChannelUsesAdapterConfiguration(t *testing.T) {
	disabled := false
	settings := playgroundVideoAdapterSettings{}
	settings.LocalCancel.Enabled = &disabled
	settings.Cancel.Enabled = true
	settings.Cancel.AllowedStatuses = []string{"submitted"}
	rule := playgroundVideoModelValidation{
		Resolutions:                 []string{"720p"},
		Ratios:                      []string{"16:9"},
		DurationMin:                 4,
		DurationMax:                 30,
		TaskModes:                   []string{"references"},
		RequireVisualMediaWithAudio: true,
		AllowedRoles:                []string{"first_frame", "last_frame", "reference_image"},
		MaxImages:                   30,
		Parameters: []playgroundVideoParameter{{
			Name: "priority", Label: "Queue", Type: "select",
			Options: []playgroundVideoParameterOption{{Label: "Normal", Value: float64(5)}},
		}},
	}
	channel := video.VideoChannel{
		AdapterType:  "generic",
		Capabilities: datatypes.JSON(`{"first_frame":true,"last_frame":true}`),
	}

	options := playgroundVideoOptionsForChannel(channel, "video-model", rule, settings)
	if !slices.Equal(options.Resolutions, []string{"720p"}) || !slices.Equal(options.Ratios, []string{"16:9"}) {
		t.Fatalf("format options = %#v", options)
	}
	if !slices.Equal(options.TaskTypes, []string{"first_frame", "first_last_frame", "multimodal"}) {
		t.Fatalf("task types = %#v", options.TaskTypes)
	}
	if options.DurationMin != 4 || options.DurationMax != 30 || options.MaxImages != 30 || !options.RequireVisualMediaWithAudio {
		t.Fatalf("validation options = %#v", options)
	}
	if options.AllowLocalCancel || !slices.Equal(options.CancelStatuses, []string{"submitted"}) {
		t.Fatalf("cancellation options = %#v", options)
	}
	if len(options.Parameters) != 1 || options.Parameters[0].Name != "priority" {
		t.Fatalf("parameters = %#v", options.Parameters)
	}
}

func TestMergePlaygroundVideoOptionsKeepsAnyRoutableChoice(t *testing.T) {
	allowAudio := false
	current := playgroundVideoModelOptions{
		Resolutions: []string{"720p"}, DurationMin: 4, DurationMax: 15,
		AllowGeneratedAudio: &allowAudio, MaxImages: 9,
	}
	next := playgroundVideoModelOptions{
		Resolutions: []string{"1080p"}, DurationMin: 2, DurationMax: 30,
		MaxImages: 30, AllowLocalCancel: true,
	}

	merged := mergePlaygroundVideoOptions(current, next)
	if !slices.Equal(merged.Resolutions, []string{"720p", "1080p"}) || merged.DurationMin != 2 || merged.DurationMax != 30 {
		t.Fatalf("merged format options = %#v", merged)
	}
	if merged.AllowGeneratedAudio != nil || merged.MaxImages != 30 || !merged.AllowLocalCancel {
		t.Fatalf("merged capability options = %#v", merged)
	}
}
