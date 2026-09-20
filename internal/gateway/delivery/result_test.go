package delivery

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestBindImageResultUsesOrderedDeliveryReferences(t *testing.T) {
	raw, err := json.Marshal(ImageResult{SchemaVersion: 1, Kind: ImageResultKind, Created: 42, Images: []ImageOutput{{RevisedPrompt: "one"}, {RevisedPrompt: "two"}}})
	if err != nil {
		t.Fatal(err)
	}
	sources := []RemoteResult{
		{Role: "image", URL: "https://images.example/one.png"},
		{Role: "image", InlineData: []byte("png"), ContentType: "image/png"},
	}
	bound, err := BindResult(ResourceCapabilityTask, raw, sources, []uint64{11, 12})
	if err != nil {
		t.Fatal(err)
	}
	var result ImageResult
	if err := json.Unmarshal(bound, &result); err != nil {
		t.Fatal(err)
	}
	if result.Created != 42 || len(result.Images) != 2 || result.Images[0].DeliveryID != 11 || result.Images[1].DeliveryID != 12 {
		t.Fatalf("bound result = %#v", result)
	}
	if string(bound) == string(raw) || containsResultSource(bound) {
		t.Fatalf("unsafe bound result = %s", bound)
	}
}

func TestValidateImageResultRejectsSourceOrShapeMismatch(t *testing.T) {
	valid, _ := json.Marshal(ImageResult{SchemaVersion: 1, Kind: ImageResultKind, Images: []ImageOutput{{}}})
	tests := []struct {
		name    string
		raw     []byte
		sources []RemoteResult
	}{
		{"missing source", valid, nil},
		{"wrong role", valid, []RemoteResult{{Role: "video", URL: "https://images.example/a.png"}}},
		{"mixed URL and inline", valid, []RemoteResult{{Role: "image", URL: "https://images.example/a.png", InlineData: []byte("x"), ContentType: "image/png"}}},
		{"non image inline", valid, []RemoteResult{{Role: "image", InlineData: []byte("x"), ContentType: "text/plain"}}},
		{"prebound result", mustImageResult(t, 9), []RemoteResult{{Role: "image", URL: "https://images.example/a.png"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if ValidateResult(ResourceCapabilityTask, test.raw, test.sources) == nil {
				t.Fatal("invalid result accepted")
			}
		})
	}
}

func mustImageResult(t *testing.T, deliveryID uint64) []byte {
	t.Helper()
	value, err := json.Marshal(ImageResult{SchemaVersion: 1, Kind: ImageResultKind, Images: []ImageOutput{{DeliveryID: deliveryID}}})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func containsResultSource(value []byte) bool {
	for _, fragment := range [][]byte{[]byte("https://"), []byte("inline_data"), []byte("b64_json")} {
		if bytes.Contains(value, fragment) {
			return true
		}
	}
	return false
}

func TestRetryableManagedCopyFailure(t *testing.T) {
	for _, reason := range []string{
		ManagedCopyUploadFailed,
		ManagedCopyVerificationFailed,
	} {
		if !RetryableManagedCopyFailure(reason) {
			t.Errorf("%q should be retryable", reason)
		}
	}
	for _, reason := range []string{
		ManagedCopySizeExceeded,
		ManagedCopyEmpty,
		ManagedCopyContentTypeInvalid,
		"",
	} {
		if RetryableManagedCopyFailure(reason) {
			t.Errorf("%q should be permanent", reason)
		}
	}
}
