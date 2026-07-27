package filestorage

import "testing"

func TestNormalizePublicURL(t *testing.T) {
	tests := map[string]string{
		"http://storage.example/image.png":  "https://storage.example/image.png",
		"https://storage.example/image.png": "https://storage.example/image.png",
		"storage.example/image.png":         "https://storage.example/image.png",
		"":                                  "",
	}
	for input, expected := range tests {
		if actual := normalizePublicURL(input); actual != expected {
			t.Fatalf("normalizePublicURL(%q) = %q, want %q", input, actual, expected)
		}
	}
}
