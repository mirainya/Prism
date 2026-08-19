package video

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// VideoModelMapping separates the model exposed by Prism from the identifier
// sent to one upstream channel.
type VideoModelMapping struct {
	ModelName   string `json:"model_name"`
	VendorModel string `json:"vendor_model"`
}

// ParseVideoModelMappings accepts both the current object format and the
// historical string array where the public and upstream names were identical.
func ParseVideoModelMappings(raw []byte) ([]VideoModelMapping, error) {
	var items []json.RawMessage
	if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &items) != nil || len(items) == 0 {
		return nil, errors.New("models must be a non-empty JSON array")
	}
	result := make([]VideoModelMapping, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for index, item := range items {
		var mapping VideoModelMapping
		var legacy string
		if json.Unmarshal(item, &legacy) == nil {
			mapping.ModelName = legacy
			mapping.VendorModel = legacy
		} else if err := json.Unmarshal(item, &mapping); err != nil {
			return nil, fmt.Errorf("models[%d] must be a string or model mapping", index)
		}
		mapping.ModelName = strings.TrimSpace(mapping.ModelName)
		mapping.VendorModel = strings.TrimSpace(mapping.VendorModel)
		if mapping.ModelName == "" {
			return nil, fmt.Errorf("models[%d].model_name is required", index)
		}
		if mapping.VendorModel == "" {
			mapping.VendorModel = mapping.ModelName
		}
		if _, exists := seen[mapping.ModelName]; exists {
			return nil, fmt.Errorf("duplicate public video model %q", mapping.ModelName)
		}
		seen[mapping.ModelName] = struct{}{}
		result = append(result, mapping)
	}
	return result, nil
}

func ResolveVideoVendorModel(raw []byte, modelName string) (string, bool) {
	mappings, err := ParseVideoModelMappings(raw)
	if err != nil {
		return "", false
	}
	for _, mapping := range mappings {
		if mapping.ModelName == modelName {
			return mapping.VendorModel, true
		}
	}
	return "", false
}

func PublicVideoModels(raw []byte) []string {
	mappings, err := ParseVideoModelMappings(raw)
	if err != nil {
		return nil
	}
	models := make([]string, len(mappings))
	for index, mapping := range mappings {
		models[index] = mapping.ModelName
	}
	return models
}
