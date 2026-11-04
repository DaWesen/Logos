package memory

import (
	"time"

	botmodel "Logos/internal/service/ai/bot/model"
)

// 记忆提取的触发策略：控制「多久跑一次 LLM」而不是「每次对话都跑」。
//
// 提取本身的成本是一次完整的模型调用，而收益随对话增量线性增长：
// 同一批 20 条消息反复提取，得到的记忆几乎一样（写路径只能去重，不能省调用）。
const (
	// extractMinNewMessages 至少积累这么多条「新消息」才值得再提取一次。
	extractMinNewMessages = 3
	// extractMaxAge 距上次提取超过该时长且至少有新消息时强制提取，
	// 避免低频用户永远攒不满阈值、记忆一直不更新。
	extractMaxAge = 30 * time.Minute
)

// extractionState 每个 (user, bot) 的提取进度。
type extractionState struct {
	lastRun time.Time
	lastMsg time.Time
}

// decideExtraction 判断本次是否触发提取，并返回本次应喂给模型的增量消息。
//
// 纯函数：便于在无仓储、无模型的情况下验证节流与增量窗口。
func decideExtraction(prev *extractionState, messages []*botmodel.Message, now time.Time) (batch []*botmodel.Message, run bool, next *extractionState) {
	if len(messages) == 0 {
		return nil, false, prev
	}

	if prev == nil {
		// 首次提取：至少要有两条消息，单条消息不足以支撑任何「事实」。
		if len(messages) < 2 {
			return nil, false, prev
		}
		return messages, true, &extractionState{lastRun: now, lastMsg: newestMessageTime(messages)}
	}

	fresh := make([]*botmodel.Message, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		if prev.lastMsg.IsZero() || msg.CreatedAt.After(prev.lastMsg) {
			fresh = append(fresh, msg)
		}
	}
	if len(fresh) == 0 {
		return nil, false, prev
	}

	// 增量没攒够，但距上次提取已经很久：强制跑一次（低频用户的兜底）。
	if len(fresh) < extractMinNewMessages && now.Sub(prev.lastRun) < extractMaxAge {
		return nil, false, prev
	}

	return fresh, true, &extractionState{lastRun: now, lastMsg: newestMessageTime(messages)}
}

func newestMessageTime(messages []*botmodel.Message) time.Time {
	var newest time.Time
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		if msg.CreatedAt.After(newest) {
			newest = msg.CreatedAt
		}
	}
	return newest
}
