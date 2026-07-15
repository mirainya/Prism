package engine

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

func TestRequestLogRecordsPreparedRequestAndRedactsSecrets(t *testing.T) {
	db := logTestDB(t)
	route := &routing.RouteResult{ChannelID: 2, KeyID: 3, ModelName: "public", VendorModel: "vendor", Transport: model.UpstreamTransportGoogle}
	prepared := transport.PreparedRequest{
		Method:  http.MethodPost,
		URL:     "https://upstream-user:secret@example.test/v1beta/models/vendor:generateContent?apiKey=camel-secret&key=secret&keep=yes",
		Headers: http.Header{"Authorization": []string{"Bearer secret"}, "X-Test": []string{"kept"}},
		Body:    []byte(`{"api_key":"secret","accessToken":"camel-secret","input":"ok"}`),
	}
	entry, err := StartRequestLog(route, prepared, transport.OperationChat)
	if err != nil {
		t.Fatal(err)
	}
	response := &canonical.Response{FinishReason: "STOP", Usage: &canonical.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}, Output: []canonical.Item{{Content: []canonical.Content{{Text: "done"}}}}}
	if err := entry.CompleteResponse(response, http.StatusOK, nil); err != nil {
		t.Fatal(err)
	}
	var stored model.ChannelRequestLog
	if err := db.First(&stored, entry.Record().ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.UpstreamTransport != model.UpstreamTransportGoogle || stored.RequestPath != "/v1beta/models/vendor:generateContent" || stored.URL == prepared.URL {
		t.Fatalf("stored route fields: %#v", stored)
	}
	if stored.RequestType != model.RequestTypeChat || stored.UsageTotalTokens != 5 || stored.ResponsePreview != "done" {
		t.Fatalf("stored response fields: %#v", stored)
	}
	if containsSecret(stored.RequestHeaders) || containsSecret(stored.RequestBody) || containsSecret(stored.URL) {
		t.Fatalf("secret leaked: headers=%s body=%s url=%s", stored.RequestHeaders, stored.RequestBody, stored.URL)
	}
	if contains(stored.URL, "upstream-user") {
		t.Fatalf("URL userinfo leaked: %s", stored.URL)
	}
}

func TestRequestLogCompletesStreamFromCanonicalEvents(t *testing.T) {
	db := logTestDB(t)
	entry, err := StartRequestLog(&routing.RouteResult{ModelName: "m", Transport: model.UpstreamTransportOpenAIChat}, transport.PreparedRequest{Method: http.MethodPost, URL: "https://example.test/v1/chat/completions", Stream: true}, transport.OperationResponses)
	if err != nil {
		t.Fatal(err)
	}
	entry.Observe(canonical.Event{Type: canonical.EventTextDelta, Delta: "part"})
	entry.Observe(canonical.Event{Type: canonical.EventCompleted, Usage: &canonical.Usage{TotalTokens: 7}})
	if err := entry.CompleteStream(http.StatusOK, nil); err != nil {
		t.Fatal(err)
	}
	var stored model.ChannelRequestLog
	if err := db.First(&stored, entry.Record().ID).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.IsStream || stored.RequestType != model.RequestTypeResponses || stored.FinishReason != "" || stored.UsageTotalTokens != 7 {
		t.Fatalf("stored stream: %#v", stored)
	}
	var events []canonical.Event
	if err := json.Unmarshal([]byte(stored.ResponseBody), &events); err != nil || len(events) != 2 {
		t.Fatalf("events=%s err=%v", stored.ResponseBody, err)
	}
}

type statusError struct{}

func (statusError) Error() string   { return "limited" }
func (statusError) HTTPStatus() int { return http.StatusTooManyRequests }

func TestRequestLogUsesUpstreamErrorStatus(t *testing.T) {
	db := logTestDB(t)
	entry, err := StartRequestLog(&routing.RouteResult{ModelName: "m"}, transport.PreparedRequest{Method: http.MethodPost, URL: "https://example.test/x"}, transport.OperationChat)
	if err != nil {
		t.Fatal(err)
	}
	if err := entry.CompleteResponse(nil, 0, statusError{}); err != nil {
		t.Fatal(err)
	}
	var stored model.ChannelRequestLog
	if err := db.First(&stored, entry.Record().ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.StatusCode != http.StatusTooManyRequests || !errors.Is(statusError{}, statusError{}) {
		t.Fatalf("stored error: %#v", stored)
	}
}

func logTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.ChannelRequestLog{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	return db
}

func containsSecret(value string) bool {
	return value == "secret" || len(value) >= 6 && (value == "secret" || contains(value, "secret"))
}

func contains(value, match string) bool {
	for index := 0; index+len(match) <= len(value); index++ {
		if value[index:index+len(match)] == match {
			return true
		}
	}
	return false
}
