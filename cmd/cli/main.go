package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

var (
	baseURL    string
	wsURL      string
	authToken  string
	userID     string

	styleTitle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")).MarginBottom(1)
	styleSent     = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).MarginLeft(2)
	styleReceived = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).MarginLeft(2)
	styleSystem   = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).MarginLeft(2)
	styleError    = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).MarginLeft(2)
	styleBot      = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).MarginLeft(2)
	styleDivider  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

type chatMsg struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	SenderID  string    `json:"sender_id,omitempty"`
}

type model struct {
	messages   []chatMsg
	input      textinput.Model
	chatID     string
	chatType   int
	ready      bool
	viewport   string
	err        error
	renderer   *glamour.TermRenderer
	scrollPos  int
}

func initialModel() model {
	ti := textinput.New()
	ti.Placeholder = "输入消息... (/help 查看命令)"
	ti.Focus()
	ti.CharLimit = 500
	ti.Width = 60

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(80),
	)

	return model{
		input:    ti,
		chatType: 1,
		renderer: renderer,
	}
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEnter:
			val := m.input.Value()
			if val == "" {
				return m, nil
			}
			m.input.SetValue("")

			if strings.HasPrefix(val, "/") {
				return m.handleCommand(val)
			}

			return m.sendMessage(val)
		}
	}

	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) handleCommand(cmd string) (tea.Model, tea.Cmd) {
	parts := strings.SplitN(cmd, " ", 2)
	command := parts[0]
	arg := ""
	if len(parts) > 1 {
		arg = parts[1]
	}

	switch command {
	case "/help":
		m.messages = append(m.messages, chatMsg{
			Role:    "system",
			Content: "可用命令:\n/help - 显示帮助\n/chat <chatID> - 切换聊天\n/group <groupID> - 切换群聊\n/bot <botID> <message> - 与Bot对话\n/summary - 总结当前聊天\n/quit - 退出",
		})
	case "/chat":
		if arg == "" {
			m.messages = append(m.messages, chatMsg{Role: "system", Content: "用法: /chat <chatID>"})
		} else {
			m.chatID = arg
			m.chatType = 1
			m.messages = append(m.messages, chatMsg{Role: "system", Content: fmt.Sprintf("已切换到私聊: %s", arg)})
			m.loadHistory()
		}
	case "/group":
		if arg == "" {
			m.messages = append(m.messages, chatMsg{Role: "system", Content: "用法: /group <groupID>"})
		} else {
			m.chatID = arg
			m.chatType = 2
			m.messages = append(m.messages, chatMsg{Role: "system", Content: fmt.Sprintf("已切换到群聊: %s", arg)})
			m.loadHistory()
		}
	case "/bot":
		botParts := strings.SplitN(arg, " ", 2)
		if len(botParts) < 2 {
			m.messages = append(m.messages, chatMsg{Role: "system", Content: "用法: /bot <botID> <message>"})
		} else {
			return m.sendBotMessage(botParts[0], botParts[1])
		}
	case "/summary":
		return m.summarizeChat()
	case "/quit", "/exit":
		return m, tea.Quit
	default:
		m.messages = append(m.messages, chatMsg{Role: "system", Content: "未知命令，输入 /help 查看帮助"})
	}
	return m, nil
}

func (m model) sendMessage(content string) (tea.Model, tea.Cmd) {
	if m.chatID == "" {
		m.messages = append(m.messages, chatMsg{Role: "system", Content: "请先使用 /chat <chatID> 或 /group <groupID> 选择聊天"})
		return m, nil
	}

	m.messages = append(m.messages, chatMsg{
		Role:    "sent",
		Content: content,
	})

	payload := map[string]interface{}{
		"chat_id":      m.chatID,
		"content":      content,
		"chat_type":    m.chatType,
		"message_type": 1,
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", baseURL+"/api/v1/chat/message", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+authToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		m.messages = append(m.messages, chatMsg{Role: "error", Content: "发送失败: " + err.Error()})
		return m, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		m.messages = append(m.messages, chatMsg{Role: "error", Content: fmt.Sprintf("发送失败 (%d): %s", resp.StatusCode, string(respBody))})
		return m, nil
	}

	return m, nil
}

func (m model) sendBotMessage(botID, content string) (tea.Model, tea.Cmd) {
	m.messages = append(m.messages, chatMsg{
		Role:    "sent",
		Content: fmt.Sprintf("@%s %s", botID, content),
	})

	payload := map[string]interface{}{
		"bot_id":  botID,
		"content": content,
		"stream":  false,
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", baseURL+"/api/v1/bot/message", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+authToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		m.messages = append(m.messages, chatMsg{Role: "error", Content: "Bot请求失败: " + err.Error()})
		return m, nil
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	if data, ok := result["data"].(map[string]interface{}); ok {
		if content, ok := data["content"].(string); ok {
			rendered, _ := m.renderer.Render(content)
			m.messages = append(m.messages, chatMsg{
				Role:    "bot",
				Content: rendered,
			})
		}
	}

	return m, nil
}

func (m model) summarizeChat() (tea.Model, tea.Cmd) {
	if m.chatID == "" {
		m.messages = append(m.messages, chatMsg{Role: "system", Content: "请先选择聊天"})
		return m, nil
	}

	m.messages = append(m.messages, chatMsg{Role: "system", Content: "正在生成摘要..."})

	payload := map[string]interface{}{
		"chat_id":   m.chatID,
		"chat_type": m.chatType,
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", baseURL+"/api/v1/summary/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+authToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		m.messages = append(m.messages, chatMsg{Role: "error", Content: "摘要请求失败: " + err.Error()})
		return m, nil
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	if data, ok := result["data"].(map[string]interface{}); ok {
		summaryJSON, _ := json.MarshalIndent(data, "", "  ")
		rendered, _ := m.renderer.Render(string(summaryJSON))
		m.messages = append(m.messages, chatMsg{Role: "bot", Content: rendered})
	}

	return m, nil
}

func (m *model) loadHistory() {
	if m.chatID == "" || authToken == "" {
		return
	}

	url := fmt.Sprintf("%s/api/v1/chat/history?chat_id=%s&limit=20", baseURL, m.chatID)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+authToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	if data, ok := result["data"].(map[string]interface{}); ok {
		if messages, ok := data["messages"].([]interface{}); ok {
			for _, msg := range messages {
				if msgMap, ok := msg.(map[string]interface{}); ok {
					role := "received"
					if sid, ok := msgMap["sender_id"].(string); ok && sid == userID {
						role = "sent"
					}
					content, _ := msgMap["content"].(string)
					m.messages = append(m.messages, chatMsg{
						Role:    role,
						Content: content,
					})
				}
			}
		}
	}
}

func (m model) View() string {
	var b strings.Builder

	b.WriteString(styleTitle.Render("AIM - 即时通讯客户端"))
	b.WriteString("\n")

	if m.chatID != "" {
		chatTypeStr := "私聊"
		if m.chatType == 2 {
			chatTypeStr = "群聊"
		}
		b.WriteString(fmt.Sprintf("当前聊天: %s [%s]\n", m.chatID, chatTypeStr))
	}

	b.WriteString(styleDivider.Render("────────────────────────────────────────"))
	b.WriteString("\n")

	maxVisible := 20
	start := 0
	if len(m.messages) > maxVisible {
		start = len(m.messages) - maxVisible
	}

	for i := start; i < len(m.messages); i++ {
		msg := m.messages[i]
		switch msg.Role {
		case "sent":
			b.WriteString(styleSent.Render("你: " + msg.Content))
		case "received":
			b.WriteString(styleReceived.Render("对方: " + msg.Content))
		case "bot":
			b.WriteString(styleBot.Render("🤖 Bot:\n" + msg.Content))
		case "system":
			b.WriteString(styleSystem.Render("系统: " + msg.Content))
		case "error":
			b.WriteString(styleError.Render("错误: " + msg.Content))
		default:
			b.WriteString(msg.Content)
		}
		b.WriteString("\n")
	}

	b.WriteString(styleDivider.Render("────────────────────────────────────────"))
	b.WriteString("\n")
	b.WriteString(m.input.View())
	b.WriteString("\n")

	return b.String()
}

func doLogin(username, password string) (string, string, error) {
	payload := map[string]string{
		"username": username,
		"password": password,
	}
	body, _ := json.Marshal(payload)

	resp, err := http.Post(baseURL+"/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("连接服务器失败: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return "", "", fmt.Errorf("登录失败: %s", string(respBody))
	}

	token, _ := data["token"].(string)
	uid := ""
	if user, ok := data["user"].(map[string]interface{}); ok {
		uid = fmt.Sprintf("%v", user["id"])
	}

	return token, uid, nil
}

func interactiveLogin() (string, string, error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("用户名: ")
	username, _ := reader.ReadString('\n')
	username = strings.TrimSpace(username)

	fmt.Print("密码: ")
	password, _ := reader.ReadString('\n')
	password = strings.TrimSpace(password)

	return doLogin(username, password)
}

func main() {
	flag.StringVar(&baseURL, "url", "http://localhost:8080", "API 服务器地址")
	flag.StringVar(&wsURL, "ws", "ws://localhost:8080/ws", "WebSocket 地址")
	username := flag.String("user", "", "用户名")
	password := flag.String("pass", "", "密码")
	flag.Parse()

	fmt.Println(styleTitle.Render("AIM - 即时通讯客户端"))
	fmt.Println(styleDivider.Render("────────────────────────────────────────"))

	var err error
	if *username != "" && *password != "" {
		authToken, userID, err = doLogin(*username, *password)
	} else {
		authToken, userID, err = interactiveLogin()
	}

	if err != nil {
		fmt.Println(styleError.Render("登录失败: " + err.Error()))
		os.Exit(1)
	}

	fmt.Println(styleSystem.Render("登录成功! Token: " + authToken[:20] + "..."))
	fmt.Println(styleSystem.Render("输入 /help 查看可用命令"))
	fmt.Println()

	p := tea.NewProgram(initialModel())
	if _, err := p.Run(); err != nil {
		fmt.Printf("错误: %v\n", err)
		os.Exit(1)
	}
}
