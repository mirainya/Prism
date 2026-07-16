package responses

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
)

type failingReadCloser struct {
	reader *strings.Reader
}

func (reader *failingReadCloser) Read(buffer []byte) (int, error) {
	if reader.reader.Len() > 0 {
		return reader.reader.Read(buffer)
	}
	return 0, errors.New("connection reset")
}

func (*failingReadCloser) Close() error { return nil }

func TestResolveInputFilesChecksOwnership(t *testing.T) {
	db := openResponsesTestDB(t)
	if err := db.AutoMigrate(&model.AIFile{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	file := model.AIFile{ID: "file_test", UserID: 1, TokenID: 10, Filename: "a.txt", Purpose: "user_data", Bytes: 3, MimeType: "text/plain", Content: []byte("abc"), Status: "processed"}
	if err := db.Create(&file).Error; err != nil {
		t.Fatal(err)
	}
	req := &protocol.Request{Input: json.RawMessage(`[{"type":"message","content":[{"type":"input_file","file_id":"file_test"}]}]`)}
	if err := resolveInputFiles(10, req); err != nil {
		t.Fatalf("owned file: %v", err)
	}
	if string(req.Input) == "" || !json.Valid(req.Input) {
		t.Fatalf("invalid input: %s", req.Input)
	}
	bad := &protocol.Request{Input: json.RawMessage(`[{"type":"message","content":[{"type":"input_file","file_id":"file_test"}]}]`)}
	if err := resolveInputFiles(11, bad); err == nil {
		t.Fatal("cross-token file access was accepted")
	}
}

func TestResolveInputFilesUsesModalitySpecificDataURLs(t *testing.T) {
	db := openResponsesTestDB(t)
	if err := db.AutoMigrate(&model.AIFile{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	files := []model.AIFile{
		{ID: "file_audio", UserID: 1, TokenID: 10, Filename: "sound.wav", Purpose: "user_data", Bytes: 3, MimeType: "audio/wav", Content: []byte("abc"), Status: "processed"},
		{ID: "file_video", UserID: 1, TokenID: 10, Filename: "clip.mp4", Purpose: "user_data", Bytes: 3, MimeType: "video/mp4", Content: []byte("xyz"), Status: "processed"},
	}
	if err := db.Create(&files).Error; err != nil {
		t.Fatal(err)
	}
	req := &protocol.Request{Input: json.RawMessage(`[{"role":"user","content":[{"type":"input_audio","file_id":"file_audio"},{"type":"input_video","file_id":"file_video"}]}]`)}
	if err := resolveInputFiles(10, req); err != nil {
		t.Fatal(err)
	}
	text := string(req.Input)
	for _, expected := range []string{`"audio_url":"data:audio/wav;base64,YWJj"`, `"video_url":"data:video/mp4;base64,eHl6"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %s in %s", expected, text)
		}
	}
	if strings.Contains(text, `"file_id"`) || strings.Contains(text, `"file_data"`) {
		t.Fatalf("modality files used the wrong field: %s", text)
	}
}
