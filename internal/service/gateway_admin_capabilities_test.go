package service

import (
	"errors"
	"testing"
)

func TestNormalizeGwCapabilities(t *testing.T) {
	valid := []any{
		map[string]any{"stream": false},
		map[string]any{"web_search": true, "image_generation": false},
		map[string]any{"reasoning": true, "video": true},
		[]any{"tools", "vision"},
		nil,
	}
	for _, value := range valid {
		if _, err := normalizeGwCapabilities(value); err != nil {
			t.Fatalf("normalizeGwCapabilities(%#v): %v", value, err)
		}
	}

	invalid := []any{
		map[string]any{"unknown": true},
		map[string]any{"tools": "yes"},
		map[string]any{"responses": true},
		[]any{"tools", 1},
		"chat",
	}
	for _, value := range invalid {
		if _, err := normalizeGwCapabilities(value); !errors.Is(err, ErrGwInvalidCapabilities) {
			t.Fatalf("normalizeGwCapabilities(%#v) error = %v", value, err)
		}
	}
}
