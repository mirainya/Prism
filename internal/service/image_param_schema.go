package service

import (
	"encoding/json"

	"gorm.io/datatypes"
)

// imageEndpointParamSchema is the public parameter contract shared by image
// generation and image editing endpoints. Provider-specific field names stay
// in Endpoint.ParamMapping; this schema only describes the canonical request.
var imageEndpointParamSchema = map[string]any{
	"prompt": map[string]any{
		"name": "prompt", "type": "string", "required": true,
	},
	"image_urls": map[string]any{
		"name": "image_urls", "type": "array", "required": false,
		"description": "reference image URLs or data URLs; non-empty values enable image editing",
	},
	"aspect_ratio": map[string]any{
		"name": "aspect_ratio", "type": "enum", "required": false,
		"options": []string{"1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3"},
	},
	"size": map[string]any{
		"name": "size", "type": "enum", "required": false, "default": "1024x1024",
		"options": []string{"1024x1024", "1536x1024", "1024x1536", "auto"},
	},
	"n": map[string]any{
		"name": "n", "type": "number", "required": false, "default": 1,
	},
	"quality": map[string]any{
		"name": "quality", "type": "enum", "required": false, "default": "auto",
		"options": []string{"auto", "high", "medium", "low"},
	},
	"response_format": map[string]any{
		"name": "response_format", "type": "enum", "required": false, "default": "url",
		"options": []string{"url", "b64_json"},
	},
	"output_format": map[string]any{
		"name": "output_format", "type": "enum", "required": false, "default": "png",
		"options": []string{"png", "jpeg", "webp"},
	},
	"output_compression": map[string]any{
		"name": "output_compression", "type": "number", "required": false,
		"description": "output compression, 0-100",
	},
	"moderation": map[string]any{
		"name": "moderation", "type": "string", "required": false,
	},
	"style": map[string]any{
		"name": "style", "type": "string", "required": false,
	},
}

// defaultImageEndpointParamSchema returns a copy encoded as JSON so callers
// cannot mutate the shared definition through a decoded map.
func defaultImageEndpointParamSchema() datatypes.JSON {
	encoded, err := json.Marshal(imageEndpointParamSchema)
	if err != nil {
		return datatypes.JSON(`{}`)
	}
	return datatypes.JSON(encoded)
}

// normalizeImageEndpointParamSchema fills missing public fields while
// preserving provider-specific fields already declared by an endpoint. Size
// is normalized because old rows exposed values unsupported by current image
// routes.
func normalizeImageEndpointParamSchema(raw datatypes.JSON) datatypes.JSON {
	result := make(map[string]any)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &result)
	}
	for key, value := range imageEndpointParamSchema {
		if _, exists := result[key]; !exists {
			result[key] = value
		}
	}
	result["size"] = imageEndpointParamSchema["size"]
	encoded, err := json.Marshal(result)
	if err != nil {
		return defaultImageEndpointParamSchema()
	}
	return datatypes.JSON(encoded)
}
