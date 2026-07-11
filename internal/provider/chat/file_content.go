package chat

import (
	"mime"
	"path/filepath"
	"strings"
)

func parseChatFileData(file map[string]any) (mediaType, data string, ok bool) {
	fileData, _ := file["file_data"].(string)
	if fileData == "" {
		return "", "", false
	}
	if mediaType, data, ok := parseDataURL(fileData); ok {
		return mediaType, data, true
	}

	filename, _ := file["filename"].(string)
	mediaType = mime.TypeByExtension(filepath.Ext(filename))
	if separator := strings.IndexByte(mediaType, ';'); separator >= 0 {
		mediaType = mediaType[:separator]
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return mediaType, fileData, true
}
