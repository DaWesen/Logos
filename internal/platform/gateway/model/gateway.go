package model

import "time"

type Response struct {
	Code      int         `json:"code"`
	Message   string      `json:"message"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp int64       `json:"timestamp"`
}

func Success(data interface{}) *Response {
	return &Response{
		Code:      200,
		Message:   "success",
		Data:      data,
		Timestamp: time.Now().UnixMilli(),
	}
}

func SuccessWithMessage(message string, data interface{}) *Response {
	return &Response{
		Code:      200,
		Message:   message,
		Data:      data,
		Timestamp: time.Now().UnixMilli(),
	}
}

func Error(code int, message string) *Response {
	return &Response{
		Code:      code,
		Message:   message,
		Timestamp: time.Now().UnixMilli(),
	}
}

func BadRequest(message string) *Response {
	return &Response{Code: 400, Message: message, Timestamp: time.Now().UnixMilli()}
}

func Unauthorized(message string) *Response {
	if message == "" {
		message = "未授权，请先登录"
	}
	return &Response{Code: 401, Message: message, Timestamp: time.Now().UnixMilli()}
}

func Forbidden(message string) *Response {
	if message == "" {
		message = "权限不足"
	}
	return &Response{Code: 403, Message: message, Timestamp: time.Now().UnixMilli()}
}

func NotFound(message string) *Response {
	if message == "" {
		message = "资源不存在"
	}
	return &Response{Code: 404, Message: message, Timestamp: time.Now().UnixMilli()}
}

func InternalError(message string) *Response {
	if message == "" {
		message = "服务器内部错误"
	}
	return &Response{Code: 500, Message: message, Timestamp: time.Now().UnixMilli()}
}

const (
	CodeSuccess            = 200
	CodeBadRequest         = 400
	CodeUnauthorized       = 401
	CodeForbidden          = 403
	CodeNotFound           = 404
	CodeMethodNotAllowed   = 405
	CodeInternalError      = 500
	CodeServiceUnavailable = 503

	ErrInvalidToken      = "invalid_token"
	ErrTokenExpired      = "token_expired"
	ErrPermissionDenied  = "permission_denied"
	ErrRateLimitExceeded = "rate_limit_exceeded"
)
