package payloadview

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mirainya/Prism/internal/gateway/repository"
)

const maxFailureMessageRunes = 500

var (
	failureControlWhitespace = regexp.MustCompile(`[\r\n\t]+`)
	failureURL               = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]+`)
	failureIPv4              = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}(?::[0-9]{1,5})?\b`)
	failureCredentialField   = regexp.MustCompile(`(?i)(\b(?:authorization|api[_-]?key|access[_-]?token|access[_-]?key(?:[_-]?id)?|secret[_-]?access[_-]?key|client[_-]?secret|private[_-]?key|secret[_-]?key|key|token|secret|password|credential)\b\s*[:=]\s*)(?:"[^"]*"|'[^']*'|(?:bearer|basic)\s+[^\s,;}\]]+|[^\s,;}\]]+)`)
	failureAuthorization     = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[A-Za-z0-9._~+/=-]+`)
	failureKnownSecret       = regexp.MustCompile(`(?i)\b(?:sk|xfs)[-_][A-Za-z0-9._~-]{8,}\b`)
	failureOpaqueValue       = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9._~+/=-]{31,}`)
)

type RequestFailure struct {
	Code       string
	Message    string
	HTTPStatus int64
}

type requestFailureDiagnostic struct {
	SchemaVersion      uint32 `json:"schema_version"`
	ProviderCode       string `json:"provider_code,omitempty"`
	ProviderMessage    string `json:"provider_message,omitempty"`
	ProviderHTTPStatus uint16 `json:"provider_http_status,omitempty"`
}

func EncodeFailureDiagnostic(providerCode, providerMessage string, providerHTTPStatus *uint16) ([]byte, error) {
	providerCode = strings.TrimSpace(providerCode)
	if !utf8.ValidString(providerCode) {
		providerCode = ""
	}
	codeRunes := []rune(providerCode)
	if len(codeRunes) > 128 {
		providerCode = string(codeRunes[:128])
	}
	status := uint16(0)
	if providerHTTPStatus != nil && *providerHTTPStatus >= 100 && *providerHTTPStatus <= 599 {
		status = *providerHTTPStatus
	}
	diagnostic := requestFailureDiagnostic{
		SchemaVersion:      1,
		ProviderCode:       providerCode,
		ProviderMessage:    SanitizeFailureMessage(providerMessage),
		ProviderHTTPStatus: status,
	}
	if diagnostic.ProviderCode == "" && diagnostic.ProviderMessage == "" && diagnostic.ProviderHTTPStatus == 0 {
		return nil, nil
	}
	return json.Marshal(diagnostic)
}

// ReadLatestRequestFailure returns a display-safe summary of the most recent
// upstream exchange. It never exposes the full provider response.
func ReadLatestRequestFailure(ctx context.Context, store *repository.Store, callID uint64) (RequestFailure, error) {
	if store == nil || callID == 0 {
		return RequestFailure{}, repository.ErrInvalidInput
	}
	var requestLogID uint64
	var code string
	var status, responseBlobID, diagnosticBlobID sql.NullInt64
	err := store.DB().QueryRowContext(ctx, `SELECT l.id,l.error_code,l.http_status,l.response_payload_blob_id,l.diagnostic_blob_id
FROM gw_channel_request_logs l
JOIN gw_api_call_attempts a ON a.id=l.attempt_id
WHERE a.call_id=? ORDER BY l.id DESC LIMIT 1`, callID).Scan(&requestLogID, &code, &status, &responseBlobID, &diagnosticBlobID)
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
	hasDiagnostic := diagnosticBlobID.Valid && diagnosticBlobID.Int64 > 0
	if hasDiagnostic {
		plain, readErr := ReadRequestLogDiagnostic(ctx, store, requestLogID, uint64(diagnosticBlobID.Int64))
		if readErr == nil {
			var diagnostic requestFailureDiagnostic
			if json.Unmarshal(plain, &diagnostic) == nil && diagnostic.SchemaVersion == 1 {
				failure.Message = SanitizeFailureMessage(diagnostic.ProviderMessage)
				if diagnostic.ProviderHTTPStatus >= 100 && diagnostic.ProviderHTTPStatus <= 599 {
					failure.HTTPStatus = int64(diagnostic.ProviderHTTPStatus)
				}
			}
			clear(plain)
		}
	}
	// A diagnostic is the only public-safe summary for new records. If it is
	// unreadable, fail closed instead of extracting text from the raw response.
	if failure.Message == "" && !hasDiagnostic && responseBlobID.Valid && responseBlobID.Int64 > 0 {
		plain, readErr := ReadRequestLogPayload(ctx, store, requestLogID, uint64(responseBlobID.Int64), "response")
		if readErr == nil {
			failure.Message = ExtractFailureMessage(plain)
			clear(plain)
		}
	}
	if failure.Message == "" {
		switch {
		case failure.HTTPStatus >= 400:
			failure.Message = fmt.Sprintf("上游请求失败（HTTP %d）", failure.HTTPStatus)
		case failure.Code != "":
			failure.Message = "上游任务失败"
		}
	}
	return failure, nil
}

func ExtractFailureMessage(body []byte) string {
	var value any
	if json.Unmarshal(body, &value) == nil {
		for _, path := range [][]string{{"message"}, {"msg"}, {"detail"}, {"error", "message"}, {"error"}, {"data", "message"}} {
			if message := nestedFailureString(value, path); message != "" {
				return SanitizeFailureMessage(message)
			}
		}
		if message, ok := value.(string); ok {
			return SanitizeFailureMessage(message)
		}
		return ""
	}
	return SanitizeFailureMessage(string(body))
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
	if typed, ok := current.(string); ok {
		return strings.TrimSpace(typed)
	}
	return ""
}

// SanitizeFailureMessage removes credentials, locations and opaque identifiers
// before a provider-supplied error crosses the public API boundary.
func SanitizeFailureMessage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) {
		return ""
	}
	value = failureControlWhitespace.ReplaceAllString(value, " ")
	value = failureURL.ReplaceAllString(value, "[URL]")
	value = failureIPv4.ReplaceAllString(value, "[HOST]")
	value = failureCredentialField.ReplaceAllString(value, `${1}[REDACTED]`)
	value = failureAuthorization.ReplaceAllString(value, "[REDACTED]")
	value = failureKnownSecret.ReplaceAllString(value, "[REDACTED]")
	value = failureOpaqueValue.ReplaceAllString(value, "[REDACTED]")
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > maxFailureMessageRunes {
		value = string(runes[:maxFailureMessageRunes])
	}
	return value
}
