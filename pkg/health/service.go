package health

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ServiceRegistry 是各服务入口的便捷构造器。
//
// 服务 main 里真正需要的只是「注册哪几个依赖」，不应该每个入口都重复
// 写一遍超时、缓存、nil 处理。这里把默认值收在一处，保证 20 个服务的
// 探测语义一致；需要特殊配置时仍可直接用 New/Register。
func ServiceRegistry(db *gorm.DB, optional ...CheckFunc) *Registry {
	r := New(WithTimeout(3*time.Second), WithCacheTTL(2*time.Second))
	if db != nil {
		r.Register("postgres", GormPostgres(db))
	}
	for i, fn := range optional {
		if fn != nil {
			r.RegisterOptional(fmt.Sprintf("optional_%d", i+1), fn)
		}
	}
	return r
}

// Noop 永远健康，用于确实没有任何外部依赖的服务。
// 显式注册而不是留空：日志里能看出「这个服务确实没有依赖」，
// 而不是「忘了配就绪检查」。
func Noop(context.Context) error { return nil }
