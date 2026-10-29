package middleware

import "testing"

// TestIsPublicPath 白名单必须精确匹配：
// 旧实现用 strings.HasPrefix(path, "/api/v1/auth")，会把 /api/v1/auth/logout
// 以及 /api/v1/authXxx 一并放行，等于新增的认证接口天生没有鉴权。
func TestIsPublicPath(t *testing.T) {
	public := []string{
		"/health",
		"/metrics",
		"/test",
		"/ws",
		"/api/v1/auth/login",
		"/api/v1/auth/register",
		"/api/v1/auth/check-username",
		"/api/v1/search/public/hot",
	}
	for _, path := range public {
		if !isPublicPath(path) {
			t.Fatalf("%s 应免认证", path)
		}
	}

	private := []string{
		"/api/v1/auth/logout",
		"/api/v1/auth/loginX",
		"/api/v1/users",
		"/api/v1/users/1",
		"/api/v1/chat/group/mute",
		"/api/v1/search/private",
	}
	for _, path := range private {
		if isPublicPath(path) {
			t.Fatalf("%s 不应被免认证白名单放行", path)
		}
	}
}
