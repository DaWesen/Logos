package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"Logos/config"
	"Logos/internal/bot/ctxmem"

	"github.com/redis/go-redis/v9"
)

// summaryCASScript 是摘要状态的单调写入脚本。
//
// 为什么必须放在 Redis 侧：多副本同时摘要同一会话时，先读后写的
// 应用层 CAS 仍然会互相覆盖。把「比较 created_ms + up_to」和写入
// 放进同一个 Lua 脚本，Redis 的单线程执行保证了原子性。
//
// 比较键用 created_at 毫秒而不是 UUID：UUID 没有时间序。同毫秒时
// 再用消息 ID 打破平局，保证任意两次写入都有确定的全序。
var summaryCASScript = redis.NewScript(`
local curCreated = redis.call('HGET', KEYS[1], 'created_ms')
local curUpTo = redis.call('HGET', KEYS[1], 'up_to') or ''
local newCreated = tonumber(ARGV[1])
local newUpTo = ARGV[2]

local shouldWrite = false
if not curCreated then
  shouldWrite = true
else
  local current = tonumber(curCreated)
  if current < newCreated or (current == newCreated and curUpTo < newUpTo) then
    shouldWrite = true
  end
end

if not shouldWrite then
  return 0
end

redis.call('HSET', KEYS[1],
  'version', ARGV[3],
  'up_to', newUpTo,
  'created_ms', ARGV[1],
  'payload', ARGV[4],
  'updated_at', ARGV[5])
redis.call('EXPIRE', KEYS[1], ARGV[6])
return 1
`)

// RedisSummaryStore 把摘要状态放在 Redis，支持多副本共享。
//
// 它只实现存储语义，不关心熔断、单飞或渲染：这些留在 compressor，
// 换存储不会改变上层行为。
type RedisSummaryStore struct {
	client *redis.Client
	prefix string
	ttl    time.Duration
}

func NewRedisSummaryStore(client *redis.Client) *RedisSummaryStore {
	return &RedisSummaryStore{
		client: client,
		// 前缀带 v2：结构化 JSON 与旧纯文本摘要的格式不同，
		// 共用 key 会让灰度期间的新旧副本互相读到无法解析的 payload。
		prefix: "ctxmem:summary:v2:",
		ttl:    summaryStateTTL,
	}
}

func (s *RedisSummaryStore) key(conversationID string) string {
	return s.prefix + conversationID
}

func (s *RedisSummaryStore) Load(ctx context.Context, conversationID string) (ctxmem.SummaryState, bool, error) {
	if s == nil || s.client == nil {
		return ctxmem.SummaryState{}, false, errors.New("ctxmem: Redis 摘要存储未初始化")
	}
	fields, err := s.client.HGetAll(ctx, s.key(conversationID)).Result()
	if err != nil {
		return ctxmem.SummaryState{}, false, err
	}
	if len(fields) == 0 {
		return ctxmem.SummaryState{}, false, nil
	}

	state := ctxmem.SummaryState{
		ConversationID: conversationID,
		UpToMsgID:      fields["up_to"],
		Payload:        []byte(fields["payload"]),
	}
	if version, err := strconv.Atoi(fields["version"]); err == nil {
		state.Version = version
	}
	if createdMS, err := strconv.ParseInt(fields["created_ms"], 10, 64); err == nil {
		state.Order.CreatedMS = createdMS
	}
	state.Order.MsgID = state.UpToMsgID
	if updatedMS, err := strconv.ParseInt(fields["updated_at"], 10, 64); err == nil {
		state.UpdatedAt = time.UnixMilli(updatedMS)
	}
	return state, true, nil
}

func (s *RedisSummaryStore) Save(ctx context.Context, state ctxmem.SummaryState) (bool, error) {
	if s == nil || s.client == nil {
		return false, errors.New("ctxmem: Redis 摘要存储未初始化")
	}
	result, err := summaryCASScript.Run(ctx, s.client, []string{s.key(state.ConversationID)},
		state.Order.CreatedMS,
		state.UpToMsgID,
		state.Version,
		string(state.Payload),
		state.UpdatedAt.UnixMilli(),
		int64(s.ttl.Seconds()),
	).Int()
	if err != nil {
		return false, fmt.Errorf("ctxmem: 写入 Redis 摘要: %w", err)
	}
	return result == 1, nil
}

// newSummaryStore 装配摘要存储：配了 Redis 就主 Redis + 进程内兜底，
// 没配就只用进程内实现。
//
// 这里刻意不 ping：构造 NewBotService 的路径在进程启动期，Redis 晚几秒
// 就绪不应该拖慢启动。真正的连接在第一次 Load/Save 时建立，失败由
// FallbackSummaryStore 接住。
func newSummaryStore(cfg *config.Config) ctxmem.SummaryStore {
	fallback := ctxmem.NewMemorySummaryStore()
	if cfg == nil || cfg.Redis.Host == "" {
		return fallback
	}
	client := redis.NewClient(&redis.Options{
		Addr:         fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.DB,
		PoolSize:     cfg.Redis.PoolSize,
		DialTimeout:  time.Second,
		ReadTimeout:  500 * time.Millisecond,
		WriteTimeout: 500 * time.Millisecond,
	})
	return ctxmem.FallbackSummaryStore{
		Primary:  NewRedisSummaryStore(client),
		Fallback: fallback,
	}
}
