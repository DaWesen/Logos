package types

// Platform 领域共享类型
// 包含网关、用户、监控服务共用的数据结构

// UserStatus 用户状态
type UserStatus int

const (
	UserStatusActive  UserStatus = iota + 1 // 活跃
	UserStatusInactive                       // 未激活
	UserStatusBanned                         // 封禁
)

// UserRole 用户角色
type UserRole int

const (
	UserRoleAdmin  UserRole = iota + 1 // 管理员
	UserRoleUser                        // 普通用户
	UserRoleGuest                       // 访客
)

// APIResponse 统一 API 响应格式
type APIResponse struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// PaginatedRequest 分页请求
type PaginatedRequest struct {
	Page     int `json:"page" form:"page"`
	PageSize int `json:"page_size" form:"page_size"`
}

// PaginatedResponse 分页响应
type PaginatedResponse struct {
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
	Items    interface{} `json:"items"`
}

// HealthStatus 健康检查状态
type HealthStatus struct {
	Service string `json:"service"`
	Status  string `json:"status"`
	Uptime  int64  `json:"uptime"`
	Version string `json:"version"`
}

// Common errors
var (
	ErrUserNotFound   = &PlatformError{Code: 40401, Message: "user not found"}
	ErrUnauthorized   = &PlatformError{Code: 40101, Message: "unauthorized"}
	ErrForbidden      = &PlatformError{Code: 40301, Message: "forbidden"}
	ErrRateLimited    = &PlatformError{Code: 42901, Message: "rate limited"}
	ErrInternalServer = &PlatformError{Code: 50001, Message: "internal server error"}
)

// PlatformError 平台领域错误
type PlatformError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *PlatformError) Error() string {
	return e.Message
}
