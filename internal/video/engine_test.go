package video

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type priceTestAdapter struct {
	cost   float64
	err    error
	called bool
}

func (a *priceTestAdapter) BuildRequest(context.Context, *GenerateRequest) (*ProviderRequest, error) {
	return nil, nil
}

func (a *priceTestAdapter) Submit(context.Context, *ProviderRequest) (*SubmitResult, error) {
	return nil, nil
}

func (a *priceTestAdapter) Poll(context.Context, string) (*Progress, error) { return nil, nil }

func (a *priceTestAdapter) Estimate(context.Context, *GenerateRequest) (float64, error) {
	a.called = true
	return a.cost, a.err
}

type routeValidationAdapter struct {
	resolution string
}

func (a *routeValidationAdapter) BuildRequest(context.Context, *GenerateRequest) (*ProviderRequest, error) {
	return nil, nil
}

func (a *routeValidationAdapter) Submit(context.Context, *ProviderRequest) (*SubmitResult, error) {
	return nil, nil
}

func (a *routeValidationAdapter) Poll(context.Context, string) (*Progress, error) {
	return nil, nil
}

func (a *routeValidationAdapter) ValidateRequest(_ context.Context, request *GenerateRequest) error {
	if request.Resolution != a.resolution {
		return fmt.Errorf("resolution %q is not supported", request.Resolution)
	}
	return nil
}

func TestValidateContentInfersReferencesAndCapabilities(t *testing.T) {
	engine := &Engine{db: newAssetTestService(t).db}
	content, mode, caps, err := engine.validateContent(context.Background(), 7, "", []ContentItem{
		{Type: "image_url", Role: "first_frame", URL: "https://1.1.1.1/frame.png"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(content) != 1 || mode != "references" || !caps.FirstFrame {
		t.Fatalf("content=%#v mode=%q caps=%#v", content, mode, caps)
	}
}

func TestValidateContentRequiresSingleReferenceSource(t *testing.T) {
	engine := &Engine{db: newAssetTestService(t).db}
	_, _, _, err := engine.validateContent(context.Background(), 7, "", []ContentItem{
		{Type: "image_url", Role: "reference_image", AssetID: "asset", URL: "https://1.1.1.1/frame.png"},
	})
	if !errors.Is(err, ErrInvalidTaskRequest) {
		t.Fatalf("error = %v, want ErrInvalidTaskRequest", err)
	}
}

func TestValidateContentRejectsReferenceModeWithoutMedia(t *testing.T) {
	engine := &Engine{db: newAssetTestService(t).db}
	_, _, _, err := engine.validateContent(context.Background(), 7, "references", []ContentItem{
		{Type: "text", Text: "prompt fragment"},
	})
	if !errors.Is(err, ErrInvalidTaskRequest) {
		t.Fatalf("error = %v, want ErrInvalidTaskRequest", err)
	}
}

func TestValidateContentRejectsMismatchedRole(t *testing.T) {
	engine := &Engine{db: newAssetTestService(t).db}
	_, _, _, err := engine.validateContent(context.Background(), 7, "", []ContentItem{
		{Type: "video_url", Role: "first_frame", URL: "https://1.1.1.1/video.mp4", DurationSeconds: 4},
	})
	if !errors.Is(err, ErrInvalidTaskRequest) {
		t.Fatalf("error = %v, want ErrInvalidTaskRequest", err)
	}
}

func TestValidateContentAllowsMediaWithoutDuration(t *testing.T) {
	engine := &Engine{db: newAssetTestService(t).db}
	_, mode, _, err := engine.validateContent(context.Background(), 7, "", []ContentItem{
		{Type: "video_url", Role: "reference_video", URL: "https://1.1.1.1/video.mp4"},
	})
	if err != nil || mode != "references" {
		t.Fatalf("mode=%q error=%v", mode, err)
	}
}

func TestPrepareTaskRequestSkipsIncompatibleHigherPriorityChannel(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&VideoChannel{}, &VideoChannelKey{}); err != nil {
		t.Fatal(err)
	}
	channels := []VideoChannel{
		{Name: "H", AdapterType: "test", BaseURL: "https://h.example", Status: "active", Priority: 20, Models: datatypes.JSON(`["seedance-2.0"]`), Capabilities: datatypes.JSON(`{}`)},
		{Name: "official", AdapterType: "test", BaseURL: "https://official.example", Status: "active", Priority: 10, Models: datatypes.JSON(`["seedance-2.0"]`), Capabilities: datatypes.JSON(`{}`)},
	}
	if err := db.Create(&channels).Error; err != nil {
		t.Fatal(err)
	}
	for _, channel := range channels {
		if err := db.Create(&VideoChannelKey{ChannelID: channel.ID, APIKey: "test-key", Status: "active", Weight: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	registry := NewRegistry()
	registry.Register("test", func(channel *VideoChannel, _ *VideoChannelKey) Adapter {
		resolution := "720p"
		if channel.Name == "H" {
			resolution = "1080p"
		}
		return &routeValidationAdapter{resolution: resolution}
	})
	engine := &Engine{db: db, router: NewRouter(db, nil), registry: registry}

	prepared, err := engine.prepareTaskRequest(context.Background(), &CreateTaskRequest{
		TokenID: 1, Model: "seedance-2.0", Prompt: "test", Resolution: "720p",
	}, "request-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.channel.Name != "official" {
		t.Fatalf("channel = %q, want official", prepared.channel.Name)
	}

	_, err = engine.prepareTaskRequest(context.Background(), &CreateTaskRequest{
		TokenID: 1, ChannelID: channels[0].ID, Model: "seedance-2.0", Prompt: "test", Resolution: "720p",
	}, "request-2", false)
	if !errors.Is(err, ErrInvalidTaskRequest) {
		t.Fatalf("explicit incompatible channel error = %v, want ErrInvalidTaskRequest", err)
	}

	prepared, err = engine.prepareTaskRequest(context.Background(), &CreateTaskRequest{
		TokenID: 1, ChannelID: channels[1].ID, Model: "seedance-2.0", Prompt: "test", Resolution: "720p",
	}, "request-3", false)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.channel.ID != channels[1].ID {
		t.Fatalf("channel id = %d, want %d", prepared.channel.ID, channels[1].ID)
	}
}

func TestEnabledVideoParam(t *testing.T) {
	params := map[string]any{"web_search": true, "return_last_frame": false, "invalid": "true"}
	if !enabledVideoParam(params, "web_search") || enabledVideoParam(params, "return_last_frame") || enabledVideoParam(params, "invalid") {
		t.Fatalf("unexpected enabled video params: %#v", params)
	}
}

func TestValidateContentAllowsSub25ReferenceLimits(t *testing.T) {
	engine := &Engine{db: newAssetTestService(t).db}
	content := make([]ContentItem, 10)
	for index := range content {
		content[index] = ContentItem{Type: "image_url", Role: "reference_image", URL: "https://1.1.1.1/image.png"}
	}
	if _, _, _, err := engine.validateContent(context.Background(), 7, "", content); err != nil {
		t.Fatalf("error = %v", err)
	}

	content = make([]ContentItem, 31)
	for index := range content {
		content[index] = ContentItem{Type: "image_url", Role: "reference_image", URL: "https://1.1.1.1/image.png"}
	}
	_, _, _, err := engine.validateContent(context.Background(), 7, "", content)
	if !errors.Is(err, ErrInvalidTaskRequest) {
		t.Fatalf("error = %v, want ErrInvalidTaskRequest", err)
	}
}

func TestValidateContentAllowsAudioWithoutVisualReference(t *testing.T) {
	engine := &Engine{db: newAssetTestService(t).db}
	_, mode, _, err := engine.validateContent(context.Background(), 7, "", []ContentItem{
		{Type: "audio_url", Role: "reference_audio", URL: "https://1.1.1.1/audio.mp3"},
	})
	if err != nil || mode != "references" {
		t.Fatalf("mode=%q error=%v", mode, err)
	}
}

func TestValidateContentAllowsVideoExtension(t *testing.T) {
	engine := &Engine{db: newAssetTestService(t).db}
	_, mode, _, err := engine.validateContent(context.Background(), 7, "video_extension", []ContentItem{
		{Type: "video_url", Role: "reference_video", URL: "https://1.1.1.1/video.mp4", DurationSeconds: 8},
	})
	if err != nil || mode != "video_extension" {
		t.Fatalf("mode=%q error=%v", mode, err)
	}
}

func TestVideoPriceUsesUpstreamEstimateAndMarkup(t *testing.T) {
	db := newAssetTestService(t).db
	adapter := &priceTestAdapter{cost: 1.25}
	channel := &VideoChannel{
		Pricing:       []byte(`{"mode":"upstream_estimate","fixed_price":99,"markup_ratio":1.2}`),
		AssetResolver: "direct_url",
	}
	result, err := videoPrice(context.Background(), db, channel, &VideoChannelKey{}, adapter, &GenerateRequest{TaskID: "estimate-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !adapter.called || result.BaseCost.String() != "1.25" || result.EstimatedCost.String() != "1.5" || result.MarkupRatio.String() != "1.2" {
		t.Fatalf("result=%#v called=%v", result, adapter.called)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(result.PricingSnapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot["upstream_estimated_cost"] != "1.25" || snapshot["reserved_cost"] != "1.5" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}

func TestVideoPriceDoesNotFallbackWhenUpstreamEstimateFails(t *testing.T) {
	db := newAssetTestService(t).db
	estimateErr := errors.New("estimate unavailable")
	channel := &VideoChannel{
		Pricing:       []byte(`{"mode":"upstream_estimate","fixed_price":9,"markup_ratio":1}`),
		AssetResolver: "direct_url",
	}
	_, err := videoPrice(context.Background(), db, channel, &VideoChannelKey{}, &priceTestAdapter{err: estimateErr}, &GenerateRequest{TaskID: "estimate-2"})
	if !errors.Is(err, estimateErr) {
		t.Fatalf("error=%v, want estimate failure", err)
	}
}
