package domain

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// 通用业务错误
var (
	ErrNotFound            = errors.New("record not found")
	ErrNoAvailableAccount  = errors.New("no available account")
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrIdempotentConflict  = errors.New("idempotent conflict: already processed")
)

// AppError 统一业务错误类型
type AppError struct {
	HTTPStatus int    `json:"-"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Err        error  `json:"-"`
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *AppError) Unwrap() error {
	return e.Err
}

// 常用错误构造器

func ErrBadRequest(msg string) *AppError {
	return &AppError{HTTPStatus: http.StatusBadRequest, Code: "bad_request", Message: msg}
}

func ErrUnauthorized(msg string) *AppError {
	return &AppError{HTTPStatus: http.StatusUnauthorized, Code: "unauthorized", Message: msg}
}

func ErrForbidden(msg string) *AppError {
	return &AppError{HTTPStatus: http.StatusForbidden, Code: "forbidden", Message: msg}
}

func ErrNotFoundMsg(msg string) *AppError {
	return &AppError{HTTPStatus: http.StatusNotFound, Code: "not_found", Message: msg}
}

func ErrRateLimit(msg string) *AppError {
	return &AppError{HTTPStatus: http.StatusTooManyRequests, Code: "rate_limited", Message: msg}
}

func ErrInternal(msg string, err error) *AppError {
	return &AppError{HTTPStatus: http.StatusInternalServerError, Code: "internal_error", Message: msg, Err: err}
}

func ErrUpstream(msg string, err error) *AppError {
	return &AppError{HTTPStatus: http.StatusBadGateway, Code: "upstream_error", Message: msg, Err: err}
}

func ErrTimeout(msg string) *AppError {
	return &AppError{HTTPStatus: http.StatusGatewayTimeout, Code: "timeout", Message: msg}
}

// IsAppError 判断是否为 AppError 并提取
func IsAppError(err error) (*AppError, bool) {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr, true
	}
	return nil, false
}

// UpstreamError 携带上游 HTTP 状态码的错误,供 per-model 熔断分类使用
type UpstreamError struct {
	StatusCode int
	Body       string
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("upstream error: status=%d, body=%s", e.StatusCode, e.Body)
}

// UpstreamStatusCode 从错误链中提取上游状态码
// 优先取结构化 UpstreamError; 否则从 "http error: <code>" 字符串兜底解析; 取不到返回 0
func UpstreamStatusCode(err error) int {
	if err == nil {
		return 0
	}
	var ue *UpstreamError
	if errors.As(err, &ue) {
		return ue.StatusCode
	}
	var statusProvider interface{ HTTPStatus() int }
	if errors.As(err, &statusProvider) {
		return statusProvider.HTTPStatus()
	}
	return parseStatusFromString(err.Error())
}

// ClassifyUpstreamError 根据上游错误决定是否熔断该账号及退避时长
// 返回 (shouldBreak 是否熔断, backoff 退避时长)
// 401/403/404 → 6h (key 不支持该模型/鉴权失败); 429 → 3min (限流,非能力问题);
// 5xx → 不熔断(渠道整体故障,非账号问题); 其他 4xx → 6h
func ClassifyUpstreamError(err error) (shouldBreak bool, backoff time.Duration) {
	code := UpstreamStatusCode(err)
	switch code {
	case 401, 403, 404:
		// 鉴权失败/无权限/模型不存在 → key 不支持该模型,长退避
		return true, 6 * time.Hour
	case 429:
		// 限流 → 短退避,换账号规避
		return true, 3 * time.Minute
	default:
		// 5xx(渠道故障)、400/422(请求本身问题)等 → 不熔断账号
		return false, 0
	}
}

// parseStatusFromString 兜底: 从 "http error: 401, body: ..." 或 "status 503" 提取状态码
func parseStatusFromString(msg string) int {
	lowerMessage := strings.ToLower(msg)
	for _, prefix := range []string{"http error: ", "status=", "status ", "returned "} {
		idx := strings.Index(lowerMessage, prefix)
		if idx < 0 {
			continue
		}
		rest := msg[idx+len(prefix):]
		var code int
		if _, e := fmt.Sscanf(rest, "%d", &code); e == nil && code >= 100 && code < 600 {
			return code
		}
	}
	return 0
}
