package service

import (
	"bytes"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

var (
	bearerCredentialPattern = regexp.MustCompile(`(?i)\bBearer\s+[^\s"']+`)
	secretAssignmentPattern = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|refresh[_-]?token|password|secret|signature|credential)(\s*[:=]\s*["']?)[^&\s,"'}]+`)
	signedURLPattern        = regexp.MustCompile(`https?://[^\s"'<>]+`)
)

func SanitizeAPICallErrorMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	if json.Valid([]byte(message)) {
		message = string(sanitizeAPICallPayload([]byte(message)))
	}
	message = bearerCredentialPattern.ReplaceAllString(message, "Bearer [REDACTED]")
	message = secretAssignmentPattern.ReplaceAllString(message, "${1}${2}[REDACTED]")
	parts := strings.Fields(message)
	for index, part := range parts {
		urlIndex := strings.Index(part, "http://")
		if urlIndex < 0 {
			urlIndex = strings.Index(part, "https://")
		}
		if urlIndex < 0 {
			continue
		}
		suffixStart := len(part)
		for suffixStart > urlIndex && strings.ContainsRune(",;)]}\"'", rune(part[suffixStart-1])) {
			suffixStart--
		}
		parts[index] = part[:urlIndex] + redactSignedURL(part[urlIndex:suffixStart]) + part[suffixStart:]
	}
	message = strings.Join(parts, " ")
	runes := []rune(message)
	if len(runes) > 4096 {
		message = string(runes[:4096])
	}
	return message
}

func sanitizeAPICallPayload(data []byte) []byte {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return sanitizeTextAPICallPayload(data)
	}
	var value any
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return append([]byte(nil), data...)
	}
	redactAPICallPayloadValue(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		return append([]byte(nil), data...)
	}
	return encoded
}

func redactAPICallPayloadValue(value any) {
	switch current := value.(type) {
	case []any:
		for _, item := range current {
			redactAPICallPayloadValue(item)
		}
	case map[string]any:
		for key, child := range current {
			if sensitivePayloadName(key) {
				current[key] = "[REDACTED]"
				continue
			}
			if text, ok := child.(string); ok {
				if strings.HasPrefix(text, "data:") && len(text) > 1024 {
					current[key] = "[OMITTED]"
					continue
				}
				current[key] = string(sanitizeTextAPICallPayload([]byte(text)))
				continue
			}
			redactAPICallPayloadValue(child)
		}
	}
}

func sanitizeTextAPICallPayload(data []byte) []byte {
	text := bearerCredentialPattern.ReplaceAllString(string(data), "Bearer [REDACTED]")
	text = secretAssignmentPattern.ReplaceAllString(text, "${1}${2}[REDACTED]")
	text = signedURLPattern.ReplaceAllStringFunc(text, func(candidate string) string {
		suffixStart := len(candidate)
		for suffixStart > 0 && strings.ContainsRune(",;)]}", rune(candidate[suffixStart-1])) {
			suffixStart--
		}
		return redactSignedURL(candidate[:suffixStart]) + candidate[suffixStart:]
	})
	return []byte(text)
}

func redactSignedURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" {
		return value
	}
	changed := parsed.User != nil
	parsed.User = nil
	query := parsed.Query()
	for key := range query {
		if sensitivePayloadName(key) {
			query.Set(key, "[REDACTED]")
			changed = true
		}
	}
	if !changed {
		return value
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func sensitivePayloadName(name string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(strings.TrimSpace(name)))
	compact := strings.ReplaceAll(normalized, "_", "")
	switch normalized {
	case "authorization", "proxy_authorization", "api_key", "x_api_key", "key", "sig", "token", "access_token", "refresh_token", "cookie", "set_cookie":
		return true
	}
	switch compact {
	case "authorization", "proxyauthorization", "apikey", "xapikey", "clientkey", "privatekey", "secretkey", "signingkey", "encryptionkey", "decryptionkey", "token", "accesstoken", "refreshtoken", "idtoken", "sessiontoken", "cookie", "setcookie", "awsaccesskeyid":
		return true
	}
	return strings.Contains(normalized, "secret") || strings.Contains(normalized, "password") ||
		strings.HasSuffix(normalized, "_token") || strings.Contains(normalized, "signature") ||
		strings.Contains(normalized, "credential") || strings.Contains(compact, "accesskey") ||
		strings.HasSuffix(compact, "token")
}
