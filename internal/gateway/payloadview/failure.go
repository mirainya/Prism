package payloadview

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mirainya/Prism/internal/gateway/repository"
)

const maxFailureMessageRunes = 500

type RequestFailure struct {
	Code       string
	Message    string
	HTTPStatus int64
}

// ReadLatestRequestFailure returns a display-safe summary of the most recent
// upstream exchange. It never exposes the full provider response.
func ReadLatestRequestFailure(ctx context.Context, store *repository.Store, callID uint64) (RequestFailure, error) {
	if store == nil || callID == 0 {
		return RequestFailure{}, repository.ErrInvalidInput
	}
	var requestLogID uint64
	var code string
	var status, responseBlobID sql.NullInt64
	err := store.DB().QueryRowContext(ctx, `SELECT l.id,l.error_code,l.http_status,l.response_payload_blob_id
FROM gw_channel_request_logs l
JOIN gw_api_call_attempts a ON a.id=l.attempt_id
WHERE a.call_id=? ORDER BY l.id DESC LIMIT 1`, callID).Scan(&requestLogID, &code, &status, &responseBlobID)
	if err == sql.ErrNoRows {
		return RequestFailure{}, nil
	}
	if err != nil {
		return RequestFailure{}, err
	}
	failure := RequestFailure{Code: strings.TrimSpace(code)}
	if status.Valid {
		failure.HTTPStatus = status.Int64
	}
	if responseBlobID.Valid && responseBlobID.Int64 > 0 {
		plain, readErr := ReadRequestLogPayload(ctx, store, requestLogID, uint64(responseBlobID.Int64), "response")
		if readErr == nil {
			failure.Message = ExtractFailureMessage(plain)
			clear(plain)
		}
	}
	if failure.Message == "" {
		switch {
		case failure.HTTPStatus > 0:
			failure.Message = fmt.Sprintf("上游请求失败（HTTP %d）", failure.HTTPStatus)
		case failure.Code != "":
			failure.Message = failure.Code
		}
	}
	return failure, nil
}

func ExtractFailureMessage(body []byte) string {
	var value any
	if json.Unmarshal(body, &value) == nil {
		for _, path := range [][]string{{"message"}, {"msg"}, {"detail"}, {"error", "message"}, {"error"}, {"data", "message"}} {
			if message := nestedFailureString(value, path); message != "" {
				return truncateFailureMessage(message)
			}
		}
	}
	return truncateFailureMessage(string(body))
}

func nestedFailureString(value any, path []string) string {
	current := value
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current, ok = object[key]
		if !ok {
			return ""
		}
	}
	switch typed := current.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any, []any:
		encoded, err := json.Marshal(typed)
		if err == nil {
			return string(encoded)
		}
	}
	return ""
}

func truncateFailureMessage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) {
		return ""
	}
	runes := []rune(value)
	if len(runes) > maxFailureMessageRunes {
		value = string(runes[:maxFailureMessageRunes])
	}
	return value
}
