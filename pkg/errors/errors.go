package errors

import "fmt"

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("[%d] %s", e.Code, e.Message)
}

func New(code int, message string) *Error {
	return &Error{Code: code, Message: message}
}

// 错误码分组基数
const (
	ClientErrorBase = 40000 // 客户端错误基数
	ServerErrorBase = 50000 // 服务端错误基数
)

// 客户端错误 40001 ~ 49999
var (
	ErrInvalidToken      = New(ClientErrorBase+1, "invalid or disabled token")
	ErrInsufficientQuota = New(ClientErrorBase+2, "insufficient quota")
	ErrInvalidParams     = New(ClientErrorBase+3, "invalid params")
	ErrTaskNotFound      = New(ClientErrorBase+4, "task not found")
	ErrNoPermission      = New(ClientErrorBase+5, "no permission to access this task")
	ErrModelNotFound     = New(ClientErrorBase+6, "model not found")
	ErrUnauthorized      = New(ClientErrorBase+7, "unauthorized")
	ErrRateLimited       = New(ClientErrorBase+8, "rate limited")
	ErrDuplicateRequest  = New(ClientErrorBase+9, "duplicate request")
)

// 服务端错误 50001 ~ 59999
var (
	ErrNoAvailableChannel = New(ServerErrorBase+1, "no available channel")
	ErrProviderError      = New(ServerErrorBase+2, "provider error")
	ErrUploadFailed       = New(ServerErrorBase+3, "upload failed")
	ErrCallbackFailed     = New(ServerErrorBase+4, "callback failed")
	ErrQueueFailed        = New(ServerErrorBase+5, "queue operation failed")
	ErrInternalError      = New(ServerErrorBase+99, "internal error")
)

// IsClientError 判断是否为客户端错误
func IsClientError(err *Error) bool {
	return err.Code >= ClientErrorBase && err.Code < ServerErrorBase
}

// IsServerError 判断是否为服务端错误
func IsServerError(err *Error) bool {
	return err.Code >= ServerErrorBase
}

func WithMessage(err *Error, msg string) *Error {
	return &Error{
		Code:    err.Code,
		Message: msg,
	}
}
