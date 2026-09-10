package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
)

// RequestLog is one concrete upstream attempt. Its request fields are taken
// from PreparedRequest, never reconstructed from the downstream request.
type RequestLog struct {
	record  *RequestLogRecord
	started time.Time
	mu      sync.Mutex
	events  []canonical.Event
	unified *unifiedRequestLog
}

type RequestLogRecord struct {
	ID uint
}

type RequestLogLink struct {
	CallID    string
	AttemptID uint
	unified   *unifiedLifecycle
}

func StartRequestLog(route *routing.RouteResult, prepared transport.PreparedRequest, operation transport.Operation, links ...RequestLogLink) (*RequestLog, error) {
	if !unifiedRoute(route) {
		return nil, fmt.Errorf("%w: request logs require a unified route", repository.ErrInvalidInput)
	}
	requestAt := time.Now()
	link := RequestLogLink{}
	if len(links) > 0 {
		link = links[0]
	}
	if link.unified == nil || link.unified.attemptID == 0 {
		return nil, errors.New("unified request log requires its fixed attempt")
	}
	unified, err := beginUnifiedRequestLog(link.unified, prepared, operation)
	if err != nil {
		return nil, err
	}
	if unified == nil || unified.id == 0 {
		return nil, errors.New("unified request log was not created")
	}
	return &RequestLog{record: &RequestLogRecord{ID: uint(unified.id)}, started: requestAt, unified: unified}, nil
}

func (l *RequestLog) Record() *RequestLogRecord {
	if l == nil {
		return nil
	}
	return l.record
}

func (l *RequestLog) Observe(event canonical.Event) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.events = append(l.events, event)
	l.mu.Unlock()
}

func (l *RequestLog) CompleteResponse(response *canonical.Response, statusCode int, requestErr error) error {
	var body []byte
	if response != nil {
		body, _ = json.Marshal(response)
	}
	return l.complete(body, response, statusCode, requestErr)
}

func (l *RequestLog) CompleteStream(statusCode int, requestErr error) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	events := append([]canonical.Event(nil), l.events...)
	l.mu.Unlock()
	body, _ := json.Marshal(events)
	// 日志仅提取终态、usage 和预览；完整 canonical 事件另由可选 Payload 保留。
	response := responseFromEvents(events)
	if len(events) == 0 {
		response = nil
	}
	return l.complete(body, response, statusCode, requestErr)
}

func (l *RequestLog) StreamPayload() []byte {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	events := append([]canonical.Event(nil), l.events...)
	l.mu.Unlock()
	body, _ := json.Marshal(events)
	return body
}

func (l *RequestLog) complete(body []byte, response *canonical.Response, statusCode int, requestErr error) error {
	if l == nil || l.record == nil || l.record.ID == 0 {
		return nil
	}
	_ = body
	return l.unified.finish(response != nil, statusCode, requestErr, time.Since(l.started))
}

func logURL(raw string) (string, string) {
	// URL 用于运维展示，用户信息和敏感查询参数在入库前移除。
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", raw
	}
	parsed.User = nil
	query := parsed.Query()
	for key := range query {
		if sensitiveName(key) {
			query.Set(key, "[REDACTED]")
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.EscapedPath(), parsed.String()
}

func redactedHeaders(headers http.Header) map[string]string {
	result := make(map[string]string, len(headers))
	for key, values := range headers {
		if sensitiveName(key) {
			result[key] = "[REDACTED]"
			continue
		}
		result[key] = strings.Join(values, ", ")
	}
	return result
}

func redactedJSON(raw []byte) []byte {
	if len(raw) == 0 || !json.Valid(raw) {
		return append([]byte(nil), raw...)
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return append([]byte(nil), raw...)
	}
	redactValue(value)
	result, err := json.Marshal(value)
	if err != nil {
		return append([]byte(nil), raw...)
	}
	return result
}

func redactValue(value any) {
	switch current := value.(type) {
	case []any:
		for _, item := range current {
			redactValue(item)
		}
	case map[string]any:
		for key, child := range current {
			if sensitiveName(key) {
				current[key] = "[REDACTED]"
				continue
			}
			if text, ok := child.(string); ok && strings.HasPrefix(text, "data:") && len(text) > 1024 {
				current[key] = "[OMITTED]"
				continue
			}
			redactValue(child)
		}
	}
}

func sensitiveName(name string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(strings.TrimSpace(name)))
	compact := strings.ReplaceAll(normalized, "_", "")
	switch normalized {
	case "authorization", "proxy_authorization", "api_key", "x_api_key", "x_goog_api_key", "key", "access_token", "refresh_token", "cookie", "set_cookie":
		return true
	}
	switch compact {
	case "apikey", "xapikey", "xgoogapikey", "clientkey", "privatekey", "secretkey", "accesskey", "awsaccesskeyid", "accesstoken", "refreshtoken", "idtoken", "sessiontoken":
		return true
	}
	return strings.Contains(normalized, "secret") || strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "signature") || strings.Contains(normalized, "credential") ||
		strings.HasSuffix(normalized, "_token") || strings.HasSuffix(normalized, "_key") ||
		strings.Contains(compact, "accesskey") || strings.HasSuffix(compact, "token")
}

func responseFromEvents(events []canonical.Event) *canonical.Response {
	response := &canonical.Response{}
	found := false
	for _, event := range events {
		if event.Response != nil {
			response = event.Response
			found = true
		}
		if event.Usage != nil {
			response.Usage = event.Usage
			found = true
		}
		switch event.Type {
		case canonical.EventCompleted:
			response.Status = "completed"
			found = true
		case canonical.EventFailed, canonical.EventError:
			response.Status = "failed"
			found = true
		case canonical.EventIncomplete:
			response.Status = "incomplete"
			found = true
		}
	}
	if !found {
		return nil
	}
	return response
}

func responsePreview(response *canonical.Response) string {
	if response == nil {
		return ""
	}
	for _, item := range response.Output {
		for _, content := range item.Content {
			if content.Text != "" {
				if len(content.Text) > 1000 {
					return content.Text[:1000]
				}
				return content.Text
			}
		}
	}
	return ""
}

func errorStatus(err error) int {
	var status interface{ HTTPStatus() int }
	if errors.As(err, &status) && status.HTTPStatus() > 0 {
		return status.HTTPStatus()
	}
	return http.StatusBadGateway
}
