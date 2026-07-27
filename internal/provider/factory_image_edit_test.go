package provider

import (
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/datatypes"
)

func TestNewProviderConfiguresMultipartImageEditOnly(t *testing.T) {
	tests := []struct {
		name         string
		extraConfig  string
		wantEditPath string
		wantField    string
	}{
		{
			name: "multipart",
			extraConfig: `{"image_edit":{"enabled":true,"input_mode":"multipart",` +
				`"edit_path":"/v1/images/edits","file_field":"image"}}`,
			wantEditPath: "/v1/images/edits",
			wantField:    "image",
		},
		{
			name: "url",
			extraConfig: `{"image_edit":{"enabled":true,"input_mode":"url",` +
				`"file_field":"image_urls"}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider, err := NewProvider(
				&model.Channel{BaseURL: "https://upstream.example"},
				&model.ChannelAccount{},
				&model.Endpoint{ExtraConfig: datatypes.JSON(test.extraConfig)},
			)
			if err != nil {
				t.Fatal(err)
			}
			base, ok := provider.(*BaseProvider)
			if !ok {
				t.Fatalf("provider type = %T", provider)
			}
			if base.ImageEditPath != test.wantEditPath || base.ImageEditField != test.wantField {
				t.Fatalf("image edit provider config = %q %q", base.ImageEditPath, base.ImageEditField)
			}
		})
	}
}
