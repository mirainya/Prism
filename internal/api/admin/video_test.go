package admin

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/video"
	"gorm.io/datatypes"
)

func TestValidateVideoChannelRequestSeedanceRejectsUnusedConfig(t *testing.T) {
	req := validSeedanceChannelRequest()
	req.ExtraConfig = datatypes.JSON(`{"adapter":{"submit":{"path":"/unused"}}}`)

	err := validateVideoChannelRequest(req)
	if err == nil || !strings.Contains(err.Error(), "does not accept extra_config") {
		t.Fatalf("validate error = %v", err)
	}
}

func TestValidateVideoChannelRequestSeedanceAcceptsEmptyConfig(t *testing.T) {
	req := validSeedanceChannelRequest()
	if err := validateVideoChannelRequest(req); err != nil {
		t.Fatalf("validate channel: %v", err)
	}
	var mappings []video.VideoModelMapping
	if err := json.Unmarshal(req.Models, &mappings); err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 1 || mappings[0].ModelName != "seedance-2.0" || mappings[0].VendorModel != "seedance-2.0" {
		t.Fatalf("normalized mappings = %#v", mappings)
	}
}

func TestBuildDiscoveredVideoModelOptionsIncludesUnconfiguredModels(t *testing.T) {
	discovered := map[string]video.DiscoveredModelCapabilities{
		"seedance-2.5": {},
		"seedance-2.0": {},
	}
	got := buildDiscoveredVideoModelOptions([]byte(`[{"model_name":"video-standard","vendor_model":"seedance-2.0"}]`), discovered)
	want := []discoveredVideoModelOption{
		{VendorModel: "seedance-2.0", PublicModels: []string{"video-standard"}},
		{VendorModel: "seedance-2.5", PublicModels: nil},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("discovered options = %#v, want %#v", got, want)
	}
}

func validSeedanceChannelRequest() *createVideoChannelRequest {
	return &createVideoChannelRequest{
		Name: "Seedance official", AdapterType: video.AdapterTypeSeedance,
		BaseURL: "https://ark.example.com", Status: "active",
		Models:       datatypes.JSON(`["seedance-2.0"]`),
		Pricing:      datatypes.JSON(`{"mode":"fixed","fixed_price":1,"markup_ratio":1}`),
		Capabilities: datatypes.JSON(`{}`), AssetResolver: video.AssetResolverDirectURL,
		ExtraConfig: datatypes.JSON(`{}`),
	}
}
