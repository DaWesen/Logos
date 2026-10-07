package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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

对话内容（每行前的 [N] 为消息序号，供 evidence 引用）：
%s

请以JSON格式输出提取的记忆，格式如下：
{
  "memories": [
    {
      "key": "简洁的记忆键名（英文小写+下划线）",
      "value": "记忆的详细内容",
      "category": "分类：preference/habit/fact/goal/relationship/style",
      "confidence": 0.8,
      "evidence": [1, 3]
    }
  ]
}

注意：
1. 只提取有长期价值的记忆，忽略临时性对话内容
2. key应简洁明了，如 "favorite_language"、"work_style"、"pet_name"
3. category分类说明：preference(偏好)、habit(习惯)、fact(事实)、goal(目标)、relationship(关系)、style(风格)
4. evidence 填写支持该条记忆的消息序号列表（对话中 [N] 标注的数字），只引用真实存在的序号
5. 如果没有值得提取的记忆，返回空数组
6. 只输出JSON，不要输出其他内容`

type ExtractedMemory struct {
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
	// Evidence 支撑该记忆的消息序号（从 1 开始），解析后转为消息 ID
	Evidence []int `json:"evidence"`
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
		// 带 [N] 序号前缀，供 evidence 引用
		conversationText = append(conversationText, fmt.Sprintf("[%d] %s: %s", len(conversationText)+1, role, msg.Content))
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
		// 归一化：模型没好好打分按"不知道"(0.5)处理，越界收敛
		mem.Confidence = normalizeConfidence(mem.Confidence)

		// 证据解析：序号 → 真实消息 ID；编造的序号整条丢弃（可审计底线）
		evidence := resolveEvidenceIDs(mem.Evidence, messages)

		existing, err := m.repo.GetUserMemoryByKey(ctx, userID, botID, mem.Key)
		if err == nil && existing != nil {
			merged, relation := ApplyMemoryMerge(existing, mem.Value, mem.Confidence, evidence)
			if relation != RelationUserOverride {
				merged.Category = mem.Category
				_ = m.repo.SetUserMemory(ctx, merged)
			}
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
			Evidence:   botmodel.StringSlice(evidence),
		}
		_ = m.repo.SetUserMemory(ctx, newMem)
	}

	logger.Info("自动提取记忆完成",
		logger.StringField("user_id", userID),
		logger.StringField("bot_id", botID),
		logger.IntField("count", len(result.Memories)))
}

// normalizeConfidence 置信度归一：非数字/越界一律按 0.5（"不知道"），不抛
func normalizeConfidence(raw float64) float64 {
	if raw <= 0 || raw > 1 {
		return 0.5
	}
	return raw
}

// resolveEvidenceIDs 把 LLM 引用的消息序号（1 开始）解析为真实消息 ID。
// 返回 (ids, ok)：任一序号不存在即编造来源，整条记忆作废。
func resolveEvidenceIDs(seq []int, messages []*botmodel.Message) []string {
	if len(seq) == 0 {
		return nil
	}
	ids := make([]string, 0, len(seq))
	for _, n := range seq {
		if n < 1 || n > len(messages) {
			return nil // 编造序号：记忆没有缝可钻
		}
		if messages[n-1].ID != "" {
			ids = append(ids, messages[n-1].ID)
		}
	}
	return ids
}

// BuildMemoryPrompt 构建 prompt 注入的记忆段（双档消费）：
//   - confidence < 0.5 的记忆不注入（agent 不会因为标注就不当真）；
//   - 0.5 ≤ confidence < 0.65 注入但标注「⚠︎证据较少」（只排序不标注，
//     0.55 与 0.9 的记忆在 agent 眼里长得一样）；
//   - 每类按 confidence 降序（最可靠的先入眼），超限截断必须声明条数
//     （静默截断会读成"这一类已经全了"）。
func (m *MemoryManager) BuildMemoryPrompt(ctx context.Context, userID, botID string) string {
	memories, err := m.repo.GetUserMemoriesByUser(ctx, userID, botID)
	if err != nil || len(memories) == 0 {
		return ""
	}

	// 双档门槛：不可靠的不进 prompt
	injectable := make([]*botmodel.UserMemory, 0, len(memories))
	dropped := 0
	for _, mem := range memories {
		if ShouldInjectMemory(mem) {
			injectable = append(injectable, mem)
		} else {
			dropped++
		}
	}
	if len(injectable) == 0 {
		return ""
	}

	var parts []string
	parts = append(parts, "以下是你对用户的已知记忆，请在回复时参考这些信息：")

	categories := map[string][]*botmodel.UserMemory{}
	for _, mem := range injectable {
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

	// 类别按固定顺序输出，类内按置信度降序
	catOrder := []string{"preference", "habit", "fact", "goal", "relationship", "style", "other"}
	for _, cat := range catOrder {
		mems, ok := categories[cat]
		if !ok {
			continue
		}
		sort.SliceStable(mems, func(i, j int) bool {
			return mems[i].Confidence > mems[j].Confidence
		})

		catName := categoryNames[cat]
		parts = append(parts, fmt.Sprintf("\n【%s】", catName))

		shown := mems
		if len(mems) > memoryPerCategoryLimit {
			shown = mems[:memoryPerCategoryLimit]
		}
		for _, mem := range shown {
			parts = append(parts, RenderMemoryLine(mem))
		}
		if len(mems) > len(shown) {
			parts = append(parts, fmt.Sprintf("（另有 %d 条置信度更低的记忆未列出）", len(mems)-len(shown)))
		}
	}

	if dropped > 0 {
		parts = append(parts, fmt.Sprintf("（另有 %d 条低置信度记忆未注入，可在记忆管理页查看）", dropped))
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
