package mcp_server

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type CodeExecutionTool struct{}

func (t *CodeExecutionTool) Name() string        { return "code_execution" }
func (t *CodeExecutionTool) Description() string { return "在沙箱环境中执行代码，支持 Python/JavaScript/Go" }
func (t *CodeExecutionTool) Type() int           { return 2 }
func (t *CodeExecutionTool) Parameters() []ToolParamDef {
	return []ToolParamDef{
		{Name: "code", Type: "string", Description: "要执行的代码", Required: true},
		{Name: "language", Type: "string", Description: "编程语言: python/javascript/go", Required: true, DefaultValue: "python"},
		{Name: "timeout", Type: "int", Description: "超时时间(秒)", Required: false, DefaultValue: "30"},
	}
}

func (t *CodeExecutionTool) Execute(ctx context.Context, params map[string]string) (*ToolResult, error) {
	code := params["code"]
	if code == "" {
		return &ToolResult{Content: "缺少code参数", IsError: true}, nil
	}

	language := params["language"]
	if language == "" {
		language = "python"
	}

	timeout := 30
	if t := params["timeout"]; t != "" {
		fmt.Sscanf(t, "%d", &timeout)
	}

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
		},
	}, nil
}

func executeCode(ctx context.Context, code, language string, timeoutSec int) (string, error) {
	tmpDir, err := os.MkdirTemp("", "logos-code-*")
	if err != nil {
		return "", fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tmpDir)

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

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	select {
	case <-time.After(time.Duration(timeoutSec) * time.Second):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return "", fmt.Errorf("执行超时 (%ds)", timeoutSec)
	case err := <-done:
		stdoutStr := stdout.String()
		stderrStr := stderr.String()
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
