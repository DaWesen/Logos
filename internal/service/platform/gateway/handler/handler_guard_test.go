package handler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 静态守卫：这些写法一旦回归，参数校验就会静默失效，而且单元测试很难覆盖到
// 每一个新接口。用源码扫描把「约定」变成可执行的检查。
func TestHandlersDoNotSwallowStrconvErrors(t *testing.T) {
	files := mustGlobHandlers(t)

	forbidden := []string{
		// `userID, _ := strconv.ParseInt(...)`：解析失败变 0，等于查/写「用户 0」。
		", _ := strconv.",
		", _ = strconv.",
		// 直接把请求参数塞进 strconv，错误无处可去。
		"strconv.Atoi(c.Query(",
		"strconv.Atoi(c.Param(",
		"strconv.ParseInt(c.Query(",
		"strconv.ParseInt(c.Param(",
	}

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", file, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			// 注释里会引用旧写法作为反面例子，不能因此误报。
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, pattern := range forbidden {
				if strings.Contains(line, pattern) {
					t.Errorf("%s:%d 吞掉解析错误：%s（请改用 query_params.go 里的 helper）",
						file, i+1, trimmed)
				}
			}
		}
	}
}

// 身份只能来自认证中间件。允许 query_params 读取 user_id 的唯一位置是
// handler.go 的 resolveTargetUserID，那里会校验「只能查自己」。
func TestHandlersDoNotTrustUserIDQueryParam(t *testing.T) {
	files := mustGlobHandlers(t)

	for _, file := range files {
		if file == "handler.go" {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", file, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, `Query("user_id")`) || strings.Contains(line, `DefaultQuery("user_id"`) {
				t.Errorf("%s:%d 直接采信查询参数 user_id：%s（请改用 requireNumericUserID / resolveTargetUserID）",
					file, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// 身份提取只有两种合法形态：requireUserID（失败即 401），或者显式 ok 断言后
// 再判断非空。旧写法 `userID, _ := c.Get("user_id")` 会吞掉「key 不存在」，
// 接着 `userID.(string)` 直接 panic；更隐蔽的是 `req.UserId, _ = userID.(string)`
// 把身份变成空串——下游 DAO 的 `if userId != ""` 于是不加过滤，返回全库数据。
func TestHandlersRequireAuthenticatedUserID(t *testing.T) {
	files := mustGlobHandlers(t)

	// handler.go 是 helper 本身的实现处，requireUserID / withUserID 在这里合法。
	skip := map[string]bool{"handler.go": true}
	forbidden := []string{
		", _ := c.Get(",
		", _ = c.Get(",
	}
	// 裸断言：没有 ok 的 `xxx.(string)`。命中时要求同一行出现 `, ok :=`。
	bareAssertions := []string{"userID.(string)", "senderID.(string)"}

	for _, file := range files {
		if skip[file] {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", file, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, pattern := range forbidden {
				if strings.Contains(line, pattern) {
					t.Errorf("%s:%d 丢弃了 gin.Context 的 exists 结果：%s（请改用 requireUserID）",
						file, i+1, trimmed)
				}
			}
			for _, pattern := range bareAssertions {
				if strings.Contains(line, pattern) && !strings.Contains(line, ", ok :=") {
					t.Errorf("%s:%d 未检查类型断言结果：%s（请改用 requireUserID）",
						file, i+1, trimmed)
				}
			}
		}
	}
}

// 参数名错位（读 c.Param("type") 却挂在 :id 路由上）不会报错，只会操作错对象。
// 这里锁定索引接口的路由参数名，改路由时必须同步改 handler。
func TestIndexHandlersReadRouteParamID(t *testing.T) {
	data, err := os.ReadFile("handler_search.go")
	if err != nil {
		t.Fatalf("读取 handler_search.go 失败: %v", err)
	}
	source := string(data)

	if strings.Contains(source, `c.Param("type")`) {
		t.Error("handler_search.go 仍在读 :type 路由参数，但路由定义的是 :id")
	}
	if !strings.Contains(source, "parseIndexTypePath(c)") {
		t.Error("删除/刷新索引未使用 parseIndexTypePath")
	}
	if !strings.Contains(source, "parseIndexTypeQuery(c)") {
		t.Error("建索引未使用 parseIndexTypeQuery")
	}
}

// 登出接口的 all 参数没有「第三态」：旧写法
// `c.Query("all") == "true" || c.Query("all") == "1"` 会把 `?all=yes`
// 静默当成 false（只登出当前设备），安全语义刚好反了。
// 这里锁定必须走严格解析的 helper。
func TestLogoutParsesAllStrictly(t *testing.T) {
	data, err := os.ReadFile("handler_user.go")
	if err != nil {
		t.Fatalf("读取 handler_user.go 失败: %v", err)
	}
	source := string(data)

	if !strings.Contains(source, `parseOptionalBoolCompatQuery(c, "all")`) {
		t.Error("Logout 未使用 parseOptionalBoolCompatQuery 解析 all 参数")
	}
	for i, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.Contains(line, `Query("all") ==`) {
			t.Errorf("handler_user.go:%d 仍在用 == 比较 all 参数：%s", i+1, trimmed)
		}
	}
}

// 字符串枚举不能再写成 if/else 链：未命中的值会掉进 else 被当成
// 「不过滤」，过滤条件静默失效。新增枚举参数必须走 parseEnumQuery。
func TestHandlersDoNotInlineStringEnums(t *testing.T) {
	files := mustGlobHandlers(t)

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", file, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			// `if t := c.Query("type"); t == "builtin" {` 这类内联比较
			if strings.Contains(line, `:= c.Query(`) && strings.Contains(line, `==`) {
				t.Errorf("%s:%d 内联比较字符串枚举：%s（请改用 parseEnumQuery）",
					file, i+1, trimmed)
			}
		}
	}
}

func mustGlobHandlers(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob 失败: %v", err)
	}
	handlers := make([]string, 0, len(files))
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		handlers = append(handlers, file)
	}
	if len(handlers) == 0 {
		t.Fatal("没有扫描到 handler 源文件，守卫形同虚设")
	}
	return handlers
}
