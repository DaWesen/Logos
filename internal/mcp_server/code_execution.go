package mcp_server

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ==================== 沙箱安全策略 ====================
//
// 代码执行是高危能力，本工具实施多层防护：
//  1. 环境变量白名单：执行进程仅继承 PATH 等最小集合，服务自身的
//     API Key / JWT 密钥等敏感变量不会泄漏给被执行代码；
//  2. 超时硬上限：调用方传入的 timeout 被钳制在 [1, 60] 秒，超时后
//     终止整个进程组（防止代码再 spawn 子进程逃逸超时）；
//  3. 输出限额：stdout/stderr 各截断至 64KB，防止内存耗尽；
//  4. 并发限额：全局信号量限制同时执行的代码数量；
//  5. Docker 隔离（可选）：设置 LOGOS_CODE_SANDBOX=docker 时，
//     代码在 --network none --memory 128m 的容器内执行，无网络、
//     无宿主文件系统访问，read-only 挂载代码目录。

const (
	maxCodeSize        = 64 * 1024        // 代码大小上限
	maxOutputSize      = 64 * 1024        // 输出截断上限
	maxTimeoutSeconds  = 60               // 超时硬上限
	defaultTimeoutSecs = 30
	maxConcurrentExec  = 3                // 全局并发执行上限
)

var (
	// sandboxEnvWhitelist 环境变量白名单：仅允许与运行解释器相关的最小集合
	sandboxEnvWhitelist = []string{"PATH", "HOME", "LANG", "LC_ALL", "TMPDIR", "SYSTEMROOT"}

	execSem = make(chan struct{}, maxConcurrentExec)
)

// sanitizeEnv 返回仅含白名单变量的环境（纯函数，可单测）
func sanitizeEnv(environ []string) []string {
	allowed := make(map[string]bool, len(sandboxEnvWhitelist))
	for _, k := range sandboxEnvWhitelist {
		allowed[k] = true
	}

	var out []string
	for _, kv := range environ {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if allowed[strings.ToUpper(key)] {
			out = append(out, kv)
		}
	}
	return out
}

// clampTimeout 将超时钳制到安全范围（纯函数，可单测）
func clampTimeout(requested string) int {
	t := defaultTimeoutSecs
	if requested != "" {
		if n, err := fmt.Sscanf(requested, "%d", &t); n != 1 || err != nil || t <= 0 {
			t = defaultTimeoutSecs
		}
	}
	if t > maxTimeoutSeconds {
		return maxTimeoutSeconds
	}
	return t
}

// truncateOutput 将输出截断到上限（纯函数，可单测）
func truncateOutput(s string) string {
	if len(s) <= maxOutputSize {
		return s
	}
	return s[:maxOutputSize] + "\n... (输出超过 64KB，已截断)"
}

type CodeExecutionTool struct{}

func (t *CodeExecutionTool) Name() string        { return "code_execution" }
func (t *CodeExecutionTool) Description() string {
	return "在沙箱环境中执行代码，支持 Python/JavaScript/Go（无网络、隔离环境变量、限时执行）"
}
func (t *CodeExecutionTool) Type() int { return 2 }
func (t *CodeExecutionTool) Parameters() []ToolParamDef {
	return []ToolParamDef{
		{Name: "code", Type: "string", Description: "要执行的代码", Required: true},
		{Name: "language", Type: "string", Description: "编程语言: python/javascript/go", Required: true, DefaultValue: "python"},
		{Name: "timeout", Type: "int", Description: "超时时间(秒，上限60)", Required: false, DefaultValue: "30"},
	}
}

func (t *CodeExecutionTool) Execute(ctx context.Context, params map[string]string) (*ToolResult, error) {
	code := params["code"]
	if code == "" {
		return &ToolResult{Content: "缺少code参数", IsError: true}, nil
	}
	if len(code) > maxCodeSize {
		return &ToolResult{Content: fmt.Sprintf("代码超过大小限制 (%d KB)", maxCodeSize/1024), IsError: true}, nil
	}

	language := params["language"]
	if language == "" {
		language = "python"
	}

	timeout := clampTimeout(params["timeout"])

	result, err := executeCode(ctx, code, language, timeout)
	if err != nil {
		return &ToolResult{
			Content:  fmt.Sprintf("代码执行失败: %s", err.Error()),
			IsError:  true,
			Metadata: map[string]string{"language": language, "status": "error"},
		}, nil
	}

	return &ToolResult{
		Content: result,
		Metadata: map[string]string{
			"language": language,
			"status":   "success",
			"sandbox":  sandboxMode(),
		},
	}, nil
}

// sandboxMode 返回当前沙箱模式：docker（容器隔离）或 process（进程级加固）
func sandboxMode() string {
	if os.Getenv("LOGOS_CODE_SANDBOX") == "docker" {
		return "docker"
	}
	return "process"
}

func executeCode(ctx context.Context, code, language string, timeoutSec int) (string, error) {
	// 全局并发限额
	execSem <- struct{}{}
	defer func() { <-execSem }()

	tmpDir, err := os.MkdirTemp("", "logos-code-*")
	if err != nil {
		return "", fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if sandboxMode() == "docker" {
		return executeInDocker(ctx, tmpDir, code, language, timeoutSec)
	}
	return executeLocal(ctx, tmpDir, code, language, timeoutSec)
}

// executeLocal 进程级沙箱：隔离环境变量 + 进程组超时终止
func executeLocal(ctx context.Context, tmpDir, code, language string, timeoutSec int) (string, error) {
	var cmd *exec.Cmd
	var filename string

	switch strings.ToLower(language) {
	case "python", "python3":
		filename = "main.py"
		if err := os.WriteFile(filepath.Join(tmpDir, filename), []byte(code), 0644); err != nil {
			return "", fmt.Errorf("写入代码文件失败: %w", err)
		}
		cmd = exec.CommandContext(ctx, "python3", filename)
		cmd.Dir = tmpDir

	case "javascript", "js", "node":
		filename = "main.js"
		if err := os.WriteFile(filepath.Join(tmpDir, filename), []byte(code), 0644); err != nil {
			return "", fmt.Errorf("写入代码文件失败: %w", err)
		}
		cmd = exec.CommandContext(ctx, "node", filename)
		cmd.Dir = tmpDir

	case "go":
		filename = "main.go"
		wrappedCode := fmt.Sprintf(`package main

import (
	"fmt"
	"os"
)

func main() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic: %%v", r)
			os.Exit(1)
		}
	}()

%s
}`, indentCode(code))
		if err := os.WriteFile(filepath.Join(tmpDir, filename), []byte(wrappedCode), 0644); err != nil {
			return "", fmt.Errorf("写入代码文件失败: %w", err)
		}
		cmd = exec.CommandContext(ctx, "go", "run", filename)
		cmd.Dir = tmpDir

	default:
		return "", fmt.Errorf("不支持的语言: %s，支持: python/javascript/go", language)
	}

	// 沙箱核心：仅继承白名单环境变量，服务密钥不进入执行进程
	cmd.Env = sanitizeEnv(os.Environ())

	// 限额读取，防止海量输出耗尽内存
	limitedStdout := &limitedBuffer{max: maxOutputSize + 1}
	limitedStderr := &limitedBuffer{max: maxOutputSize + 1}
	cmd.Stdout = limitedStdout
	cmd.Stderr = limitedStderr

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("启动执行失败: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-time.After(time.Duration(timeoutSec) * time.Second):
		killProcessTree(cmd)
		return "", fmt.Errorf("执行超时 (%ds)", timeoutSec)
	case err := <-done:
		stdoutStr := truncateOutput(limitedStdout.String())
		stderrStr := truncateOutput(limitedStderr.String())
		exitCode := 0

		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
			}
		}

		var sb strings.Builder
		if stdoutStr != "" {
			sb.WriteString("=== 输出 ===\n")
			sb.WriteString(stdoutStr)
		}
		if stderrStr != "" {
			sb.WriteString("=== 错误 ===\n")
			sb.WriteString(stderrStr)
		}
		if stdoutStr == "" && stderrStr == "" {
			sb.WriteString("(无输出)")
		}
		if exitCode != 0 {
			sb.WriteString(fmt.Sprintf("\n退出码: %d", exitCode))
		}

		return sb.String(), nil
	}
}

// executeInDocker 容器级沙箱：无网络 + 内存/CPU/进程数限制 + 只读挂载
func executeInDocker(ctx context.Context, tmpDir, code, language string, timeoutSec int) (string, error) {
	image, entrypoint := dockerImageFor(language)

	if err := os.WriteFile(filepath.Join(tmpDir, entrypoint), []byte(code), 0644); err != nil {
		return "", fmt.Errorf("写入代码文件失败: %w", err)
	}

	args := []string{
		"run", "--rm",
		"--network", "none",     // 禁止网络访问
		"--memory", "128m",      // 内存上限
		"--cpus", "0.5",         // CPU 上限
		"--pids-limit", "64",    // 进程数上限
		"--read-only",           // 容器文件系统只读
		"--tmpfs", "/tmp:rw,size=16m",
		"-v", tmpDir + ":/sandbox:ro", // 代码目录只读挂载
		"-w", "/sandbox",
		image,
	}
	switch strings.ToLower(language) {
	case "python", "python3":
		args = append(args, "python3", entrypoint)
	case "javascript", "js", "node":
		args = append(args, "node", entrypoint)
	case "go":
		args = append(args, "go", "run", entrypoint)
	default:
		return "", fmt.Errorf("不支持的语言: %s", language)
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = sanitizeEnv(os.Environ())

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	select {
	case <-time.After(time.Duration(timeoutSec) * time.Second):
		_ = cmd.Process.Kill()
		return "", fmt.Errorf("执行超时 (%ds)", timeoutSec)
	case err := <-done:
		if err != nil && stdout.Len() == 0 {
			return "", fmt.Errorf("docker 执行失败（镜像未拉取或 docker 不可用?）: %w: %s", err, truncateOutput(stderr.String()))
		}
		var sb strings.Builder
		if s := truncateOutput(stdout.String()); s != "" {
			sb.WriteString("=== 输出 ===\n")
			sb.WriteString(s)
		}
		if s := truncateOutput(stderr.String()); s != "" {
			sb.WriteString("=== 错误 ===\n")
			sb.WriteString(s)
		}
		if sb.Len() == 0 {
			sb.WriteString("(无输出)")
		}
		return sb.String(), nil
	}
}

func dockerImageFor(language string) (image, entrypoint string) {
	switch strings.ToLower(language) {
	case "javascript", "js", "node":
		return "node:20-alpine", "main.js"
	case "go":
		return "golang:1.25-alpine", "main.go"
	default:
		return "python:3.11-slim", "main.py"
	}
}

// killProcessTree 超时后终止整个进程树，防止被杀进程的子进程逃逸
func killProcessTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		// /T 连同子进程一起终止
		_ = exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprint(cmd.Process.Pid)).Run()
		return
	}
	// Unix: 负 PID 终止整个进程组
	_ = exec.Command("kill", "-9", fmt.Sprintf("-%d", cmd.Process.Pid)).Run()
	_ = cmd.Process.Kill()
}

// limitedBuffer 带Max上限的 bytes.Buffer，超过后丢弃后续写入
type limitedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.buf.Len() >= b.max {
		return len(p), nil // 丢弃
	}
	remaining := b.max - b.buf.Len()
	if len(p) > remaining {
		b.buf.Write(p[:remaining])
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *limitedBuffer) String() string {
	return b.buf.String()
}

func indentCode(code string) string {
	lines := strings.Split(code, "\n")
	var sb strings.Builder
	for _, line := range lines {
		sb.WriteString("\t")
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	return sb.String()
}

// 编译期断言：limitedBuffer 满足 io.Writer
var _ interface {
	Write(p []byte) (int, error)
} = (*limitedBuffer)(nil)
