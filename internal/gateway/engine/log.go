package engine

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/model"
)

// RequestLog is one concrete upstream attempt. Its request fields are taken
// from PreparedRequest, never reconstructed from the downstream request.
type RequestLog struct {
	record  *model.ChannelRequestLog
	started time.Time
	mu      sync.Mutex
	events  []canonical.Event
}

func StartRequestLog(route *routing.RouteResult, prepared transport.PreparedRequest, operation transport.Operation) (*RequestLog, error) {
	if route == nil {
		return nil, errors.New("route is required")
	}
	requestAt := time.Now()
	path, requestURL := logURL(prepared.URL)
	headers, _ := json.Marshal(redactedHeaders(prepared.Headers))
	record := &model.ChannelRequestLog{
		ChannelID:         route.ChannelID,
		AccountID:         route.KeyID,
		CapabilityCode:    route.ModelName,
		RequestType:       requestType(operation),
		IsStream:          prepared.Stream,
		ModelCode:         route.ModelName,
		VendorModel:       route.VendorModel,
		UpstreamTransport: route.Transport,
		RequestPath:       path,
		Method:            prepared.Method,
		URL:               requestURL,
		RequestHeaders:    string(headers),
		RequestBody:       string(redactedJSON(prepared.Body)),
		RequestAt:         requestAt,
	}
	if err := model.DB().Create(record).Error; err != nil {
		return nil, err
	}
	return &RequestLog{record: record, started: requestAt}, nil
}

func (l *RequestLog) Record() *model.ChannelRequestLog {
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
	response := responseFromEvents(events)
	return l.complete(body, response, statusCode, requestErr)
}

func (l *RequestLog) complete(body []byte, response *canonical.Response, statusCode int, requestErr error) error {
	if l == nil || l.record == nil || l.record.ID == 0 {
		return nil
	}
	if statusCode == 0 {
		statusCode = http.StatusOK
		if requestErr != nil {
			statusCode = errorStatus(requestErr)
		}
	}
	updates := map[string]any{
		"duration_ms":   time.Since(l.started).Milliseconds(),
		"status_code":   statusCode,
		"response_body": string(redactedJSON(body)),
	}
	if requestErr != nil {
		updates["error_message"] = requestErr.Error()
	}
	if response != nil {
		updates["finish_reason"] = response.FinishReason
		updates["response_preview"] = responsePreview(response)
		if response.Usage != nil {
			updates["usage_prompt_tokens"] = response.Usage.InputTokens
			updates["usage_completion_tokens"] = response.Usage.OutputTokens
			updates["usage_total_tokens"] = response.Usage.TotalTokens
		}
	}
	return model.DB().Model(&model.ChannelRequestLog{}).Where("id = ?", l.record.ID).Updates(updates).Error
}

func requestType(operation transport.Operation) model.RequestType {
	if operation == transport.OperationResponses {
		return model.RequestTypeResponses
	}
	return model.RequestTypeChat
}

func logURL(raw string) (string, string) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", raw
	}
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
	name = strings.ToLower(strings.ReplaceAll(name, "-", "_"))
	switch name {
	case "authorization", "proxy_authorization", "api_key", "x_api_key", "x_goog_api_key", "key", "access_token", "refresh_token", "cookie", "set_cookie":
		return true
	}
	return strings.Contains(name, "secret") || strings.Contains(name, "password") || strings.HasSuffix(name, "_token")
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
