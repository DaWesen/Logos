package mcp_server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type FileSystemTool struct{}

func (t *FileSystemTool) Name() string        { return "filesystem" }
func (t *FileSystemTool) Description() string { return "文件系统操作：读写文件、列出目录、获取文件信息" }
func (t *FileSystemTool) Type() int           { return 4 }
func (t *FileSystemTool) Parameters() []ToolParamDef {
	return []ToolParamDef{
		{Name: "action", Type: "string", Description: "操作类型: read/write/list/info/delete/mkdir", Required: true},
		{Name: "path", Type: "string", Description: "文件或目录路径", Required: true},
		{Name: "content", Type: "string", Description: "写入内容(write操作时使用)", Required: false},
		{Name: "recursive", Type: "bool", Description: "是否递归(list/delete操作时使用)", Required: false, DefaultValue: "false"},
	}
}

func (t *FileSystemTool) Execute(ctx context.Context, params map[string]string) (*ToolResult, error) {
	action := params["action"]
	path := params["path"]

	if action == "" {
		return &ToolResult{Content: "缺少action参数", IsError: true}, nil
	}
	if path == "" {
		return &ToolResult{Content: "缺少path参数", IsError: true}, nil
	}

	path = sanitizePath(path)

	switch action {
	case "read":
		return t.readFile(path)
	case "write":
		content := params["content"]
		return t.writeFile(path, content)
	case "list":
		recursive := params["recursive"] == "true"
		return t.listDir(path, recursive)
	case "info":
		return t.fileInfo(path)
	case "delete":
		return t.deleteFile(path)
	case "mkdir":
		return t.makeDir(path)
	default:
		return &ToolResult{
			Content:  fmt.Sprintf("不支持的操作: %s", action),
			IsError:  true,
			Metadata: map[string]string{"action": action},
		}, nil
	}
}

func sanitizePath(p string) string {
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		wd, _ := os.Getwd()
		p = filepath.Join(wd, p)
	}
	return p
}

func (t *FileSystemTool) readFile(path string) (*ToolResult, error) {
	info, err := os.Stat(path)
	if err != nil {
		return &ToolResult{
			Content:  fmt.Sprintf("文件不存在: %s", err.Error()),
			IsError:  true,
			Metadata: map[string]string{"path": path, "action": "read"},
		}, nil
	}

	if info.IsDir() {
		return &ToolResult{
			Content:  "路径是目录，不是文件",
			IsError:  true,
			Metadata: map[string]string{"path": path, "action": "read"},
		}, nil
	}

	if info.Size() > 10*1024*1024 {
		return &ToolResult{
			Content:  fmt.Sprintf("文件过大 (%d bytes)，最大支持10MB", info.Size()),
			IsError:  true,
			Metadata: map[string]string{"path": path, "action": "read", "size": fmt.Sprintf("%d", info.Size())},
		}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return &ToolResult{
			Content:  fmt.Sprintf("读取文件失败: %s", err.Error()),
			IsError:  true,
			Metadata: map[string]string{"path": path, "action": "read"},
		}, nil
	}

	return &ToolResult{
		Content: string(data),
		Metadata: map[string]string{
			"path": path,
			"size": fmt.Sprintf("%d", len(data)),
		},
	}, nil
}

func (t *FileSystemTool) writeFile(path string, content string) (*ToolResult, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return &ToolResult{
			Content:  fmt.Sprintf("创建目录失败: %s", err.Error()),
			IsError:  true,
			Metadata: map[string]string{"path": path, "action": "write"},
		}, nil
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return &ToolResult{
			Content:  fmt.Sprintf("写入文件失败: %s", err.Error()),
			IsError:  true,
			Metadata: map[string]string{"path": path, "action": "write"},
		}, nil
	}

	return &ToolResult{
		Content:  fmt.Sprintf("文件写入成功: %s (%d bytes)", path, len(content)),
		Metadata: map[string]string{"path": path, "action": "write", "size": fmt.Sprintf("%d", len(content))},
	}, nil
}

type fileEntry struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

func (t *FileSystemTool) listDir(path string, recursive bool) (*ToolResult, error) {
	info, err := os.Stat(path)
	if err != nil {
		return &ToolResult{
			Content:  fmt.Sprintf("路径不存在: %s", err.Error()),
			IsError:  true,
			Metadata: map[string]string{"path": path, "action": "list"},
		}, nil
	}

	if !info.IsDir() {
		return &ToolResult{
			Content:  "路径不是目录",
			IsError:  true,
			Metadata: map[string]string{"path": path, "action": "list"},
		}, nil
	}

	var entries []fileEntry

	if recursive {
		err = filepath.Walk(path, func(walkPath string, walkInfo os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			relPath, _ := filepath.Rel(path, walkPath)
			entries = append(entries, fileEntry{
				Name:    relPath,
				IsDir:   walkInfo.IsDir(),
				Size:    walkInfo.Size(),
				ModTime: walkInfo.ModTime().Format(time.RFC3339),
			})
			return nil
		})
	} else {
		file, err := os.Open(path)
		if err != nil {
			return &ToolResult{
				Content:  fmt.Sprintf("打开目录失败: %s", err.Error()),
				IsError:  true,
				Metadata: map[string]string{"path": path, "action": "list"},
			}, nil
		}
		defer file.Close()

		dirEntries, err := file.Readdir(0)
		if err != nil {
			return &ToolResult{
				Content:  fmt.Sprintf("读取目录失败: %s", err.Error()),
				IsError:  true,
				Metadata: map[string]string{"path": path, "action": "list"},
			}, nil
		}

		for _, e := range dirEntries {
			entries = append(entries, fileEntry{
				Name:    e.Name(),
				IsDir:   e.IsDir(),
				Size:    e.Size(),
				ModTime: e.ModTime().Format(time.RFC3339),
			})
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("目录: %s (共%d项)\n\n", path, len(entries)))
	for _, e := range entries {
		prefix := "📄"
		if e.IsDir {
			prefix = "📁"
		}
		sb.WriteString(fmt.Sprintf("%s %s", prefix, e.Name))
		if !e.IsDir {
			sb.WriteString(fmt.Sprintf(" (%s)", formatSize(e.Size)))
		}
		sb.WriteString("\n")
	}

	entriesJSON, _ := json.Marshal(entries)
	return &ToolResult{
		Content: sb.String(),
		Metadata: map[string]string{
			"path":       path,
			"action":     "list",
			"count":      fmt.Sprintf("%d", len(entries)),
			"entries":    string(entriesJSON),
			"recursive":  fmt.Sprintf("%v", recursive),
		},
	}, nil
}

func (t *FileSystemTool) fileInfo(path string) (*ToolResult, error) {
	info, err := os.Stat(path)
	if err != nil {
		return &ToolResult{
			Content:  fmt.Sprintf("文件不存在: %s", err.Error()),
			IsError:  true,
			Metadata: map[string]string{"path": path, "action": "info"},
		}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("路径: %s\n", path))
	sb.WriteString(fmt.Sprintf("类型: %s\n", map[bool]string{true: "目录", false: "文件"}[info.IsDir()]))
	sb.WriteString(fmt.Sprintf("大小: %s\n", formatSize(info.Size())))
	sb.WriteString(fmt.Sprintf("修改时间: %s\n", info.ModTime().Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("权限: %s\n", info.Mode().String()))

	return &ToolResult{
		Content: sb.String(),
		Metadata: map[string]string{
			"path":     path,
			"action":   "info",
			"is_dir":   fmt.Sprintf("%v", info.IsDir()),
			"size":     fmt.Sprintf("%d", info.Size()),
			"mod_time": info.ModTime().Format(time.RFC3339),
			"mode":     info.Mode().String(),
		},
	}, nil
}

func (t *FileSystemTool) deleteFile(path string) (*ToolResult, error) {
	info, err := os.Stat(path)
	if err != nil {
		return &ToolResult{
			Content:  fmt.Sprintf("文件不存在: %s", err.Error()),
			IsError:  true,
			Metadata: map[string]string{"path": path, "action": "delete"},
		}, nil
	}

	if info.IsDir() {
		if err := os.RemoveAll(path); err != nil {
			return &ToolResult{
				Content:  fmt.Sprintf("删除目录失败: %s", err.Error()),
				IsError:  true,
				Metadata: map[string]string{"path": path, "action": "delete"},
			}, nil
		}
	} else {
		if err := os.Remove(path); err != nil {
			return &ToolResult{
				Content:  fmt.Sprintf("删除文件失败: %s", err.Error()),
				IsError:  true,
				Metadata: map[string]string{"path": path, "action": "delete"},
			}, nil
		}
	}

	return &ToolResult{
		Content:  fmt.Sprintf("删除成功: %s", path),
		Metadata: map[string]string{"path": path, "action": "delete"},
	}, nil
}

func (t *FileSystemTool) makeDir(path string) (*ToolResult, error) {
	if err := os.MkdirAll(path, 0755); err != nil {
		return &ToolResult{
			Content:  fmt.Sprintf("创建目录失败: %s", err.Error()),
			IsError:  true,
			Metadata: map[string]string{"path": path, "action": "mkdir"},
		}, nil
	}

	return &ToolResult{
		Content:  fmt.Sprintf("目录创建成功: %s", path),
		Metadata: map[string]string{"path": path, "action": "mkdir"},
	}, nil
}

func formatSize(size int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case size >= GB:
		return fmt.Sprintf("%.2f GB", float64(size)/float64(GB))
	case size >= MB:
		return fmt.Sprintf("%.2f MB", float64(size)/float64(MB))
	case size >= KB:
		return fmt.Sprintf("%.2f KB", float64(size)/float64(KB))
	default:
		return fmt.Sprintf("%d B", size)
	}
}
