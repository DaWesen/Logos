package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"Logos/internal/service/ai/process/model"
	"Logos/pkg/pagination"
)

func newQueryContext(t *testing.T, rawQuery string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	req := httptest.NewRequest(http.MethodGet, "/api/ai/process/documents?"+rawQuery, nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	return c, w
}

func TestParseDocumentListQuery_Defaults(t *testing.T) {
	c, _ := newQueryContext(t, "")

	page, pageSize, status, ok := parseDocumentListQuery(c)
	if !ok {
		t.Fatal("缺省参数应当合法")
	}
	if page != pagination.DefaultPage || pageSize != defaultDocumentPageSize {
		t.Fatalf("缺省分页 = (%d,%d)，期望 (%d,%d)",
			page, pageSize, pagination.DefaultPage, defaultDocumentPageSize)
	}
	if status != nil {
		t.Fatalf("缺省 status 应为 nil（不过滤），实际 %v", *status)
	}
}

// TestParseDocumentListQuery_RejectsInvalidPagination 回归：
// 旧写法 `page, _ := strconv.Atoi(...)` 把 `?page=abc` 吞成 0，
// DAO 归一化后静默返回第一页——调用方以为翻页成功，实际停留在首页。
func TestParseDocumentListQuery_RejectsInvalidPagination(t *testing.T) {
	cases := []string{
		"page=abc",
		"page=0",
		"page=-3",
		"page=1.5",
		"page_size=abc",
		"page_size=0",
		"page_size=-10",
		"page=9999999999999999999999",
	}
	for _, query := range cases {
		t.Run(query, func(t *testing.T) {
			c, w := newQueryContext(t, query)
			if _, _, _, ok := parseDocumentListQuery(c); ok {
				t.Fatalf("%s 应当被拒绝", query)
			}
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d，期望 400", w.Code)
			}
		})
	}
}

// TestParseDocumentListQuery_ClampsPageSize 页大小超限时收敛，
// 并且调用方拿到的就是实际生效的值，而不是请求里的 1e9。
func TestParseDocumentListQuery_ClampsPageSize(t *testing.T) {
	c, _ := newQueryContext(t, "page=2&page_size=1000000000")

	page, pageSize, _, ok := parseDocumentListQuery(c)
	if !ok {
		t.Fatal("超限 page_size 应当被收敛而不是拒绝")
	}
	if page != 2 || pageSize != pagination.MaxPageSize {
		t.Fatalf("归一化结果 = (%d,%d)，期望 (2,%d)", page, pageSize, pagination.MaxPageSize)
	}
}

func TestParseDocumentListQuery_StatusWhitelist(t *testing.T) {
	cases := []struct {
		query  string
		want   int
		reject bool
	}{
		{"status=1", model.DocumentStatusPending, false},
		{"status=2", model.DocumentStatusProcessing, false},
		{"status=3", model.DocumentStatusCompleted, false},
		{"status=4", model.DocumentStatusFailed, false},
		// 旧实现把解析失败静默当成「不过滤」，于是返回全部状态。
		{"status=abc", 0, true},
		{"status=", 0, false},
		{"status=0", 0, true},
		{"status=5", 0, true},
		{"status=-1", 0, true},
		{"status=3.0", 0, true},
	}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			ctx, w := newQueryContext(t, c.query)
			_, _, status, ok := parseDocumentListQuery(ctx)
			if c.reject {
				if ok {
					t.Fatalf("%s 应当被拒绝", c.query)
				}
				if w.Code != http.StatusBadRequest {
					t.Fatalf("status = %d，期望 400", w.Code)
				}
				return
			}
			if !ok {
				t.Fatalf("%s 应当合法", c.query)
			}
			if c.want == 0 {
				if status != nil {
					t.Fatalf("%s 应为 nil（不过滤），实际 %v", c.query, *status)
				}
				return
			}
			if status == nil || *status != c.want {
				t.Fatalf("%s 解析成 %v，期望 %d", c.query, status, c.want)
			}
		})
	}
}

// 静态守卫：process 服务的 handler 也必须走统一的参数解析，
// 不能再出现 `, _ := strconv.` 这种把错误交给零值的写法。
func TestProcessHandlersDoNotSwallowStrconvErrors(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取 handler 目录失败: %v", err)
	}

	forbidden := []string{
		", _ := strconv.",
		", _ = strconv.",
		"dto, _ := h.processService.GetDocumentDTO",
		"strconv.Atoi(c.Query(",
		"strconv.Atoi(c.Param(",
		"strconv.ParseInt(c.Query(",
		"strconv.ParseInt(c.Param(",
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		if strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", entry.Name()))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", entry.Name(), err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, pattern := range forbidden {
				if strings.Contains(line, pattern) {
					t.Errorf("%s:%d 吞掉解析错误：%s", entry.Name(), i+1, trimmed)
				}
			}
		}
	}
}
