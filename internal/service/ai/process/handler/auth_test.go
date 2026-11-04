package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"Logos/pkg/jwt"
)

type fakeParser struct {
	claims *jwt.Claims
	err    error
}

func (p fakeParser) ParseToken(string) (*jwt.Claims, error) {
	return p.claims, p.err
}

func newAuthedRouter(t *testing.T, parser tokenParser) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(requireAuth(parser))
	r.GET("/whoami", func(c *gin.Context) {
		userID, _ := c.Get("user_id")
		c.JSON(http.StatusOK, gin.H{"user_id": userID})
	})
	return r
}

func doRequest(r *gin.Engine, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRequireAuth_MissingToken(t *testing.T) {
	r := newAuthedRouter(t, fakeParser{})
	w := doRequest(r, nil)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d，缺令牌必须 401", w.Code)
	}
}

// TestRequireAuth_IgnoresForgedUserHeader 关键回归：
// process 服务以前只读 X-User-ID，任何人手写这个头就能冒充别人。
func TestRequireAuth_IgnoresForgedUserHeader(t *testing.T) {
	r := newAuthedRouter(t, fakeParser{})
	w := doRequest(r, map[string]string{"X-User-ID": "victim-user"})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d，伪造 X-User-ID 不能通过认证", w.Code)
	}
}

func TestRequireAuth_ValidTokenSetsIdentity(t *testing.T) {
	r := newAuthedRouter(t, fakeParser{claims: &jwt.Claims{UserID: "user-1", Role: "user"}})

	w := doRequest(r, map[string]string{"Authorization": "Bearer any-token"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != `{"user_id":"user-1"}` {
		t.Fatalf("handler 应拿到 JWT 中的 user_id，实际 %s", got)
	}
}

func TestRequireAuth_InvalidTokenRejected(t *testing.T) {
	r := newAuthedRouter(t, fakeParser{err: errors.New("bad token")})
	w := doRequest(r, map[string]string{"Authorization": "Bearer not-a-token"})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d，非法令牌必须 401", w.Code)
	}
}

func TestBearerToken(t *testing.T) {
	cases := map[string]string{
		"Bearer abc": "abc",
		"bearer abc": "abc",
		"Bearer":     "",
		"Basic abc":  "",
		"Bearer a b": "",
		"":           "",
	}
	for header, want := range cases {
		if got := bearerToken(header); got != want {
			t.Errorf("bearerToken(%q) = %q，期望 %q", header, got, want)
		}
	}
}
