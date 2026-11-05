package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pbCommon "Logos/proto_gen/common"

	"github.com/gin-gonic/gin"
)

func TestMapStatusCode(t *testing.T) {
	cases := []struct {
		name string
		code int32
		want int
	}{
		{"业务成功码", 0, http.StatusOK},
		{"未分类业务错误", 1, http.StatusInternalServerError},
		{"合法 4xx", 400, http.StatusBadRequest},
		{"合法 5xx", 503, http.StatusServiceUnavailable},
		{"下界", 100, 100},
		{"上界", 599, 599},
		{"0 与 100 之间", 7, http.StatusInternalServerError},
		{"超出 599", 600, http.StatusInternalServerError},
		{"负数", -1, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapStatusCode(tc.code); got != tc.want {
				t.Fatalf("mapStatusCode(%d) = %d, want %d", tc.code, got, tc.want)
			}
		})
	}
}

func TestMapBaseRespStatusCode_NilIsSuccess(t *testing.T) {
	if got := mapBaseRespStatusCode(nil); got != http.StatusOK {
		t.Fatalf("nil BaseResp 应视为成功，实际 %d", got)
	}
}

// respondProtoCode 是「下游 int32 code → HTTP」的唯一出口：
// 状态行与响应体里的 code 必须一致，否则前端按 body.code 判断成功、
// 监控按状态行统计失败率，两边会得到互相矛盾的结论。
func TestRespondProtoCode_KeepsStatusAndBodyInSync(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name     string
		code     int32
		wantHTTP int
	}{
		{"成功", 0, http.StatusOK},
		{"内部错误", 1, http.StatusInternalServerError},
		{"限流", 429, http.StatusTooManyRequests},
		{"非法业务码", 7, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

			respondProtoCode(c, tc.code, "downstream message", gin.H{"ok": true})

			if rec.Code != tc.wantHTTP {
				t.Fatalf("HTTP 状态 = %d, want %d", rec.Code, tc.wantHTTP)
			}
			var body struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("响应体不是合法 JSON: %v", err)
			}
			if body.Code != tc.wantHTTP {
				t.Fatalf("body.code = %d, want %d（必须与状态行一致）", body.Code, tc.wantHTTP)
			}
			if body.Message != "downstream message" {
				t.Fatalf("body.message = %q，下游文案被改写", body.Message)
			}
		})
	}
}

// 静态守卫：handler 目录里不允许再出现「把下游 code 直接当 HTTP 状态码」
// 的写法。新增接口如果绕过 mapStatusCode / mapBaseRespStatusCode /
// respondProtoCode，这里会直接失败，而不是等到线上看到 0 或 7 的状态行。
func TestHandlersDoNotWriteRawDownstreamCode(t *testing.T) {
	forbidden := []string{
		"toHTTPCode(",
		"c.JSON(int(",
		"c.JSON(resp.Code",
		"statusCode = int(resp.Code)",
		"statusCode = int(resp.BaseResp.StatusCode)",
		"statusCode = int(resp.StatusCode)",
	}

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
			for _, bad := range forbidden {
				if strings.Contains(line, bad) {
					t.Errorf("%s:%d 出现 %q：下游 code 必须先经 mapStatusCode 映射",
						file, i+1, bad)
				}
			}
		}
	}
}

// BaseResp 版本也要覆盖：nil 视为成功，非 0 错误码走同一张映射表。
func TestMapBaseRespStatusCode(t *testing.T) {
	cases := []struct {
		name string
		resp *pbCommon.BaseResp
		want int
	}{
		{"成功", &pbCommon.BaseResp{StatusCode: 0}, http.StatusOK},
		{"通用错误", &pbCommon.BaseResp{StatusCode: 1}, http.StatusInternalServerError},
		{"未找到", &pbCommon.BaseResp{StatusCode: 404}, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapBaseRespStatusCode(tc.resp); got != tc.want {
				t.Fatalf("mapBaseRespStatusCode(%d) = %d, want %d", tc.resp.StatusCode, got, tc.want)
			}
		})
	}
}
