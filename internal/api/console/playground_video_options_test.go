package console

import (
	"slices"
	"testing"
)

func TestMergePlaygroundVideoOptionsKeepsAnyRoutableChoice(t *testing.T) {
	allowAudio := false
	current := playgroundVideoModelOptions{
		Resolutions: []string{"720p"}, DurationMin: 4, DurationMax: 15,
		AllowGeneratedAudio: &allowAudio, MaxImages: 9,
	}
	next := playgroundVideoModelOptions{
		Resolutions: []string{"1080p"}, DurationMin: 2, DurationMax: 30,
		MaxImages: 30,
	}

	merged := mergePlaygroundVideoOptions(current, next)
	if !slices.Equal(merged.Resolutions, []string{"720p", "1080p"}) || merged.DurationMin != 2 || merged.DurationMax != 30 {
		t.Fatalf("merged format options = %#v", merged)
	}
	if merged.AllowGeneratedAudio != nil || merged.MaxImages != 30 {
		t.Fatalf("merged capability options = %#v", merged)
	}
}
