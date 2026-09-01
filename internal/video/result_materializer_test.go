package video

import (
	"context"
	"errors"
	"testing"

	"gorm.io/datatypes"
)

func TestMaterializeGenerationResultStoresConfiguredProviderVideo(t *testing.T) {
	previous := transferGenerationResultURL
	transferGenerationResultURL = func(_ context.Context, sourceURL, capability string) (string, error) {
		if sourceURL != "https://provider.example/temporary.mp4" || capability != "video" {
			t.Fatalf("source=%q capability=%q", sourceURL, capability)
		}
		return "https://cdn.example/stored.mp4", nil
	}
	t.Cleanup(func() { transferGenerationResultURL = previous })

	channel := &VideoChannel{ExtraConfig: datatypes.JSON(`{"result_storage":{"enabled":true}}`)}
	input := &GenerationResult{VideoURL: "https://provider.example/temporary.mp4", Duration: 1}
	result, err := MaterializeGenerationResult(context.Background(), channel, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.VideoURL != "https://cdn.example/stored.mp4" || result.Duration != 1 {
		t.Fatalf("result=%#v", result)
	}
	if input.VideoURL == result.VideoURL {
		t.Fatal("input result was mutated")
	}
}

func TestMaterializeGenerationResultSkipsUnconfiguredChannel(t *testing.T) {
	previous := transferGenerationResultURL
	transferGenerationResultURL = func(context.Context, string, string) (string, error) {
		return "", errors.New("must not be called")
	}
	t.Cleanup(func() { transferGenerationResultURL = previous })

	input := &GenerationResult{VideoURL: "https://provider.example/video.mp4"}
	result, err := MaterializeGenerationResult(context.Background(), &VideoChannel{}, input)
	if err != nil || result.VideoURL != input.VideoURL {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
