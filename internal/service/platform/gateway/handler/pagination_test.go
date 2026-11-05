package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"Logos/pkg/pagination"
	pbBilling "Logos/proto_gen/billing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
)

func newPaginationContext(rawQuery string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/?"+rawQuery, nil)
	return c, rec
}

// 非法分页参数必须在网关层被拒绝，而不是静默变成 0 再透传给数据库：
// page=0 会生成负 OFFSET，pageSize=0 可能让 SQL 直接省略 LIMIT。
func TestParsePagination_RejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"页码不是数字", "page=abc"},
		{"页码为 0", "page=0"},
		{"页码为负", "page=-1"},
		{"页大小不是数字", "pageSize=abc"},
		{"页大小为 0", "pageSize=0"},
		{"页大小为负", "pageSize=-5"},
		{"页码溢出 int", "page=99999999999999999999"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newPaginationContext(tc.query)

			_, _, ok := parsePagination(c, "page", "pageSize", pagination.DefaultPageSize)

			if ok {
				t.Fatalf("非法参数 %q 被接受", tc.query)
			}
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("HTTP 状态 = %d, want 400", rec.Code)
			}
			var body struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("响应体不是合法 JSON: %v", err)
			}
			if !strings.Contains(body.Message, "必须是 >= 1 的整数") {
				t.Fatalf("错误信息缺少可操作提示: %q", body.Message)
			}
		})
	}
}

// 超过上限的 pageSize 收敛到 MaxPageSize，而不是 400：
// 前端滑动加载可能顺手传一个很大的数，拒绝会让整页加载失败；
// 但绝不能把 1000000000 原样交给数据库。
func TestParsePagination_ClampsOversizedPageSize(t *testing.T) {
	c, _ := newPaginationContext("page=1&pageSize=1000000000")

	page, pageSize, ok := parsePagination(c, "page", "pageSize", pagination.DefaultPageSize)

	if !ok {
		t.Fatal("合法整数不应被拒绝")
	}
	if page != 1 {
		t.Fatalf("page = %d, want 1", page)
	}
	if pageSize != pagination.MaxPageSize {
		t.Fatalf("pageSize = %d, want %d", pageSize, pagination.MaxPageSize)
	}
}

func TestParsePagination_DefaultsWhenAbsent(t *testing.T) {
	c, _ := newPaginationContext("")

	page, pageSize, ok := parsePagination(c, "page", "pageSize", pagination.DefaultPageSize)

	if !ok {
		t.Fatal("缺省参数应使用默认值")
	}
	if page != pagination.DefaultPage || pageSize != pagination.DefaultPageSize {
		t.Fatalf("默认值 = (%d, %d), want (%d, %d)",
			page, pageSize, pagination.DefaultPage, pagination.DefaultPageSize)
	}
}

// 空字符串视作「没传」：`?pageSize=` 这种前端表单空值不应把整个请求打成 400。
func TestParsePagination_EmptyValueFallsBackToDefault(t *testing.T) {
	c, _ := newPaginationContext("page=&pageSize=")

	page, pageSize, ok := parsePagination(c, "page", "pageSize", pagination.DefaultPageSize)

	if !ok {
		t.Fatal("空值应回落到默认值")
	}
	if page != pagination.DefaultPage || pageSize != pagination.DefaultPageSize {
		t.Fatalf("默认值 = (%d, %d)", page, pageSize)
	}
}

// knowledge 接口历史上同时接受 pageSize 和 page_size，两条入口都必须校验。
func TestParsePaginationAlias_CoversBothSpellings(t *testing.T) {
	for _, key := range []string{"pageSize", "page_size"} {
		t.Run(key, func(t *testing.T) {
			c, rec := newPaginationContext(key + "=abc")

			_, _, ok := parsePaginationAlias(c, []string{"page"}, []string{"pageSize", "page_size"}, pagination.DefaultPageSize)

			if ok {
				t.Fatalf("%s=abc 被接受", key)
			}
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("HTTP 状态 = %d, want 400", rec.Code)
			}
		})
	}
}

func TestParseLimit(t *testing.T) {
	cases := []struct {
		name   string
		query  string
		want   int
		wantOK bool
	}{
		{"缺省", "", 20, true},
		{"合法值", "limit=5", 5, true},
		{"超过上限收敛", "limit=1000", pagination.MaxPageSize, true},
		{"非数字拒绝", "limit=abc", 0, false},
		{"0 拒绝", "limit=0", 0, false},
		{"负数拒绝", "limit=-1", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newPaginationContext(tc.query)

			got, ok := parseLimit(c, "limit", 20, pagination.MaxPageSize)

			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("limit = %d, want %d", got, tc.want)
			}
			if !tc.wantOK && rec.Code != http.StatusBadRequest {
				t.Fatalf("HTTP 状态 = %d, want 400", rec.Code)
			}
		})
	}
}

// fakeTransactionsClient 记录网关下发的分页参数，用于证明非法请求没有到达下游。
type fakeTransactionsClient struct {
	pbBilling.BillingServiceClient
	calls int
	got   *pbBilling.GetTransactionsRequest
}

func (f *fakeTransactionsClient) GetTransactions(_ context.Context, in *pbBilling.GetTransactionsRequest, _ ...grpc.CallOption) (*pbBilling.GetTransactionsResponse, error) {
	f.calls++
	f.got = in
	return &pbBilling.GetTransactionsResponse{Code: 0, Message: "ok"}, nil
}

func TestGetTransactions_InvalidPaginationNeverReachesDownstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name     string
		query    string
		wantCode int
	}{
		{"page=abc", "page=abc", http.StatusBadRequest},
		{"page=0", "page=0", http.StatusBadRequest},
		{"pageSize=abc", "pageSize=abc", http.StatusBadRequest},
		{"合法请求", "page=2&pageSize=30", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeTransactionsClient{}
			h := &Handler{BillingClient: client}

			r := gin.New()
			r.GET("/billing/transactions", func(c *gin.Context) {
				c.Set("user_id", "user-1")
				h.GetTransactions(c)
			})

			req := httptest.NewRequest(http.MethodGet, "/billing/transactions?"+tc.query, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("HTTP 状态 = %d, want %d, body = %s", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantCode != http.StatusOK {
				if client.calls != 0 {
					t.Fatalf("非法参数不应调用下游，实际调用 %d 次", client.calls)
				}
				return
			}
			if client.got.GetPage() != 2 || client.got.GetPageSize() != 30 {
				t.Fatalf("下游收到 page=%d pageSize=%d, want 2/30",
					client.got.GetPage(), client.got.GetPageSize())
			}
		})
	}
}

// 静态守卫：新增接口如果再写 `strconv.Atoi(c.Query("page"))` 这类吞错写法，
// 这里直接失败，避免分页边界再次退化。
func TestHandlersDoNotParsePaginationInline(t *testing.T) {
	paginationKeys := []string{`"page"`, `"page_size"`, `"pageSize"`, `"limit"`}
	parseCalls := []string{"strconv.Atoi(", "strconv.ParseInt("}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob 失败: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("没有扫描到 handler 源文件，守卫形同虚设")
	}

	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", file, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !containsAny(line, parseCalls) {
				continue
			}
			if !containsAny(line, paginationKeys) {
				continue
			}
			t.Errorf("%s:%d 直接解析分页参数：%s（请改用 parsePagination / parseLimit）",
				file, i+1, strings.TrimSpace(line))
		}
	}
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
