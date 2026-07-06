package service

import (
	"testing"

	"gorm.io/datatypes"
)

func TestComputeParam(t *testing.T) {
	cases := []struct {
		name     string
		template string
		params   map[string]any
		want     string
		wantOK   bool
	}{
		{"all fields present", "{width}x{height}", map[string]any{"width": 1024, "height": 768}, "1024x768", true},
		{"single field", "{ratio}", map[string]any{"ratio": "16:9"}, "16:9", true},
		{"missing field", "{width}x{height}", map[string]any{"width": 1024}, "", false},
		{"no placeholder", "fixed", map[string]any{}, "fixed", true},
		{"float rendered", "{scale}", map[string]any{"scale": 2.5}, "2.5", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := computeParam(c.template, c.params)
			if got != c.want || ok != c.wantOK {
				t.Errorf("computeParam(%q, %v) = (%q, %v), want (%q, %v)", c.template, c.params, got, ok, c.want, c.wantOK)
			}
		})
	}
}

func TestMapParamsComputedParams(t *testing.T) {
	mapping := datatypes.JSON(`{
		"field_mapping": {"prompt": "text"},
		"fixed_params": {"model": "sd-xl"},
		"computed_params": {"size": "{width}x{height}"}
	}`)
	params := map[string]any{"prompt": "a cat", "width": 1024, "height": 1024}

	result := mapParams(params, mapping)

	if result["text"] != "a cat" {
		t.Errorf("field_mapping failed: text = %v", result["text"])
	}
	if result["model"] != "sd-xl" {
		t.Errorf("fixed_params failed: model = %v", result["model"])
	}
	if result["size"] != "1024x1024" {
		t.Errorf("computed_params failed: size = %v", result["size"])
	}
}

func TestMapParamsComputedParamsSkippedWhenIncomplete(t *testing.T) {
	mapping := datatypes.JSON(`{"computed_params": {"size": "{width}x{height}"}}`)
	params := map[string]any{"width": 1024} // 缺 height

	result := mapParams(params, mapping)

	if _, exists := result["size"]; exists {
		t.Errorf("computed_params should be skipped when field missing, got size = %v", result["size"])
	}
}

func TestMapParamsBackwardCompatNoComputedParams(t *testing.T) {
	// 无 computed_params 时行为不变
	mapping := datatypes.JSON(`{"field_mapping": {"prompt": "text"}}`)
	params := map[string]any{"prompt": "hi", "n": 2}

	result := mapParams(params, mapping)

	if result["text"] != "hi" || result["n"] != float64(2) && result["n"] != 2 {
		t.Errorf("backward compat broken: %v", result)
	}
}
