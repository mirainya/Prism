package prismv1

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/video/canonical"
)

func TestDecodeAndConvertCanonicalRequest(t *testing.T) {
	spec, err := Decode(strings.NewReader(`{
		"model":"video-fast",
		"prompt":"海边日落",
		"duration":5,
		"resolution":"720p",
		"aspect_ratio":"16:9",
		"generate_audio":true,
		"references":[{"type":"image","role":"first_frame","asset_id":"asset_1"}],
		"provider_options":{"seedance":{"web_search":true,"return_last_frame":false}},
		"callback_url":"https://example.com/webhook"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if spec.InferredTaskKind() != canonical.TaskKindFirstFrame || spec.GenerateAudio == nil || !*spec.GenerateAudio {
		t.Fatalf("spec = %#v", spec)
	}
	req, err := ToTaskRequest(spec, 3, 7)
	if err != nil {
		t.Fatal(err)
	}
	if req.UserID != 3 || req.TokenID != 7 || req.Ratio != "16:9" || req.TaskMode != "first_frame" || !req.Audio {
		t.Fatalf("request = %#v", req)
	}
	if len(req.Content) != 1 || req.Content[0].Type != "image_url" || req.Content[0].AssetID != "asset_1" {
		t.Fatalf("content = %#v", req.Content)
	}
	if req.Params["web_search"] != true || req.Params["return_last_frame"] != false {
		t.Fatalf("params = %#v", req.Params)
	}
}

func TestDecodeRejectsLegacyAndUnknownFields(t *testing.T) {
	fields := []string{
		`"content":[]`,
		`"ratio":"16:9"`,
		`"params":{}`,
		`"parameters":{}`,
		`"task_mode":"text"`,
		`"priority":4`,
	}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			_, err := Decode(strings.NewReader(fmt.Sprintf(`{"model":"video-fast","prompt":"test",%s}`, field)))
			if err == nil || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestDecodeRejectsInvalidReferenceSemantics(t *testing.T) {
	requests := []string{
		`{"model":"video-fast","references":[{"type":"image","role":"last_frame","url":"https://example.com/last.png"}]}`,
		`{"model":"video-fast","references":[{"type":"audio","role":"first_frame","url":"https://example.com/a.mp3"}]}`,
		`{"model":"video-fast","references":[{"type":"video","role":"source_video","url":"https://example.com/a.mp4"},{"type":"video","role":"reference_video","url":"https://example.com/b.mp4"}]}`,
		`{"model":"video-fast","references":[{"type":"image","role":"reference_image","asset_id":"asset_1","url":"https://example.com/a.png"}]}`,
	}
	for _, request := range requests {
		if _, err := Decode(strings.NewReader(request)); err == nil {
			t.Fatalf("request should fail: %s", request)
		}
	}
}

func TestDecodePreservesOmittedOptionalFields(t *testing.T) {
	spec, err := Decode(strings.NewReader(`{"model":"video-fast","prompt":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if spec.Duration != nil || spec.GenerateAudio != nil {
		t.Fatalf("optional fields = %#v", spec)
	}
	req, err := ToTaskRequest(spec, 3, 7)
	if err != nil {
		t.Fatal(err)
	}
	if req.Audio || req.Duration != 0 || req.TaskMode != "text" {
		t.Fatalf("request = %#v", req)
	}
}

func TestDecodeRejectsUnknownProviderOptions(t *testing.T) {
	_, err := Decode(strings.NewReader(`{
		"model":"video-fast","prompt":"test",
		"provider_options":{"seedance":{"priority":4}}
	}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("error = %v", err)
	}
}

func TestToTaskRequestPreservesSourceVideoForExtensionMode(t *testing.T) {
	spec, err := Decode(strings.NewReader(`{
		"model":"video-fast",
		"references":[{"type":"video","role":"source_video","asset_id":"asset_video"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	req, err := ToTaskRequest(spec, 3, 7)
	if err != nil {
		t.Fatal(err)
	}
	if req.TaskMode != "video_extension" || req.Content[0].Role != "source_video" {
		t.Fatalf("request = %#v", req)
	}
}

func TestToTaskRequestMapsVideoEdit(t *testing.T) {
	spec, err := Decode(strings.NewReader(`{
		"model":"video-fast",
		"references":[
			{"id":"source","type":"video","role":"edit_source","asset_id":"asset_video"},
			{"id":"style","type":"image","role":"reference_image","asset_id":"asset_image"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	req, err := ToTaskRequest(spec, 3, 7)
	if err != nil {
		t.Fatal(err)
	}
	if req.TaskMode != "video_edit" || req.Content[0].Role != "edit_source" || req.Content[0].ClientRefID != "source" {
		t.Fatalf("request = %#v", req)
	}
}
