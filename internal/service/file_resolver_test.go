package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/datatypes"
)

func TestResolveFileParamsURLModeUploadsInternalImages(t *testing.T) {
	pngData := []byte("\x89PNG\r\n\x1a\nreference")
	encoded := "@base64:reference.png:" + base64.StdEncoding.EncodeToString(pngData)
	endpoint := &model.Endpoint{ExtraConfig: datatypes.JSON(`{
		"image_edit":{"enabled":true,"input_mode":"url","file_field":"image_urls"}
	}`)}

	previousUploader := uploadImageEditBytes
	uploadImageEditBytes = func(_ context.Context, data []byte, contentType, capabilityCode string) (string, error) {
		if !bytes.Equal(data, pngData) {
			t.Fatalf("uploaded data = %q", data)
		}
		if contentType != "image/png" || capabilityCode != "gpt-image-2-duomi" {
			t.Fatalf("upload metadata = %q %q", contentType, capabilityCode)
		}
		return "https://storage.example/reference.png", nil
	}
	t.Cleanup(func() { uploadImageEditBytes = previousUploader })

	resolved, err := resolveFileParams(context.Background(), map[string]any{
		"image_urls": []any{encoded, "https://images.example/existing.png"},
	}, endpoint, "gpt-image-2-duomi")
	if err != nil {
		t.Fatal(err)
	}
	images, ok := resolved["image_urls"].([]any)
	if !ok || len(images) != 2 || images[0] != "https://storage.example/reference.png" ||
		images[1] != "https://images.example/existing.png" {
		t.Fatalf("image_urls = %#v", resolved["image_urls"])
	}
}

func TestResolveFileParamsURLModeRewritesCanonicalField(t *testing.T) {
	pngData := []byte("\x89PNG\r\n\x1a\nreference")
	encoded := "@base64:reference.png:" + base64.StdEncoding.EncodeToString(pngData)
	endpoint := &model.Endpoint{ExtraConfig: datatypes.JSON(`{
		"image_edit":{"enabled":true,"input_mode":"url","file_field":"image_urls"}
	}`)}

	previousUploader := uploadImageEditBytes
	uploadImageEditBytes = func(_ context.Context, _ []byte, _, _ string) (string, error) {
		return "https://storage.example/reference.png", nil
	}
	t.Cleanup(func() { uploadImageEditBytes = previousUploader })

	resolved, err := resolveFileParams(context.Background(), map[string]any{
		"image": []any{encoded},
	}, endpoint, "gpt-image-2-duomi")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := resolved["image"]; exists {
		t.Fatalf("legacy image field was not removed: %#v", resolved)
	}
	images, ok := resolved["image_urls"].([]any)
	if !ok || len(images) != 1 || images[0] != "https://storage.example/reference.png" {
		t.Fatalf("image_urls = %#v", resolved["image_urls"])
	}
}

func TestResolveFileParamsMultipartModeKeepsUploadedImage(t *testing.T) {
	encoded := "@base64:reference.png:" + base64.StdEncoding.EncodeToString([]byte("image"))
	endpoint := &model.Endpoint{ExtraConfig: datatypes.JSON(`{
		"image_edit":{"enabled":true,"input_mode":"multipart","edit_path":"/v1/images/edits","file_field":"image"}
	}`)}

	resolved, err := resolveFileParams(context.Background(), map[string]any{
		"image_urls": []any{encoded},
	}, endpoint, "gpt-image")
	if err != nil {
		t.Fatal(err)
	}
	images, ok := resolved["image"].([]any)
	if !ok || len(images) != 1 || images[0] != encoded {
		t.Fatalf("image = %#v", resolved["image"])
	}
	if _, exists := resolved["image_urls"]; exists {
		t.Fatalf("canonical image_urls field was not rewritten: %#v", resolved)
	}
}

func TestResolveFileParamsMultipartModeDownloadsStoredImageAndMask(t *testing.T) {
	endpoint := &model.Endpoint{ExtraConfig: datatypes.JSON(`{
		"image_edit":{"enabled":true,"input_mode":"multipart","file_field":"image"}
	}`)}

	previousDownloader := downloadImageEditURL
	downloadImageEditURL = func(_ context.Context, imageURL string) (string, string, error) {
		switch imageURL {
		case "https://storage.example/reference.png":
			return "reference-data", "reference.png", nil
		case "https://storage.example/mask.png":
			return "mask-data", "mask.png", nil
		default:
			t.Fatalf("unexpected download URL %q", imageURL)
			return "", "", nil
		}
	}
	t.Cleanup(func() { downloadImageEditURL = previousDownloader })

	resolved, err := resolveFileParams(context.Background(), map[string]any{
		"image_urls": []any{"https://storage.example/reference.png"},
		"mask":       []any{"https://storage.example/mask.png"},
	}, endpoint, "gpt-image")
	if err != nil {
		t.Fatal(err)
	}
	images, ok := resolved["image"].([]any)
	if !ok || len(images) != 1 || images[0] != "@base64:reference.png:reference-data" {
		t.Fatalf("image = %#v", resolved["image"])
	}
	masks, ok := resolved["mask"].([]any)
	if !ok || len(masks) != 1 || masks[0] != "@base64:mask.png:mask-data" {
		t.Fatalf("mask = %#v", resolved["mask"])
	}
}
