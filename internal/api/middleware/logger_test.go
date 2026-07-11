package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestRequestLoggerOmitsSensitiveRequestBodies(t *testing.T) {
	paths := []string{
		"/api/auth/login",
		"/api/auth/register",
		"/api/user/password",
		"/api/auth/login/",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			logs := installObservedLogger(t)
			router := newLoggerTestRouter(func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})

			body := `{"password":"secret"}`
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(httptest.NewRecorder(), request)

			entry := logs.All()[0]
			if _, exists := entry.ContextMap()["request"]; exists {
				t.Fatalf("sensitive request body was logged for %s", path)
			}
		})
	}
}

func TestRequestLoggerLogsSmallNonSensitiveRequestBody(t *testing.T) {
	logs := installObservedLogger(t)
	router := newLoggerTestRouter(func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	body := `{"name":"test"}`
	request := httptest.NewRequest(http.MethodPost, "/api/tokens", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), request)

	entry := logs.All()[0]
	if got := entry.ContextMap()["request"]; got != body {
		t.Fatalf("logged request body = %q, want %q", got, body)
	}
}

func TestRequestLoggerRedactsSensitiveJSONFieldsRecursively(t *testing.T) {
	logs := installObservedLogger(t)
	router := newLoggerTestRouter(func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	body := `{"name":"channel","api_key":"sk-live","config":{"clientSecret":"nested-secret","clientKey":"client-key","name":"kept"},"items":[{"authorization":"Bearer abc","key":"raw-key"}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/channel-accounts", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	router.ServeHTTP(httptest.NewRecorder(), request)

	loggedBody, ok := logs.All()[0].ContextMap()["request"].(string)
	if !ok {
		t.Fatal("sanitized JSON request body was not logged")
	}
	expected := `{"name":"channel","api_key":"[REDACTED]","config":{"clientSecret":"[REDACTED]","clientKey":"[REDACTED]","name":"kept"},"items":[{"authorization":"[REDACTED]","key":"[REDACTED]"}]}`
	assertJSONEqual(t, loggedBody, expected)

	for _, secret := range []string{"sk-live", "nested-secret", "client-key", "Bearer abc", "raw-key"} {
		if strings.Contains(loggedBody, secret) {
			t.Fatalf("logged request body contains secret %q", secret)
		}
	}
}

func TestRequestLoggerSkipsNonJSONRequestBodies(t *testing.T) {
	contentTypes := []string{
		"multipart/form-data; boundary=test-boundary",
		"application/octet-stream",
		"text/plain",
	}

	for _, contentType := range contentTypes {
		t.Run(contentType, func(t *testing.T) {
			logs := installObservedLogger(t)
			body := []byte("file-or-form-content")
			reader := &countingReadCloser{Reader: bytes.NewReader(body)}
			router := newLoggerTestRouter(func(c *gin.Context) {
				if reader.bytesRead != 0 {
					t.Fatalf("middleware read %d non-JSON bytes before handler", reader.bytesRead)
				}
				got, err := io.ReadAll(c.Request.Body)
				if err != nil {
					t.Fatalf("read request body: %v", err)
				}
				if !bytes.Equal(got, body) {
					t.Fatalf("handler received %q, want %q", got, body)
				}
				c.Status(http.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodPost, "/api/playground/1/upload", reader)
			request.Header.Set("Content-Type", contentType)
			router.ServeHTTP(httptest.NewRecorder(), request)

			if _, exists := logs.All()[0].ContextMap()["request"]; exists {
				t.Fatal("non-JSON request body was logged")
			}
		})
	}
}

func TestRequestLoggerBoundsCaptureAndPreservesRequestBody(t *testing.T) {
	logs := installObservedLogger(t)
	body := bytes.Repeat([]byte("a"), maxRequestBodyLogBytes*2)
	reader := &countingReadCloser{Reader: bytes.NewReader(body)}

	router := newLoggerTestRouter(func(c *gin.Context) {
		if reader.bytesRead != maxRequestBodyLogBytes {
			t.Fatalf("middleware read %d bytes before handler, want %d", reader.bytesRead, maxRequestBodyLogBytes)
		}
		got, err := io.ReadAll(c.Request.Body)
		if err != nil {
			t.Fatalf("read replayed request body: %v", err)
		}
		if !bytes.Equal(got, body) {
			t.Fatalf("handler received %d bytes, want %d", len(got), len(body))
		}
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodPost, "/api/tokens", reader)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), request)

	entry := logs.All()[0]
	if _, exists := entry.ContextMap()["request"]; exists {
		t.Fatal("request body at the capture limit should not be logged")
	}
}

func TestResponseWriterBoundsErrorResponseCapture(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	writer := &responseWriter{
		ResponseWriter: context.Writer,
		body:           bytes.NewBuffer(nil),
		statusCode:     http.StatusInternalServerError,
	}
	payload := bytes.Repeat([]byte("x"), maxResponseBodyLogBytes*2)

	written, err := writer.Write(payload)
	if err != nil {
		t.Fatalf("write response: %v", err)
	}
	if written != len(payload) {
		t.Fatalf("written bytes = %d, want %d", written, len(payload))
	}
	if writer.body.Len() != maxResponseBodyLogBytes {
		t.Fatalf("captured response bytes = %d, want %d", writer.body.Len(), maxResponseBodyLogBytes)
	}
	if recorder.Body.Len() != len(payload) {
		t.Fatalf("forwarded response bytes = %d, want %d", recorder.Body.Len(), len(payload))
	}
}

func TestRequestLoggerUsesRouteTemplateForSignedCallback(t *testing.T) {
	logs := installObservedLogger(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestLogger())
	router.POST("/internal/callback/v1/:channel_type/:task_no/:signature", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(
		http.MethodPost,
		"/internal/callback/v1/provider/task-123/sensitive-signature",
		strings.NewReader(`{"status":"success"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), request)

	entry := logs.All()[0]
	path, _ := entry.ContextMap()["path"].(string)
	want := "/internal/callback/v1/:channel_type/:task_no/:signature"
	if path != want {
		t.Fatalf("logged path = %q, want %q", path, want)
	}
	if strings.Contains(path, "sensitive-signature") {
		t.Fatal("logged callback path contains signature")
	}
}

func assertJSONEqual(t *testing.T, got, want string) {
	t.Helper()
	var gotValue any
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("decode logged JSON: %v", err)
	}
	var wantValue any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode expected JSON: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("logged JSON = %s, want %s", got, want)
	}
}

type countingReadCloser struct {
	*bytes.Reader
	bytesRead int
}

func (r *countingReadCloser) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.bytesRead += n
	return n, err
}

func (r *countingReadCloser) Close() error {
	return nil
}

func installObservedLogger(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, logs := observer.New(zapcore.InfoLevel)
	previous := logger.L
	logger.L = zap.New(core)
	t.Cleanup(func() {
		logger.L = previous
	})
	return logs
}

func newLoggerTestRouter(handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestLogger())
	router.Any("/*path", handler)
	return router
}
