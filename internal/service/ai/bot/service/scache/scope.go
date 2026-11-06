// Package scache 实现语义缓存 v2 的判定、检索与持久化。
//
// 设计取舍见 docs/semantic-cache-v2-spec.md。这里只强调两条：
//   - 缓存是可选优化：任何依赖故障都降级为 miss，绝不影响主链路；
//   - 复用边界由 Scope 决定，而不是由相似度决定。相似度只负责在
//     同一个 Scope 内排序，跨用户/跨版本的数据在物理上不可达。
package scache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// ErrEmptyUser 表示缺少用户维度。跨用户共享缓存会泄漏个人数据，
// 因此 UserID 为空时直接拒绝读写，而不是退化成全局缓存。
var ErrEmptyUser = errors.New("scache: user id is required")

// Scope 是决定「一条答案能否被复用」的全部维度。
//
// 任何影响答案的输入都必须出现在这里（不变量 I1）：
// 换 Bot、换用户、知识库更新、改系统提示词、换生成模型、换 embedding
// 模型，都会得到不同的 ScopeHash，旧条目在物理上不可达。
type Scope struct {
	Tenant     string // 预留的多租户维度；当前单租户填 "default"
	BotID      string
	UserID     string // 非空；不做跨用户共享
	KBVersion  string // 知识库版本号，见 Cache.KBVersion
	PromptHash string // 规范化后系统提示词的 sha256 前缀
	ModelKey   string // provider/model/temperature
	EmbedKey   string // provider/embedding-model（维度在 Entry 上校验）
}

// Validate 检查 Scope 是否具备最小可用维度。
func (s Scope) Validate() error {
	if strings.TrimSpace(s.UserID) == "" {
		return ErrEmptyUser
	}
	if s.BotID == "" || s.EmbedKey == "" {
		return errors.New("scache: bot id and embedding key are required")
	}
	return nil
}

// Hash 返回 16 字节十六进制摘要。
//
// 用 "\x1f"（Unit Separator）拼接：它不会出现在正常的 prompt / 模型名里，
// 因此 ("ab","c") 与 ("a","bc") 不会撞成同一个 key。顺序敏感是刻意的——
// 少拼一个维度就会让两套本应隔离的答案落到同一个 bucket。
func (s Scope) Hash() string {
	h := sha256.New()
	for i, part := range []string{
		s.Tenant, s.BotID, s.UserID, s.KBVersion,
		s.PromptHash, s.ModelKey, s.EmbedKey,
	} {
		if i > 0 {
			h.Write([]byte{0x1f})
		}
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// PromptHash 对规范化后的提示词取 sha256 前 12 位。
//
// 先统一换行与首尾空白：编辑器/数据库往返可能引入 \r\n 或尾随空格，
// 这类差异不改变模型行为，却会让整库缓存无谓失效。
func PromptHash(prompt string) string {
	normalized := strings.ReplaceAll(prompt, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	normalized = strings.TrimSpace(normalized)
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])[:12]
}
