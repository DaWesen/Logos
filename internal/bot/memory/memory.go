package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"Logos/internal/bot/agent"
	"Logos/internal/service/ai/bot/dao"
	botmodel "Logos/internal/service/ai/bot/model"
	"Logos/pkg/eino"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	"Logos/pkg/structured"

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
	// states 记录每个 (user, bot) 的提取进度，用于节流与增量窗口。
	states map[string]*extractionState
	// now 可注入时钟，便于验证节流逻辑。
	now func() time.Time
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
			states:     make(map[string]*extractionState),
			now:        time.Now,
		}
	})
	return memoryMgr
}

func (m *MemoryManager) ExtractAndSaveMemories(ctx context.Context, userID, botID string, messages []*botmodel.Message, chatModel einomodel.BaseChatModel) {
	key := fmt.Sprintf("%s:%s", userID, botID)

	// 先做节流决策，再抢 processing 标志：
	// 反过来会导致「被跳过的调用占了并发位」，真正的提取反而排队。
	batch, run, next := m.decide(key, messages)
	if !run {
		return
	}

	m.mu.Lock()
	if m.processing[key] {
		m.mu.Unlock()
		return
	}
	if m.states == nil {
		m.states = make(map[string]*extractionState)
	}
	m.processing[key] = true
	m.states[key] = next
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.processing, key)
		m.mu.Unlock()
	}()

	go m.doExtractAndSave(userID, botID, batch, chatModel)
}

// decide 在锁内读取提取进度并给出本次的增量批次。
func (m *MemoryManager) decide(key string, messages []*botmodel.Message) ([]*botmodel.Message, bool, *extractionState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return decideExtraction(m.states[key], messages, m.now())
}

func (m *MemoryManager) doExtractAndSave(userID, botID string, messages []*botmodel.Message, chatModel einomodel.BaseChatModel) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if len(messages) == 0 {
		return
	}

	dialogText := buildDialogText(messages, memoryDialogBudgetRunes)

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
	_, parseErr := structured.ParseWithRepair(ctx, "memory", resp, &result,
		func(ctx context.Context, raw string, parseErr error) (string, error) {
			repairPrompt := structured.RepairPrompt(raw, parseErr,
				`{"memories":[{"key":"...","value":"...","category":"...","confidence":0.0,"evidence":[1]}]}`)
			if chatModel != nil {
				fixed, err := chatModel.Generate(ctx, []*schema.Message{
					schema.SystemMessage("你是一个JSON修复助手，只输出修正后的JSON。"),
					schema.UserMessage(repairPrompt),
				})
				if err == nil {
					return fixed.Content, nil
				}
			}
			if m.einoMgr == nil {
				return "", parseErr
			}
			return m.einoMgr.Chat(ctx, []string{repairPrompt})
		})
	if parseErr != nil {
		logger.Warn("记忆提取结果解析失败", logger.ErrorField(parseErr), logger.StringField("response", resp))
		return
	}

	// 一次提取的结果也要设上限：模型可能把整段对话拆成几十条，
	// 全量写入会让记忆表迅速膨胀，反过来挤占注入预算。
	seenKeys := make(map[string]struct{}, len(result.Memories))
	saved, failed, deduped, mergedCount := 0, 0, 0, 0
	processed := 0
	// 相似度合并需要已有记忆快照；只在首次未命中时才加载，避免每条都查一次库。
	var known []*botmodel.UserMemory
	knownLoaded := false
	for _, mem := range result.Memories {
		if processed >= maxMemoriesPerExtraction {
			break
		}
		if !prepareExtractedMemory(&mem, seenKeys) {
			deduped++
			continue
		}
		processed++
		// 归一化：模型没好好打分按"不知道"(0.5)处理，越界收敛
		mem.Confidence = normalizeConfidence(mem.Confidence)

		// 证据解析：序号 → 真实消息 ID；编造的序号整条丢弃（可审计底线）
		evidence, ok := resolveEvidenceIDs(mem.Evidence, messages)
		if !ok {
			obs.RecordLLMParse(ctx, "memory", "fabricated_evidence")
			logger.Warn("记忆证据序号不存在，丢弃该条记忆",
				logger.StringField("key", mem.Key),
				logger.IntField("evidence_size", len(mem.Evidence)))
			continue
		}

		existing, err := m.repo.GetUserMemoryByKey(ctx, userID, botID, mem.Key)
		if err == nil && existing != nil {
			merged, relation := ApplyMemoryMerge(existing, mem.Value, mem.Confidence, evidence)
			if relation != RelationUserOverride {
				merged.Category = mem.Category
				if setErr := m.repo.SetUserMemory(ctx, merged); setErr != nil {
					failed++
					logger.Warn("合并记忆写入失败",
						logger.StringField("key", mem.Key),
						logger.ErrorField(setErr))
				} else {
					saved++
				}
			}
			continue
		}

		// 精确键未命中时，再看是否有「措辞几乎一致」的已有记忆：
		// 归一化只能消格式差异，同义措辞仍会各存一条，最后互相挤占注入预算。
		if !knownLoaded {
			known, _ = m.repo.GetUserMemoriesByUser(ctx, userID, botID)
			knownLoaded = true
		}
		if idx := findSimilarMemoryIndex(known, mem.Key, mem.Category); idx >= 0 {
			merged, relation := ApplyMemoryMerge(known[idx], mem.Value, mem.Confidence, evidence)
			if relation != RelationUserOverride {
				merged.Category = mem.Category
				if setErr := m.repo.SetUserMemory(ctx, merged); setErr != nil {
					failed++
					logger.Warn("相似记忆合并失败",
						logger.StringField("key", mem.Key),
						logger.StringField("existing_key", known[idx].Key),
						logger.ErrorField(setErr))
				} else {
					known[idx] = merged
					mergedCount++
				}
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
		if setErr := m.repo.SetUserMemory(ctx, newMem); setErr != nil {
			failed++
			logger.Warn("新记忆写入失败",
				logger.StringField("key", mem.Key),
				logger.ErrorField(setErr))
		} else {
			saved++
			if knownLoaded {
				// 保证同批次后续条目能看到这条新记忆
				known = append(known, newMem)
			}
		}
	}

	logger.Info("自动提取记忆完成",
		logger.StringField("user_id", userID),
		logger.StringField("bot_id", botID),
		logger.IntField("candidates", len(result.Memories)),
		logger.IntField("saved", saved),
		logger.IntField("failed", failed),
		logger.IntField("deduped", deduped),
		logger.IntField("merged_similar", mergedCount))
	if failed > 0 {
		// 旧实现 `_ =` 吞掉写入错误，日志照样报「完成 N 条」，
		// 结果双方都以为存进去了，下次提取又生成一遍。
		logger.Warn("部分记忆未能写入",
			logger.StringField("user_id", userID),
			logger.IntField("failed", failed))
	}
}

// maxMemoriesPerExtraction 单次提取最多写入的记忆条数。
const maxMemoriesPerExtraction = 10

// normalizeMemoryKey 归一化记忆键，消除「同一个事实、不同写法」造成的重复条目。
//
// 写路径用 Key 做精确匹配（GetUserMemoryByKey），模型每次措辞略有差异
// （"喜欢喝咖啡" / " 喜欢 喝咖啡。 "）就会被当成新记忆插入。
// 语义级去重要靠向量，这里先把格式变体收敛掉——成本几乎为零。
func normalizeMemoryKey(key string) string {
	k := strings.TrimSpace(key)
	k = strings.Join(strings.Fields(k), " ")
	return strings.Trim(k, "。.,，;；:：!！?？")
}

// prepareExtractedMemory 归一化字段并做批次内去重。
// 返回 false 表示该条不应写入（空键/空值/本批次已出现）。
func prepareExtractedMemory(mem *ExtractedMemory, seen map[string]struct{}) bool {
	if mem == nil {
		return false
	}
	mem.Key = normalizeMemoryKey(mem.Key)
	mem.Value = strings.TrimSpace(mem.Value)
	if mem.Category == "" {
		mem.Category = "fact"
	}
	if mem.Key == "" || mem.Value == "" {
		return false
	}
	if _, dup := seen[mem.Key]; dup {
		return false
	}
	seen[mem.Key] = struct{}{}
	return true
}

// memoryDialogBudgetRunes 送进提取 prompt 的对话预算（字符数）。
const memoryDialogBudgetRunes = 4000

// buildDialogText 生成带 [N] 序号的对话文本（N 从 1 开始）。
//
// 两条硬约束：
//  1. N 必须是「消息在原始切片里的下标」，绝不因为截断而平移——prompt 里让模型
//     引用序号，`resolveEvidenceIDs` 又按原切片下标解析，一旦平移，模型老实引用
//     [3] 反而会被挂到最后一条完全无关的消息上（更糟的是静默通过审计）。
//  2. 预算按 rune 计、从尾部保留。按字节切会切出半个汉字，还可能把行首的 `[12]`
//     前缀切掉，让这一行变成没有序号的裸文本。
//
// 超预算时从最早的消息开始丢；若最新一条自身就超预算，只保留它的尾部。
func buildDialogText(messages []*botmodel.Message, budgetRunes int) string {
	if len(messages) == 0 || budgetRunes <= 0 {
		return ""
	}

	lines := make([]string, 0, len(messages))
	used := 0

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg == nil {
			continue
		}

		role := "用户"
		if msg.Role == "assistant" {
			role = "助手"
		}
		line := fmt.Sprintf("[%d] %s: %s", i+1, role, msg.Content)

		cost := utf8.RuneCountInString(line)
		if len(lines) > 0 {
			cost++ // 行间换行符
		}

		if used+cost <= budgetRunes {
			lines = append(lines, line)
			used += cost
			continue
		}

		if len(lines) == 0 {
			prefix := fmt.Sprintf("[%d] %s: ", i+1, role)
			room := budgetRunes - utf8.RuneCountInString(prefix) - 1 // 1 留给省略号
			if room > 0 {
				lines = append(lines, prefix+"…"+tailRunes(msg.Content, room))
			} else {
				lines = append(lines, prefix)
			}
		}
		break
	}

	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}

	return strings.Join(lines, "\n")
}

// tailRunes 取字符串尾部 max 个字符（rune），不足则原样返回。
func tailRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[len(r)-max:])
}

// normalizeConfidence 置信度归一：非数字/越界一律按 0.5（"不知道"），不抛
func normalizeConfidence(raw float64) float64 {
	if raw <= 0 || raw > 1 {
		return 0.5
	}
	return raw
}

// resolveEvidenceIDs 把 LLM 引用的消息序号（1 开始）解析为真实消息 ID。
//
// 返回 (ids, ok)：ok=false 表示模型编造了来源，调用方必须丢弃整条记忆——
// 「证据全部不存在却照样入库」的记忆是无法审计的幻觉，查也查不到出处。
// 未引用任何序号属于正常情况（ok=true, ids=nil）。
func resolveEvidenceIDs(seq []int, messages []*botmodel.Message) ([]string, bool) {
	if len(seq) == 0 {
		return nil, true
	}
	ids := make([]string, 0, len(seq))
	for _, n := range seq {
		if n < 1 || n > len(messages) {
			return nil, false // 编造序号：记忆没有缝可钻
		}
		if messages[n-1].ID != "" {
			ids = append(ids, messages[n-1].ID)
		}
	}
	return ids, true
}

// BuildMemoryPrompt 构建 prompt 注入的记忆段（无对话上下文版本）。
func (m *MemoryManager) BuildMemoryPrompt(ctx context.Context, userID, botID string) string {
	return m.BuildMemoryPromptFor(ctx, userID, botID, "")
}

// BuildMemoryPromptFor 按当前问题检索最相关的记忆后构建 prompt 段：
//   - confidence < 0.5 的记忆不注入（agent 不会因为标注就不当真）；
//   - 0.5 ≤ confidence < 0.65 注入但标注「⚠︎证据较少」（只排序不标注，
//     0.55 与 0.9 的记忆在 agent 眼里长得一样）；
//   - 相关性 / 新鲜度 / 置信度三因子排序，手工记忆始终保留；
//   - 每类按分数降序，超限截断必须声明条数（静默截断会读成「这一类已经全了」）。
func (m *MemoryManager) BuildMemoryPromptFor(ctx context.Context, userID, botID, query string) string {
	memories, err := m.repo.GetUserMemoriesByUser(ctx, userID, botID)
	if err != nil || len(memories) == 0 {
		return ""
	}

	// 双档门槛：不可靠的不进 prompt
	injectable := make([]*botmodel.UserMemory, 0, len(memories))
	dropped := 0
	for _, mem := range memories {
		// 仓储层返回 nil 元素时（查询失败占位、批量组装漏洞）这里会直接解引用 panic，
		// 而 panic 发生在对话链路里，表现为「这个用户一说话服务就重启」。
		if mem == nil {
			continue
		}
		if ShouldInjectMemory(mem) {
			injectable = append(injectable, mem)
		} else {
			dropped++
		}
	}
	if len(injectable) == 0 {
		return ""
	}

	selected := selectMemories(injectable, query, time.Now())
	unselected := len(injectable) - len(selected)

	var parts []string
	parts = append(parts, "以下是你对用户的已知记忆，请在回复时参考这些信息：")

	categories := map[string][]*botmodel.UserMemory{}
	for _, mem := range selected {
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
		sortMemories(mems)

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
	if unselected > 0 {
		parts = append(parts, fmt.Sprintf("（另有 %d 条与当前问题无关或较旧的记忆未列出）", unselected))
	}

	return strings.Join(parts, "\n")
}

// sortMemories 置信度降序，同分用 UpdatedAt 降序、ID 升序兜底。
//
// 只按 confidence 排会让同分记忆的顺序取决于数据库返回顺序（无 ORDER BY 时不稳定），
// 同一用户两次刷新可能拿到不同 prompt：前缀缓存命中率下降，用户也会觉得
// 「明明没改过设置，助手的说法却变了」。
func sortMemories(mems []*botmodel.UserMemory) {
	sort.SliceStable(mems, func(i, j int) bool {
		if mems[i].Confidence != mems[j].Confidence {
			return mems[i].Confidence > mems[j].Confidence
		}
		if !mems[i].UpdatedAt.Equal(mems[j].UpdatedAt) {
			return mems[i].UpdatedAt.After(mems[j].UpdatedAt)
		}
		return mems[i].ID < mems[j].ID
	})
}

func (m *MemoryManager) GetMemoriesByCategory(ctx context.Context, userID, botID, category string) ([]*botmodel.UserMemory, error) {
	all, err := m.repo.GetUserMemoriesByUser(ctx, userID, botID)
	if err != nil {
		return nil, err
	}
	var filtered []*botmodel.UserMemory
	for _, mem := range all {
		if mem != nil && mem.Category == category {
			filtered = append(filtered, mem)
		}
	}
	sortMemories(filtered)
	return filtered, nil
}

func (m *MemoryManager) CleanupOldMemories(ctx context.Context, userID, botID string, maxAge time.Duration) error {
	memories, err := m.repo.GetUserMemoriesByUser(ctx, userID, botID)
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-maxAge)
	var errs []error
	removed := 0
	for _, mem := range memories {
		if mem == nil || mem.Source == "manual" {
			continue
		}
		if mem.UpdatedAt.Before(cutoff) && mem.Source == "auto_extract" && mem.Confidence < 0.6 {
			if delErr := m.repo.DeleteUserMemoryByID(ctx, mem.ID); delErr != nil {
				// 单条删除失败不中断整轮清理，但必须把错误带回去：
				// 旧实现直接 `_ =` 吞掉，清理任务显示成功、记忆其实还在。
				errs = append(errs, fmt.Errorf("删除记忆 %s 失败: %w", mem.ID, delErr))
				continue
			}
			removed++
		}
	}
	if removed > 0 {
		logger.Info("清理过期记忆",
			logger.StringField("user_id", userID),
			logger.StringField("bot_id", botID),
			logger.IntField("removed", removed))
	}
	return errors.Join(errs...)
}
