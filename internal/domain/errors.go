package domain

import (
	"errors"
	"fmt"
	"net/http"
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
