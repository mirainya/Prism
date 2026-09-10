package callback

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRegisterUnifiedRoutesUsesFixedCallbackPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterUnifiedRoutes(router.Group("/internal/gateway"))

	found := false
	for _, route := range router.Routes() {
		if route.Method == http.MethodPost && route.Path == "/internal/gateway/callback" {
			found = true
		}
		if route.Path == "/internal/gateway/callback/:scope" {
			t.Fatalf("legacy scope route is still registered: %s %s", route.Method, route.Path)
		}
	}
	if !found {
		t.Fatal("fixed callback route is not registered")
	}

	request := httptest.NewRequest(http.MethodPost, "/internal/gateway/callback/provider-a", strings.NewReader(`{"ok":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("legacy scope path status=%d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestIsJSONContentTypeIsStrict(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"APPLICATION/JSON", true},
		{"application/problem+json", true},
		{"application/jsonx", false},
		{"text/json", false},
		{"application/json; charset=", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			if got := isJSONContentType(tc.value); got != tc.want {
				t.Fatalf("isJSONContentType(%q)=%v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestHandleUnifiedCallbackRejectsUnsupportedContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/internal/gateway/callback", HandleUnifiedCallback)
	for _, contentType := range []string{"application/jsonx", "text/plain", ""} {
		t.Run(contentType, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/internal/gateway/callback", strings.NewReader(`{"ok":true}`))
			if contentType != "" {
				request.Header.Set("Content-Type", contentType)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("status=%d, want %d", response.Code, http.StatusUnsupportedMediaType)
			}
		})
	}
}

func TestHandleUnifiedCallbackRejectsDeclaredOversizeBeforeAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/internal/gateway/callback", HandleUnifiedCallback)
	request := httptest.NewRequest(http.MethodPost, "/internal/gateway/callback", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.ContentLength = maxCallbackBody + 1
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHandleUnifiedCallbackRejectsChunkedOversize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/internal/gateway/callback", HandleUnifiedCallback)
	// A syntactically valid token gets us past authentication-header parsing;
	// the handler must still reject a chunked body without consulting storage.
	token := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" // 32 zero bytes, raw URL base64
	body := bytes.Repeat([]byte("x"), maxCallbackBody+1)
	request := httptest.NewRequest(http.MethodPost, "/internal/gateway/callback", bytes.NewReader(body))
	request.ContentLength = -1
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Gateway-Callback-Token", token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestAllowCallbackRateEnforcesPerClientWindow(t *testing.T) {
	key := "callback-test-rate-" + time.Now().UTC().Format("150405.000000000")
	callbackRateState.Lock()
	delete(callbackRateState.entries, key)
	callbackRateState.Unlock()

	now := time.Date(2026, 9, 9, 1, 2, 3, 0, time.UTC)
	for i := 0; i < callbackRateLimit; i++ {
		if !allowCallbackRate(key, now) {
			t.Fatalf("request %d was rejected before the limit", i+1)
		}
	}
	if allowCallbackRate(key, now) {
		t.Fatal("request over the per-window limit was accepted")
	}
	if !allowCallbackRate(key, now.Add(callbackRateWindow)) {
		t.Fatal("request in a new rate window was rejected")
	}
}

func TestAcquireCallbackIngressRejectsWhenConcurrencyPoolIsFull(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldSemaphore := callbackIngressSemaphore
	callbackIngressSemaphore = make(chan struct{}, 1)
	t.Cleanup(func() { callbackIngressSemaphore = oldSemaphore })

	first := newCallbackTestContext("198.51.100.10:1001")
	if !acquireCallbackIngress(first) {
		t.Fatal("first callback should acquire the ingress slot")
	}
	second := newCallbackTestContext("198.51.100.11:1001")
	if acquireCallbackIngress(second) {
		t.Fatal("second callback acquired a full ingress pool")
	}
	if second.Writer.Status() != http.StatusServiceUnavailable {
		t.Fatalf("full-pool status=%d, want %d", second.Writer.Status(), http.StatusServiceUnavailable)
	}
	releaseCallbackIngress()
}

func newCallbackTestContext(remoteAddr string) *gin.Context {
	request := httptest.NewRequest(http.MethodPost, "/internal/gateway/callback", strings.NewReader(`{}`))
	request.RemoteAddr = remoteAddr
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = request
	return ctx
}
