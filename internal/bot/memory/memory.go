package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"Logos/internal/bot/agent"
	"Logos/internal/service/ai/bot/dao"
	botmodel "Logos/internal/service/ai/bot/model"
	"Logos/pkg/eino"
	"Logos/pkg/logger"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const memoryExtractionPrompt = `你是一个记忆提取助手。请分析以下用户与AI的对话，提取出用户的偏好、习惯、重要信息等长期记忆。

对话内容：
%s

请以JSON格式输出提取的记忆，格式如下：
{
  "memories": [
    {
      "key": "简洁的记忆键名（英文小写+下划线）",
      "value": "记忆的详细内容",
      "category": "分类：preference/habit/fact/goal/relationship/style"
    }
  ]
}

注意：
1. 只提取有长期价值的记忆，忽略临时性对话内容
2. key应简洁明了，如 "favorite_language"、"work_style"、"pet_name"
3. category分类说明：preference(偏好)、habit(习惯)、fact(事实)、goal(目标)、relationship(关系)、style(风格)
4. 如果没有值得提取的记忆，返回空数组
5. 只输出JSON，不要输出其他内容`

type ExtractedMemory struct {
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
}

type MemoryExtractionResult struct {
	Memories []ExtractedMemory `json:"memories"`
}

type MemoryManager struct {
	repo       dao.BotRepository
	einoMgr    *eino.EinoManager
	agentMgr   *agent.AgentManager
	mu         sync.Mutex
	processing map[string]bool
}

var memoryMgr *MemoryManager
var memoryOnce sync.Once

func GetMemoryManager(repo dao.BotRepository, einoMgr *eino.EinoManager, agentMgr *agent.AgentManager) *MemoryManager {
	memoryOnce.Do(func() {
		memoryMgr = &MemoryManager{
			repo:       repo,
			einoMgr:    einoMgr,
			agentMgr:   agentMgr,
			processing: make(map[string]bool),
		}
	})
	return memoryMgr
}

func (m *MemoryManager) ExtractAndSaveMemories(ctx context.Context, userID, botID string, messages []*botmodel.Message, chatModel einomodel.BaseChatModel) {
	key := fmt.Sprintf("%s:%s", userID, botID)
	m.mu.Lock()
	if m.processing[key] {
		m.mu.Unlock()
		return
	}
	m.processing[key] = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.processing, key)
		m.mu.Unlock()
	}()

	go m.doExtractAndSave(userID, botID, messages, chatModel)
}

func (m *MemoryManager) doExtractAndSave(userID, botID string, messages []*botmodel.Message, chatModel einomodel.BaseChatModel) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if len(messages) == 0 {
		return
	}

	var conversationText []string
	for _, msg := range messages {
		role := "用户"
		if msg.Role == "assistant" {
			role = "助手"
		}
		conversationText = append(conversationText, fmt.Sprintf("%s: %s", role, msg.Content))
	}

	dialogText := strings.Join(conversationText, "\n")
	if len(dialogText) > 4000 {
		dialogText = dialogText[len(dialogText)-4000:]
	}

	prompt := fmt.Sprintf(memoryExtractionPrompt, dialogText)

	var resp string
	var err error

	if chatModel != nil {
		schemaMsgs := []*schema.Message{
			schema.SystemMessage("你是一个JSON输出助手，只输出JSON格式的内容。"),
			schema.UserMessage(prompt),
		}
		result, genErr := chatModel.Generate(ctx, schemaMsgs)
		if genErr != nil {
			logger.Warn("使用Bot模型进行记忆提取失败，尝试全局模型", logger.ErrorField(genErr))
			resp, err = m.einoMgr.Chat(ctx, []string{prompt})
		} else {
			resp = result.Content
		}
	} else {
		resp, err = m.einoMgr.Chat(ctx, []string{prompt})
	}

	if err != nil {
		logger.Warn("记忆提取LLM调用失败", logger.ErrorField(err))
		return
	}

	var result MemoryExtractionResult
	cleanResp := trimJSON(resp)
	if err := json.Unmarshal([]byte(cleanResp), &result); err != nil {
		logger.Warn("记忆提取结果解析失败", logger.ErrorField(err), logger.StringField("response", resp))
		return
	}

	for _, mem := range result.Memories {
		if mem.Key == "" || mem.Value == "" {
			continue
		}
		if mem.Category == "" {
			mem.Category = "fact"
		}
		if mem.Confidence == 0 {
			mem.Confidence = 0.8
		}

		existing, err := m.repo.GetUserMemoryByKey(ctx, userID, botID, mem.Key)
		if err == nil && existing != nil {
			existing.Value = mem.Value
			existing.Category = mem.Category
			existing.Confidence = mem.Confidence
			existing.Source = "auto_extract"
			_ = m.repo.SetUserMemory(ctx, existing)
			continue
		}

		newMem := &botmodel.UserMemory{
			UserID:     userID,
			BotID:      botID,
			Key:        mem.Key,
			Value:      mem.Value,
			Category:   mem.Category,
			Source:     "auto_extract",
			Confidence: mem.Confidence,
		}
		_ = m.repo.SetUserMemory(ctx, newMem)
	}

	logger.Info("自动提取记忆完成",
		logger.StringField("user_id", userID),
		logger.StringField("bot_id", botID),
		logger.IntField("count", len(result.Memories)))
}

func (m *MemoryManager) BuildMemoryPrompt(ctx context.Context, userID, botID string) string {
	memories, err := m.repo.GetUserMemoriesByUser(ctx, userID, botID)
	if err != nil || len(memories) == 0 {
		return ""
	}

	var parts []string
	parts = append(parts, "以下是你对用户的已知记忆，请在回复时参考这些信息：")

	categories := map[string][]*botmodel.UserMemory{}
	for _, mem := range memories {
		cat := mem.Category
		if cat == "" {
			cat = "other"
		}
		categories[cat] = append(categories[cat], mem)
	}

	categoryNames := map[string]string{
		"preference":   "用户偏好",
		"habit":        "用户习惯",
		"fact":         "用户信息",
		"goal":         "用户目标",
		"relationship": "关系信息",
		"style":        "交互风格",
		"other":        "其他",
	}

	for cat, mems := range categories {
		catName := categoryNames[cat]
		if catName == "" {
			catName = cat
		}
		parts = append(parts, fmt.Sprintf("\n【%s】", catName))
		for _, mem := range mems {
			parts = append(parts, fmt.Sprintf("- %s: %s", mem.Key, mem.Value))
		}
	}

	return strings.Join(parts, "\n")
}

func (m *MemoryManager) GetMemoriesByCategory(ctx context.Context, userID, botID, category string) ([]*botmodel.UserMemory, error) {
	all, err := m.repo.GetUserMemoriesByUser(ctx, userID, botID)
	if err != nil {
		return nil, err
	}
	var filtered []*botmodel.UserMemory
	for _, mem := range all {
		if mem.Category == category {
			filtered = append(filtered, mem)
		}
	}
	return filtered, nil
}

func (m *MemoryManager) CleanupOldMemories(ctx context.Context, userID, botID string, maxAge time.Duration) error {
	memories, err := m.repo.GetUserMemoriesByUser(ctx, userID, botID)
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-maxAge)
	for _, mem := range memories {
		if mem.Source == "manual" {
			continue
		}
		if mem.UpdatedAt.Before(cutoff) && mem.Source == "auto_extract" && mem.Confidence < 0.6 {
			_ = m.repo.DeleteUserMemoryByID(ctx, mem.ID)
		}
	}
	return nil
}

func trimJSON(s string) string {
	s = strings.TrimSpace(s)
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}
