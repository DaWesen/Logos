package scache

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// AnswerKind 区分正常答案与兜底文案。兜底文案入库会让用户连续
// 多次拿到同一个错误结果，且刷新无效（D8）。
type AnswerKind string

const (
	AnswerFinal    AnswerKind = "final"
	AnswerDegraded AnswerKind = "degraded"
)

// Entry 是一条缓存答案。
//
// 条目不可变（不变量 I5）：更新 = 新增一条 + 旧条目过期，
// 不做就地改写，避免并发读到半写状态。
type Entry struct {
	ID        string     `json:"id"`
	Query     string     `json:"q"`
	NormQuery string     `json:"nq"`
	Slots     SlotSet    `json:"slots"`
	Answer    string     `json:"a"`
	Kind      AnswerKind `json:"kind"`
	Vec       []float32  `json:"-"`
	Dim       int        `json:"dim"`
	EmbedKey  string     `json:"ek"`
	ScopeHash string     `json:"sh"`
	CreatedAt int64      `json:"c"`
	ExpireAt  int64      `json:"e"`
	Hits      int32      `json:"h"`
	Source    string     `json:"src"`
}

// Valid 在反序列化后调用。任何一项不符都视为损坏条目并跳过。
//
// 这里校验的是「向量确实由同一个 embedding 模型产生」：
// 即使 bucket key 拼错，也不会拿不同模型的向量去算相似度。
func (e *Entry) Valid(scopeHash string, dim int) bool {
	if e == nil || e.ID == "" || e.Answer == "" || e.NormQuery == "" {
		return false
	}
	if e.ScopeHash != scopeHash || e.Dim <= 0 || len(e.Vec) != e.Dim {
		return false
	}
	if dim > 0 && e.Dim != dim {
		return false
	}
	return true
}

// Expired 判断条目是否已过逻辑有效期。TTL 只是物理回收，
// 读路径仍要判一次，避免 Redis 过期延迟或本地索引残留。
func (e *Entry) Expired(now int64) bool {
	return e.ExpireAt > 0 && now >= e.ExpireAt
}

// SizeBytes 是条目对内存占用的估算，用于字节上限（G8 的第二个上限）。
func (e *Entry) SizeBytes() int64 {
	return int64(len(e.Vec)*4 + len(e.Answer) + len(e.Query) + 128)
}

// persistedEntry 是落 Redis 的线格式。
//
// 向量用 base64 编码的 float32 小端字节，而不是 JSON 数组：
// 1024 维从约 13KB 降到约 5.5KB，且反序列化不用逐元素解析文本。
type persistedEntry struct {
	ID        string     `json:"id"`
	Query     string     `json:"q"`
	NormQuery string     `json:"nq"`
	Slots     SlotSet    `json:"slots"`
	Answer    string     `json:"a"`
	Kind      AnswerKind `json:"kind"`
	Vec       string     `json:"v"`
	Dim       int        `json:"dim"`
	EmbedKey  string     `json:"ek"`
	ScopeHash string     `json:"sh"`
	CreatedAt int64      `json:"c"`
	ExpireAt  int64      `json:"e"`
	Hits      int32      `json:"h"`
	Source    string     `json:"src"`
}

func (e *Entry) marshal() ([]byte, error) {
	if len(e.Vec) != e.Dim {
		return nil, fmt.Errorf("scache: vector dim mismatch (%d != %d)", len(e.Vec), e.Dim)
	}
	return json.Marshal(persistedEntry{
		ID: e.ID, Query: e.Query, NormQuery: e.NormQuery, Slots: e.Slots,
		Answer: e.Answer, Kind: e.Kind, Vec: encodeVector(e.Vec), Dim: e.Dim,
		EmbedKey: e.EmbedKey, ScopeHash: e.ScopeHash, CreatedAt: e.CreatedAt,
		ExpireAt: e.ExpireAt, Hits: e.Hits, Source: e.Source,
	})
}

func unmarshalEntry(data []byte) (*Entry, error) {
	var p persistedEntry
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	vec, err := decodeVector(p.Vec)
	if err != nil {
		return nil, err
	}
	return &Entry{
		ID: p.ID, Query: p.Query, NormQuery: p.NormQuery, Slots: p.Slots,
		Answer: p.Answer, Kind: p.Kind, Vec: vec, Dim: p.Dim,
		EmbedKey: p.EmbedKey, ScopeHash: p.ScopeHash, CreatedAt: p.CreatedAt,
		ExpireAt: p.ExpireAt, Hits: p.Hits, Source: p.Source,
	}, nil
}

func encodeVector(vec []float32) string {
	raw := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(raw[i*4:], math.Float32bits(v))
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func decodeVector(encoded string) ([]float32, error) {
	if encoded == "" {
		return nil, fmt.Errorf("scache: empty vector")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("scache: decode vector: %w", err)
	}
	if len(raw)%4 != 0 {
		return nil, fmt.Errorf("scache: vector length %d is not a multiple of 4", len(raw))
	}
	vec := make([]float32, len(raw)/4)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return vec, nil
}

// NewEntryID 生成 16 位十六进制 ID，作为 Hash field 与 ZSet member。
func NewEntryID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}
