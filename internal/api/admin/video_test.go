package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/video"
	"gorm.io/datatypes"
	"gorm.io/gorm"
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

func TestVideoChannelHandlersPersistFormalSettings(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&video.VideoChannel{}); err != nil {
		t.Fatal(err)
	}
	var previous *gorm.DB
	if model.HasDB() {
		previous = model.DB()
	}
	model.SetDB(database)
	t.Cleanup(func() { model.SetDB(previous) })

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/channels", CreateVideoChannel)
	router.PUT("/channels/:id", UpdateVideoChannel)
	createBody := `{
		"name":"formal settings","adapter_type":"seedance","base_url":"https://provider.example",
		"status":"active","priority":2,"request_timeout_seconds":45,"models":["seedance-2.0"],
		"supports_first_frame":true,"supports_last_frame":false,"supports_audio":true,"supports_web_search":false,
		"cancel_mode":"provider","pricing_mode":"fixed","fixed_price":1.25,"markup_ratio":1.3,
		"asset_resolver":"direct_url","result_storage_enabled":true,"extra_config":{}
	}`
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/channels", bytes.NewBufferString(createBody)))
	if response.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}

	var channel video.VideoChannel
	if err := database.First(&channel).Error; err != nil {
		t.Fatal(err)
	}
	if channel.RequestTimeoutSeconds != 45 || channel.SupportsFirstFrame == nil || !*channel.SupportsFirstFrame ||
		channel.SupportsAudio == nil || !*channel.SupportsAudio || channel.CancelMode != video.CancelModeProvider ||
		channel.ResultStorageEnabled == nil || !*channel.ResultStorageEnabled ||
		channel.FixedPrice.StringFixed(2) != "1.25" || channel.MarkupRatio.StringFixed(2) != "1.30" ||
		len(channel.Capabilities) != 0 || len(channel.Pricing) != 0 {
		t.Fatalf("created formal settings=%#v", channel)
	}

	updateBody := strings.ReplaceAll(createBody, `"request_timeout_seconds":45`, `"request_timeout_seconds":20`)
	updateBody = strings.ReplaceAll(updateBody, `"supports_first_frame":true`, `"supports_first_frame":false`)
	updateBody = strings.ReplaceAll(updateBody, `"cancel_mode":"provider"`, `"cancel_mode":"disabled"`)
	updateBody = strings.ReplaceAll(updateBody, `"result_storage_enabled":true`, `"result_storage_enabled":false`)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPut, fmt.Sprintf("/channels/%d", channel.ID), bytes.NewBufferString(updateBody)))
	if response.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", response.Code, response.Body.String())
	}
	if err := database.First(&channel, channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if channel.RequestTimeoutSeconds != 20 || channel.SupportsFirstFrame == nil || *channel.SupportsFirstFrame ||
		channel.CancelMode != video.CancelModeDisabled || channel.ResultStorageEnabled == nil || *channel.ResultStorageEnabled {
		t.Fatalf("updated formal settings=%#v", channel)
	}
}

func TestStripPromotedVideoChannelJSONPreservesExtensions(t *testing.T) {
	req := &createVideoChannelRequest{
		Capabilities: datatypes.JSON(`{"first_frame":true,"provider_feature":true}`),
		Pricing:      datatypes.JSON(`{"mode":"fixed","fixed_price":1,"currency":"CNY"}`),
		ExtraConfig: datatypes.JSON(`{
			"adapter":{
				"profile":"json_task_v1","timeout_seconds":45,
				"cancel":{"enabled":true,"path":"/cancel"},
				"local_cancel":{"enabled":true,"disabled_models":["model-a"]},
				"submit":{"path":"/tasks"}
			},
			"result_storage":{"enabled":true,"trusted_hosts":["cdn.example"]}
		}`),
	}
	if err := stripPromotedVideoChannelJSON(req); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		raw  datatypes.JSON
		want map[string]any
	}{
		"capabilities": {raw: req.Capabilities, want: map[string]any{"provider_feature": true}},
		"pricing":      {raw: req.Pricing, want: map[string]any{"currency": "CNY"}},
	} {
		var got map[string]any
		if err := json.Unmarshal(test.raw, &got); err != nil || !reflect.DeepEqual(got, test.want) {
			t.Fatalf("%s=%s, want %#v", name, test.raw, test.want)
		}
	}
	var extra map[string]any
	if err := json.Unmarshal(req.ExtraConfig, &extra); err != nil {
		t.Fatal(err)
	}
	adapter := extra["adapter"].(map[string]any)
	if _, exists := adapter["profile"]; exists {
		t.Fatal("adapter profile was not removed")
	}
	if _, exists := adapter["timeout_seconds"]; exists {
		t.Fatal("adapter timeout was not removed")
	}
	if _, exists := adapter["submit"]; !exists || adapter["cancel"].(map[string]any)["path"] != "/cancel" {
		t.Fatalf("adapter extensions=%#v", adapter)
	}
	resultStorage := extra["result_storage"].(map[string]any)
	if _, exists := resultStorage["enabled"]; exists || len(resultStorage["trusted_hosts"].([]any)) != 1 {
		t.Fatalf("result storage extensions=%#v", resultStorage)
	}
}

func TestNormalizeFormalVideoPricingPreservesExplicitZeroMarkup(t *testing.T) {
	req := validSeedanceChannelRequest()
	req.Pricing = datatypes.JSON(`{"mode":"fixed","fixed_price":1,"markup_ratio":0}`)
	if err := normalizeFormalVideoChannelSettings(req); err != nil {
		t.Fatal(err)
	}
	if req.MarkupRatio == nil || *req.MarkupRatio != 0 {
		t.Fatalf("markup ratio=%v", req.MarkupRatio)
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
