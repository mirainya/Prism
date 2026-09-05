package catalog

import (
	"errors"
	"testing"
)

func TestNormalizeAPIName(t *testing.T) {
	got, err := NormalizeAPIName("  Image.Generate/V2 ")
	if err != nil || got != "image.generate/v2" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := NormalizeAPIName("模型"); !errors.Is(err, ErrInvalidAPIName) {
		t.Fatalf("expected invalid API name, got %v", err)
	}
}

func TestNormalizeRouteTemplate(t *testing.T) {
	got, err := NormalizeRouteTemplate(" /V1//Images/{image_id} ")
	if err != nil || got != "/v1/images/{image_id}" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, input := range []string{"/v1/{ImageID}", "/v1/foo{bar}", "/v1/{id}/?q=1", "v1/images"} {
		if _, err := NormalizeRouteTemplate(input); !errors.Is(err, ErrInvalidRoute) {
			t.Errorf("%q: expected invalid route, got %v", input, err)
		}
	}
}

func TestNormalizeHTTPMethod(t *testing.T) {
	got, err := NormalizeHTTPMethod(" post ")
	if err != nil || got != "POST" {
		t.Fatalf("got %q, %v", got, err)
	}
}
