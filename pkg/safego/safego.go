// Package safego 为后台 goroutine 提供 panic 隔离。
//
// net/http 的 recovery 中间件只覆盖请求 goroutine；自行 go 出去的任务一旦
// panic，未恢复的 panic 会直接终止整个进程，而不是只让某次请求失败。
// 消息审核、Bot 回复、离线推送、保留期清理都属于「失败可以降级、但不能
// 拖垮进程」的后台任务，因此需要各自兜底。
//
// 隔离 panic 不等于静默吞掉：PanicError 保留 panic 值与堆栈，Go 会写结构化
// 日志，同时累加 logos.safego.panic.count 指标，保证问题仍然可观测。
package safego

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"

	"Logos/pkg/logger"
	"Logos/pkg/obs"
)

// PanicError 描述一次被恢复的后台 panic。
// 保留原始 panic 值与堆栈，调用方可以用 errors.As 取回后做二次处理。
type PanicError struct {
	Task  string
	Value any
	Stack []byte
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("background task %q panicked: %v", e.Task, e.Value)
}

// Run 同步执行 fn，并把 panic 转成 *PanicError。
//
// 适用于「单次失败后还要继续跑」的循环体：调用方拿到 error 后记录日志即可，
// 循环本身不会被 panic 打断。每次恢复都会计入后台 panic 指标。
func Run(task string, fn func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &PanicError{
				Task:  task,
				Value: r,
				Stack: debug.Stack(),
			}
			obs.RecordBackgroundPanic(context.Background(), task)
		}
	}()

	fn()
	return nil
}

// Go 启动一个带 panic 隔离的后台任务；panic 会被恢复、记录日志并计入指标。
//
// task 同时作为日志字段和指标标签，必须是固定、低基数的名称
// （例如 "chat.bot_invoke"），不要把用户 ID、Bot ID 这类无界值拼进去，
// 否则指标基数会随数据量膨胀。
func Go(task string, fn func()) {
	go func() {
		err := Run(task, fn)
		if err == nil {
			return
		}

		var panicErr *PanicError
		stack := ""
		if errors.As(err, &panicErr) {
			stack = string(panicErr.Stack)
		}
		logger.Error("后台任务 panic 已恢复，进程继续运行",
			logger.StringField("task", task),
			logger.ErrorField(err),
			logger.StringField("stack", stack),
		)
	}()
}
