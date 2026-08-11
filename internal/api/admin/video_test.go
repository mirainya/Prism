package admin

import (
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
