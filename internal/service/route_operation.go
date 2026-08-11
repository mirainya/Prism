package service

import (
	"encoding/json"
	"strings"

	"github.com/mirainya/Prism/internal/model"
)

// RouteOperation identifies the concrete capability operation used for endpoint selection.
// It is separate from InvokeRequest.Operation, which is the API-call ledger operation.
const (
	RouteOperationImagesGenerate = "images.generate"
	RouteOperationImagesEdit     = "images.edit"
	RouteOperationVideosGenerate = "videos.generate"
)

func endpointDeclaredRouteOperations(endpoint *model.Endpoint) []string {
	if endpoint == nil {
		return nil
	}
	var declared []string
	if len(endpoint.SupportedOperations) > 0 {
		_ = json.Unmarshal(endpoint.SupportedOperations, &declared)
	}
	if len(declared) == 0 {
		declared = append(declared, endpoint.RouteOperation)
	}

	seen := make(map[string]struct{}, len(declared))
	operations := make([]string, 0, len(declared))
	for _, operation := range declared {
		operation = strings.TrimSpace(operation)
		if operation == "" {
			continue
		}
		if _, exists := seen[operation]; exists {
			continue
		}
		seen[operation] = struct{}{}
		operations = append(operations, operation)
	}
	return operations
}

func endpointAvailableRouteOperations(endpoint *model.Endpoint, modelType model.ModelType) []string {
	if declared := endpointDeclaredRouteOperations(endpoint); len(declared) > 0 {
		return declared
	}
	if endpoint == nil {
		return nil
	}
	switch modelType {
	case model.ModelTypeImage:
		if endpointImageEditPath(endpoint) {
			return []string{RouteOperationImagesEdit}
		}
		operations := []string{RouteOperationImagesGenerate}
		if endpoint.ImageEdit() != nil {
			operations = append(operations, RouteOperationImagesEdit)
		}
		return operations
	case model.ModelTypeVideo:
		return []string{RouteOperationVideosGenerate}
	default:
		return nil
	}
}
