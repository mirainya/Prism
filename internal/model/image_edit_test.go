package model

import (
	"testing"

	"gorm.io/datatypes"
)

func TestEndpointImageEditDefaultsLegacyConfigToMultipart(t *testing.T) {
	endpoint := &Endpoint{ExtraConfig: datatypes.JSON(`{
		"image_edit":{"enabled":true,"edit_path":"/v1/images/edits","file_field":"image"}
	}`)}

	config := endpoint.ImageEdit()
	if config == nil || config.InputMode != ImageInputModeMultipart ||
		config.EditPath != "/v1/images/edits" || config.FileField != "image" {
		t.Fatalf("image edit config = %#v", config)
	}
}

func TestEndpointImageEditDefaultsMultipartPathAndField(t *testing.T) {
	endpoint := &Endpoint{ExtraConfig: datatypes.JSON(`{
		"image_edit":{"enabled":true,"input_mode":"multipart"}
	}`)}

	config := endpoint.ImageEdit()
	if config == nil || config.EditPath != "/v1/images/edits" || config.FileField != "image" {
		t.Fatalf("image edit config = %#v", config)
	}
}

func TestEndpointImageEditURLModeDefaultsToImageURLs(t *testing.T) {
	endpoint := &Endpoint{ExtraConfig: datatypes.JSON(`{
		"image_edit":{"enabled":true,"input_mode":"url"}
	}`)}

	config := endpoint.ImageEdit()
	if config == nil || config.InputMode != ImageInputModeURL || config.FileField != "image_urls" {
		t.Fatalf("image edit config = %#v", config)
	}
}

func TestEndpointImageEditRejectsUnknownMode(t *testing.T) {
	endpoint := &Endpoint{ExtraConfig: datatypes.JSON(`{
		"image_edit":{"enabled":true,"input_mode":"binary"}
	}`)}

	if config := endpoint.ImageEdit(); config != nil {
		t.Fatalf("image edit config = %#v, want nil", config)
	}
}
