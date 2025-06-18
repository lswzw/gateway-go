package errors

import (
	"fmt"
	"net/http"
)

// Error 表示一个错误
type Error struct {
	Code    int
	Message string
	Err     error
	Details any
}

// Error 实现 error 接口
func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

// Unwrap 返回原始错误
func (e *Error) Unwrap() error {
	return e.Err
}

// GatewayError 表示网关错误
type GatewayError Error

// Error 实现 error 接口
func (e *GatewayError) Error() string {
	return (*Error)(e).Error()
}

// Unwrap 返回原始错误
func (e *GatewayError) Unwrap() error {
	return (*Error)(e).Unwrap()
}

// UpstreamError 表示上游服务错误
type UpstreamError Error

// Error 实现 error 接口
func (e *UpstreamError) Error() string {
	return (*Error)(e).Error()
}

// Unwrap 返回原始错误
func (e *UpstreamError) Unwrap() error {
	return (*Error)(e).Unwrap()
}

// PluginError 表示插件错误
type PluginError Error

// Error 实现 error 接口
func (e *PluginError) Error() string {
	return (*Error)(e).Error()
}

// Unwrap 返回原始错误
func (e *PluginError) Unwrap() error {
	return (*Error)(e).Unwrap()
}

// New 创建一个新的错误
func New(code int, message string) *Error {
	return &Error{
		Code:    code,
		Message: message,
	}
}

// Wrap 包装一个错误
func Wrap(err error, code int, message string) *Error {
	return &Error{
		Code:    code,
		Message: message,
		Err:     err,
	}
}

// NewGatewayError 创建一个新的网关错误
func NewGatewayError(code int, message string) *GatewayError {
	return (*GatewayError)(New(code, message))
}

// NewUpstreamError 创建一个新的上游服务错误
func NewUpstreamError(code int, message string) *UpstreamError {
	return (*UpstreamError)(New(code, message))
}

// NewPluginError 创建一个新的插件错误
func NewPluginError(code int, message string) *PluginError {
	return (*PluginError)(New(code, message))
}

// IsGatewayError 检查是否是网关错误
func IsGatewayError(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*GatewayError)
	return ok
}

// IsUpstreamError 检查是否是上游服务错误
func IsUpstreamError(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*UpstreamError)
	return ok
}

// IsPluginError 检查是否是插件错误
func IsPluginError(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*PluginError)
	return ok
}

// 预定义错误码
const (
	ErrBadRequestCode         = 400
	ErrUnauthorizedCode       = 401
	ErrForbiddenCode          = 403
	ErrNotFound               = 404
	ErrMethodNotAllowed       = 405
	ErrRequestTimeout         = 408
	ErrInternalServerError    = 500
	ErrBadGateway             = 502
	ErrServiceUnavailableCode = 503
)

// 预定义错误
var (
	BadRequest          = New(ErrBadRequestCode, "无效的请求")
	Unauthorized        = New(ErrUnauthorizedCode, "未授权")
	Forbidden           = New(ErrForbiddenCode, "禁止访问")
	NotFound            = New(ErrNotFound, "资源不存在")
	MethodNotAllowed    = New(ErrMethodNotAllowed, "方法不允许")
	RequestTimeout      = New(ErrRequestTimeout, "请求超时")
	InternalServerError = New(ErrInternalServerError, "服务器内部错误")
	BadGateway          = New(ErrBadGateway, "网关错误")
	ServiceUnavailable  = New(ErrServiceUnavailableCode, "服务暂时不可用")
)

// Is 判断错误类型
func Is(err error, code int) bool {
	if e, ok := err.(*Error); ok {
		return e.Code == code
	}
	return false
}

// HTTPStatus 获取对应的 HTTP 状态码
func (e *Error) HTTPStatus() int {
	switch e.Code {
	case http.StatusBadRequest:
		return http.StatusBadRequest
	case http.StatusUnauthorized:
		return http.StatusUnauthorized
	case http.StatusForbidden:
		return http.StatusForbidden
	case http.StatusNotFound:
		return http.StatusNotFound
	case http.StatusMethodNotAllowed:
		return http.StatusMethodNotAllowed
	case http.StatusRequestTimeout:
		return http.StatusRequestTimeout
	case http.StatusConflict:
		return http.StatusConflict
	case http.StatusTooManyRequests:
		return http.StatusTooManyRequests
	case http.StatusInternalServerError:
		return http.StatusInternalServerError
	case http.StatusServiceUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// WithDetails 添加详细信息
func (e *Error) WithDetails(details any) *Error {
	e.Details = details
	return e
}

// As 获取错误类型
func As(err error) (*Error, bool) {
	var e *Error
	ok := false
	if err != nil {
		ok = true
		e = &Error{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
			Err:     err,
		}
	}
	return e, ok
}

// ErrorCode 错误代码类型
type ErrorCode string

// 系统级错误
const (
	ErrInternalServer     ErrorCode = "INTERNAL_SERVER_ERROR"
	ErrConfigLoad         ErrorCode = "CONFIG_LOAD_ERROR"
	ErrConfigValidate     ErrorCode = "CONFIG_VALIDATE_ERROR"
	ErrTimeout            ErrorCode = "TIMEOUT"
	ErrTokenExpired       ErrorCode = "TOKEN_EXPIRED"
	ErrTokenInvalid       ErrorCode = "TOKEN_INVALID"
	ErrTooManyRequests    ErrorCode = "TOO_MANY_REQUESTS"
	ErrCircuitBreakerOpen ErrorCode = "CIRCUIT_BREAKER_OPEN"
)

// 业务级错误
const (
	ErrInvalidParam     ErrorCode = "INVALID_PARAM"
	ErrMissingParam     ErrorCode = "MISSING_PARAM"
	ErrInvalidFormat    ErrorCode = "INVALID_FORMAT"
	ErrBusiness         ErrorCode = "BUSINESS_ERROR"
	ErrResourceNotFound ErrorCode = "RESOURCE_NOT_FOUND"
	ErrResourceExists   ErrorCode = "RESOURCE_EXISTS"
	ErrOperationFailed  ErrorCode = "OPERATION_FAILED"
)
