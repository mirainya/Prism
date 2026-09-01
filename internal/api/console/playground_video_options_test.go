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
	settings.ServiceTiers = map[string]playgroundVideoServiceTierSettings{
		"standard": {Label: "标准队列"},
		"vip":      {Label: "积分 VIP", RequestParams: map[string]any{"priority": float64(5)}},
	}
	settings.LocalCancel.Enabled = &disabled
	settings.Cancel.Enabled = true
	settings.Cancel.AllowedStatuses = []string{"submitted"}
	rule := playgroundVideoModelValidation{
		Resolutions:                   []string{"720p"},
		Ratios:                        []string{"16:9"},
		DurationMin:                   4,
		DurationMax:                   30,
		DurationMaxWithVideoReference: 18,
		TaskModes:                     []string{"references", "video_edit"},
		RequireVisualMediaWithAudio:   true,
		AllowedRoles:                  []string{"first_frame", "last_frame", "reference_image"},
		MaxImages:                     30,
		Parameters: []playgroundVideoParameter{{
			Name: "priority", Label: "Queue", Type: "select", TaskModes: []string{"references"},
			ConflictsWith: []string{"points_vip"},
			Options: []playgroundVideoParameterOption{{
				Label: "Normal", Value: float64(5), AddsResolutions: []string{"1080p"},
			}},
		}},
	}
	channel := video.VideoChannel{
		AdapterType:  "generic",
		CancelMode:   video.CancelModeProvider,
		Capabilities: datatypes.JSON(`{"first_frame":true,"last_frame":true}`),
	}

	options := playgroundVideoOptionsForChannel(channel, "video-model", rule, settings)
	if !slices.Equal(options.Resolutions, []string{"720p"}) || !slices.Equal(options.Ratios, []string{"16:9"}) {
		t.Fatalf("format options = %#v", options)
	}
	if !slices.Equal(options.TaskTypes, []string{"first_frame", "first_last_frame", "multimodal", "video_edit"}) {
		t.Fatalf("task types = %#v", options.TaskTypes)
	}
	if options.DurationMin != 4 || options.DurationMax != 30 || options.DurationMaxWithVideoReference != 18 ||
		options.MaxImages != 30 || !options.RequireVisualMediaWithAudio {
		t.Fatalf("validation options = %#v", options)
	}
	if !options.AllowLocalCancel || !slices.Equal(options.CancelStatuses, []string{"submitted"}) {
		t.Fatalf("cancellation options = %#v", options)
	}
	if len(options.Parameters) != 0 {
		t.Fatalf("parameters = %#v", options.Parameters)
	}
	if !slices.Equal(options.ServiceTiers, []string{"standard", "vip"}) || len(options.ServiceTierOptions) != 2 ||
		options.ServiceTierOptions[1].Label != "积分 VIP" ||
		!slices.Equal(options.ServiceTierOptions[1].AddsResolutions, []string{"1080p"}) {
		t.Fatalf("service tiers = %#v", options.ServiceTierOptions)
	}
}

func TestRestrictPlaygroundVideoOptionsUsesStaticAndDiscoveredIntersection(t *testing.T) {
	allowAudio := true
	remoteAudio := false
	remoteCancel := false
	options := playgroundVideoModelOptions{
		Resolutions: []string{"720p", "1080p"}, Ratios: []string{"16:9", "9:16"},
		DurationMin: 4, DurationMax: 20, DurationMaxWithVideoReference: 18,
		MaxImages: 30, AllowGeneratedAudio: &allowAudio,
		CancelStatuses: []string{"submitted"},
	}
	discovered := video.DiscoveredModelCapabilities{
		Resolutions: []string{"480p", "720p"}, Ratios: []string{"16:9"},
		DurationOptions: []int{5, 6, 7}, DurationMin: 5, DurationMax: 30,
		DurationMaxWithVideoReference: 15, MaxImages: 10,
		AllowGeneratedAudio: &remoteAudio, SupportsCancel: &remoteCancel,
	}

	restricted := restrictPlaygroundVideoOptions(options, discovered)
	if !slices.Equal(restricted.Resolutions, []string{"720p"}) || !slices.Equal(restricted.Ratios, []string{"16:9"}) {
		t.Fatalf("formats=%#v", restricted)
	}
	if restricted.DurationMin != 5 || restricted.DurationMax != 20 ||
		restricted.DurationMaxWithVideoReference != 15 || restricted.MaxImages != 10 ||
		!slices.Equal(restricted.DurationOptions, []int{5, 6, 7}) ||
		restricted.AllowGeneratedAudio == nil || *restricted.AllowGeneratedAudio || len(restricted.CancelStatuses) != 0 {
		t.Fatalf("restricted=%#v", restricted)
	}
}

func TestRestrictPlaygroundVideoOptionsKeepsConfiguredServiceTiers(t *testing.T) {
	options := playgroundVideoModelOptions{ServiceTiers: []string{"standard", "priority", "vip"}}
	discovered := video.DiscoveredModelCapabilities{ServiceTiers: []string{"standard"}}
	restricted := restrictPlaygroundVideoOptions(options, discovered)
	if !slices.Equal(restricted.ServiceTiers, options.ServiceTiers) {
		t.Fatalf("service tiers=%#v, want %#v", restricted.ServiceTiers, options.ServiceTiers)
	}
}

func TestRestrictPlaygroundVideoOptionsKeepsStaticDurationBounds(t *testing.T) {
	options := playgroundVideoModelOptions{DurationMin: 4, DurationMax: 15}
	discovered := video.DiscoveredModelCapabilities{DurationOptions: []int{2, 4, 8, 15, 20}}
	restricted := restrictPlaygroundVideoOptions(options, discovered)
	if !slices.Equal(restricted.DurationOptions, []int{4, 8, 15}) {
		t.Fatalf("duration options = %#v", restricted.DurationOptions)
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
