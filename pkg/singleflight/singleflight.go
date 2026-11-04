// Package singleflight 合并同一进程内对同一个 key 的并发回源请求。
//
// 与 golang.org/x/sync/singleflight 的差异：
//   - 每个等待者有自己的 ctx：客户端断开时等待者立刻返回 ctx.Err()，
//     而不是继续阻塞在一个与自己无关的回源上；
//   - 回源本身用 context.WithoutCancel + 独立超时执行：发起者断开不会连带
//     取消其他等待者的数据，但也不能无限期占用 goroutine；
//   - fn panic 会被 recover 成错误，避免一个坏回源打崩整个进程；
//   - 命中合并时返回 shared=true，便于调用方打点区分「真实回源」与「搭便车」。
package singleflight

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrTimeout 回源超过 Group 的超时上限。
var ErrTimeout = errors.New("singleflight: fill timeout")

type call[T any] struct {
	done    chan struct{}
	val     T
	err     error
	waiters int
}

// Group 是带类型的合并器：同一个 key 同一时刻只有一个回源在执行。
type Group[T any] struct {
	mu      sync.Mutex
	calls   map[string]*call[T]
	timeout time.Duration
}

// NewGroup 创建合并器。timeout<=0 表示回源不设超时。
func NewGroup[T any](timeout time.Duration) *Group[T] {
	return &Group[T]{
		calls:   make(map[string]*call[T]),
		timeout: timeout,
	}
}

// Do 执行 fn 并把并发调用合并成一次。
// 返回的 shared 表示本次结果来自别人的回源（自己没有真正执行 fn）。
func (g *Group[T]) Do(ctx context.Context, key string, fn func(context.Context) (T, error)) (T, bool, error) {
	g.mu.Lock()
	if c, ok := g.calls[key]; ok {
		c.waiters++
		g.mu.Unlock()
		select {
		case <-c.done:
			return c.val, true, c.err
		case <-ctx.Done():
			var zero T
			return zero, true, ctx.Err()
		}
	}

	c := &call[T]{done: make(chan struct{})}
	g.calls[key] = c
	g.mu.Unlock()

	// 回源与调用方生命周期解耦：某个客户端断开不应让其他等待者一起失败。
	fillCtx := context.WithoutCancel(ctx)
	var cancel context.CancelFunc = func() {}
	if g.timeout > 0 {
		fillCtx, cancel = context.WithTimeout(fillCtx, g.timeout)
	}

	go func() {
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				var zero T
				c.val = zero
				c.err = fmt.Errorf("singleflight: fill panic: %v", r)
			}
			close(c.done)
			g.mu.Lock()
			delete(g.calls, key)
			g.mu.Unlock()
		}()

		c.val, c.err = fn(fillCtx)
	}()

	select {
	case <-c.done:
		return c.val, false, c.err
	case <-ctx.Done():
		var zero T
		return zero, false, ctx.Err()
	}
}

// Waiters 返回当前 key 上正在等待的调用数（不含发起者），仅用于测试与诊断。
func (g *Group[T]) Waiters(key string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if c, ok := g.calls[key]; ok {
		return c.waiters
	}
	return 0
}
