package cache

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAllRedisOpsHaveBudget 防止新增 Redis 方法绕过统一预算。
//
// 预算只在部分方法上生效等于没有预算：Redis 变慢时，未包装的调用会一直挂到
// 调用方的 gRPC/HTTP 超时（常见 30s），把 goroutine 与连接池一起占满。
// 这里用静态检查兜底，新增方法时必须显式选择 runOp/runErr。
func TestAllRedisOpsHaveBudget(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试源文件")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "redis.go"))
	if err != nil {
		t.Fatalf("读取 redis.go 失败: %v", err)
	}

	chunks := strings.Split(string(src), "\nfunc (c *RedisCache) ")
	if len(chunks) < 2 {
		t.Fatal("未解析到 RedisCache 方法")
	}

	// Close/RawClient 直接接触底层连接，不需要也不能套业务超时。
	exempt := map[string]bool{
		"Close(":     true,
		"RawClient(": true,
	}

	for _, chunk := range chunks[1:] {
		name := chunk
		if idx := strings.Index(name, "("); idx >= 0 {
			name = name[:idx+1]
		}
		if exempt[name] {
			continue
		}
		if !strings.Contains(chunk, "c.client.") {
			continue
		}
		if strings.Contains(chunk, "runOp(") || strings.Contains(chunk, "runErr(") {
			continue
		}
		t.Errorf("方法 %s 直接调用 c.client 且未套 runOp/runErr，Redis 变慢时会拖到调用方超时", name)
	}
}
