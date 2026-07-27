package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/transport"
)

type HTTPError struct {
	Status  int
	Body    []byte
	Details *canonical.Error
}

func (e *HTTPError) Error() string                  { return fmt.Sprintf("upstream HTTP %d", e.Status) }
func (e *HTTPError) HTTPStatus() int                { return e.Status }
func (e *HTTPError) ErrorDetails() *canonical.Error { return e.Details }

type base struct {
	client *http.Client
	id     transport.ID
	path   string
	plan   func(transport.Operation, canonical.Request, canonical.FeatureSet) transport.Plan
	decode func([]byte, transport.Route) (canonical.Response, error)
	event  func(string, []byte, transport.Route) ([]canonical.Event, error)
}

func (b *base) ID() transport.ID { return b.id }
func (b *base) Plan(op transport.Operation, r canonical.Request, f canonical.FeatureSet) transport.Plan {
	return b.plan(op, r, f)
}
func (b *base) Prepare(_ context.Context, in transport.Invocation) (transport.PreparedRequest, error) {
	if strings.TrimSpace(in.Route.BaseURL) == "" {
		return transport.PreparedRequest{}, fmt.Errorf("OpenAI base URL is required")
	}
	body, err := encode(in, b.id == transport.OpenAIResponses)
	if err != nil {
		return transport.PreparedRequest{}, err
	}
	model := in.Route.VendorModel
	if model == "" {
		model = in.Request.Model
	}
	var obj map[string]any
	if err = json.Unmarshal(body, &obj); err != nil {
		return transport.PreparedRequest{}, err
	}
	obj["model"] = model
	if in.Request.Stream {
		obj["stream"] = true
	}
	body, _ = json.Marshal(obj)
	head := http.Header{"Content-Type": []string{"application/json"}}
	if in.Route.APIKey != "" {
		head.Set("Authorization", "Bearer "+in.Route.APIKey)
	}
	for k, v := range in.Route.ExtraHeaders {
		head.Set(k, v)
	}
	return transport.PreparedRequest{Method: http.MethodPost, URL: strings.TrimRight(in.Route.BaseURL, "/") + b.path, Headers: head, Body: body, Stream: in.Request.Stream}, nil
}
func (b *base) Execute(ctx context.Context, in transport.Invocation) (canonical.Response, transport.PreparedRequest, error) {
	prepared, err := b.Prepare(ctx, in)
	if err != nil {
		return canonical.Response{}, prepared, err
	}
	response, err := b.ExecutePrepared(ctx, in, prepared)
	return response, prepared, err
}
func (b *base) ExecutePrepared(ctx context.Context, in transport.Invocation, prepared transport.PreparedRequest) (canonical.Response, error) {
	p := prepared.Clone()
	resp, err := do(ctx, b.client, p)
	if err != nil {
		return canonical.Response{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return canonical.Response{}, err
	}
	if resp.StatusCode >= 400 {
		return canonical.Response{}, httpError(resp.StatusCode, raw)
	}
	out, err := b.decode(raw, in.Route)
	return out, err
}
func (b *base) Stream(ctx context.Context, in transport.Invocation) (transport.EventStream, transport.PreparedRequest, error) {
	in.Request.Stream = true
	prepared, err := b.Prepare(ctx, in)
	if err != nil {
		return nil, prepared, err
	}
	stream, err := b.StreamPrepared(ctx, in, prepared)
	return stream, prepared, err
}
func (b *base) StreamPrepared(ctx context.Context, in transport.Invocation, prepared transport.PreparedRequest) (transport.EventStream, error) {
	p := prepared.Clone()
	resp, err := do(ctx, b.client, p)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, httpError(resp.StatusCode, raw)
	}
	// Chat 的 finish_reason 可能先于最终 usage 帧，因此 [DONE] 才是可确认的终点；
	// Responses 本身已有明确 terminal event。
	return &sse{body: resp.Body, reader: transport.NewSSEReader(resp.Body), route: in.Route, decode: b.event, doneIsTerminal: b.id == transport.OpenAIChat}, nil
}
func do(ctx context.Context, c *http.Client, p transport.PreparedRequest) (*http.Response, error) {
	if c == nil {
		c = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, p.Method, p.URL, bytes.NewReader(p.Body))
	if err != nil {
		return nil, err
	}
	req.Header = p.Headers.Clone()
	return c.Do(req)
}
func httpError(status int, body []byte) error {
	var x struct {
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
			Param   any    `json:"param"`
		} `json:"error"`
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
		Param   any    `json:"param"`
	}
	_ = json.Unmarshal(body, &x)
	e := canonical.Error{Status: status, Message: x.Message, Type: x.Type, Code: stringify(x.Code), Param: x.Param, Retryable: status == http.StatusTooManyRequests || status >= http.StatusInternalServerError, Raw: append(json.RawMessage(nil), body...)}
	if x.Error != nil {
		e.Message, e.Type, e.Code, e.Param = x.Error.Message, x.Error.Type, stringify(x.Error.Code), x.Error.Param
	}
	return &HTTPError{Status: status, Body: append([]byte(nil), body...), Details: &e}
}

func stringify(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

type sse struct {
	body           io.ReadCloser
	reader         *transport.SSEReader
	route          transport.Route
	decode         func(string, []byte, transport.Route) ([]canonical.Event, error)
	doneIsTerminal bool
	doneSent       bool
	pending        []canonical.Event
	terminals      []canonical.Event
}

func (s *sse) Close() error { return s.body.Close() }

// flushTerminals 把暂存的各 choice 终态按 choice 顺序放回队列；
// n>1 时各 choice 的 finish 帧到达顺序不定，Engine 按 choice 0 优先结算。
func (s *sse) flushTerminals() {
	if len(s.terminals) == 0 {
		return
	}
	sort.SliceStable(s.terminals, func(i, j int) bool { return s.terminals[i].ChoiceIndex < s.terminals[j].ChoiceIndex })
	s.pending = append(s.pending, s.terminals...)
	s.terminals = nil
}

func (s *sse) Next(ctx context.Context) (canonical.Event, error) {
	for {
		if len(s.pending) > 0 {
			event := s.pending[0]
			s.pending = s.pending[1:]
			if isTerminal(event.Type) {
				s.doneSent = true
			}
			return event, nil
		}
		frame, err := s.reader.Next(ctx)
		if err == io.EOF {
			if len(s.terminals) > 0 {
				s.flushTerminals()
				continue
			}
			return canonical.Event{}, err
		}
		if err != nil {
			return canonical.Event{}, err
		}
		data := bytes.TrimSpace(frame.Data)
		if bytes.Equal(data, []byte("[DONE]")) {
			if len(s.terminals) > 0 {
				s.flushTerminals()
				continue
			}
			if s.doneIsTerminal && !s.doneSent {
				s.doneSent = true
				return canonical.Event{Type: canonical.EventCompleted}, nil
			}
			return canonical.Event{}, io.EOF
		}
		events, err := s.decode(frame.Event, data, s.route)
		if err != nil {
			return canonical.Event{}, err
		}
		for _, event := range events {
			// 暂存 Chat 终态，等待可能紧随其后的 usage 帧合并后再交给 Engine 结算。
			if s.doneIsTerminal && isTerminal(event.Type) && event.Type != canonical.EventError && event.Usage == nil {
				s.terminals = append(s.terminals, event)
				continue
			}
			if s.doneIsTerminal && event.Type == canonical.EventUsage && event.Usage != nil && len(s.terminals) > 0 {
				for index := range s.terminals {
					s.terminals[index].Usage = event.Usage
					s.terminals[index].Raw = append(json.RawMessage(nil), event.Raw...)
				}
				s.flushTerminals()
				continue
			}
			s.pending = append(s.pending, event)
		}
	}
}

func isTerminal(eventType canonical.EventType) bool {
	switch eventType {
	case canonical.EventCompleted, canonical.EventIncomplete, canonical.EventFailed, canonical.EventError:
		return true
	default:
		return false
	}
}
