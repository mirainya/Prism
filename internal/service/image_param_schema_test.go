package service

import (
	"encoding/json"
	"testing"

	"gorm.io/datatypes"
)

func TestNormalizeImageEndpointParamSchema(t *testing.T) {
	raw := datatypes.JSON(`{"prompt":{"name":"custom prompt","type":"string"},"size":{"name":"old","type":"enum","options":["1792x1024"]},"vendor_field":{"name":"vendor","type":"string"}}`)
	var schema map[string]map[string]any
	if err := json.Unmarshal(normalizeImageEndpointParamSchema(raw), &schema); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"prompt", "image_urls", "aspect_ratio", "size", "quality", "n", "response_format", "output_format"} {
		if _, ok := schema[field]; !ok {
			t.Fatalf("missing canonical image field %q", field)
		}
	}
	if schema["vendor_field"]["name"] != "vendor" {
		t.Fatal("provider-specific field was not preserved")
	}
	options, ok := schema["size"]["options"].([]any)
	if !ok || len(options) != 4 || options[0] != "1024x1024" || options[3] != "auto" {
		t.Fatalf("unexpected size options: %#v", schema["size"]["options"])
	}
}
