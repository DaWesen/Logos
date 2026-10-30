package gateway

import (
	"context"
	"sort"
	"strconv"
	"time"

	"Logos/internal/service/messaging/types"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
)

const (
	// presenceKeyPrefix 用户活跃会话集合的 Redis key 前缀
	presenceKeyPrefix = "gateway:presence:"
	// PresencePruneBatchSize 单次 HDel 最多剔除的失效会话数。
	//
	// 为什么要分批：一个被脚本刷过的账号/异常客户端可能在同一用户下积累上千条
	// 会话（每次重连都产生新 sessionID），一次性 HDel 会让单条 Redis 命令的
	// 参数包过大、阻塞 Redis 单线程执行，进而拖慢同节点上所有 presence 读写。
	// 分批后每次命令大小可控，代价只是多几次往返（失效剔除本身是低频路径）。
	PresencePruneBatchSize = 200
)

// presenceStaleAfter 会话多久没有心跳/活动即视为失效。
// 数值统一取自 connection_lifecycle.go 的 PresenceStaleAfter：它必须大于
// WS 读超时与 TCP 读超时，保证正常连接一定能在失效前续期；
// 节点宕机遗留的会话最迟在该窗口后自动剔除。
const presenceStaleAfter = PresenceStaleAfter

// presenceHashStore 只依赖哈希操作，便于用内存实现单测 RedisPresenceStore。
// cache.Cache 天然满足该接口。
type presenceHashStore interface {
	HSet(ctx context.Context, key, field string, value interface{}) error
	HGetAll(ctx context.Context, key string) (map[string]string, error)
	HDel(ctx context.Context, key string, fields []string) error
}

// PresenceStore 维护「用户当前有哪些活跃会话」的分布式视图。
// Touch 返回操作后的活跃会话数，以及本次是否是「新登记会话」（false 表示会话原本就在）。
// Remove 返回剩余活跃会话数，以及该会话是否真实存在（用于抑制未知会话注销触发的误判离线）。
type PresenceStore interface {
	Touch(ctx context.Context, userID, sessionID string, now time.Time) (active int64, added bool, err error)
	Remove(ctx context.Context, userID, sessionID string, now time.Time) (active int64, removed bool, err error)
}

// RedisPresenceStore 用 Redis 哈希保存 userID -> {sessionID: lastSeenMillis}。
// 之所以用哈希而不是集合：集合没有成员级过期时间，节点崩溃遗留的会话会永久驻留，
// 用户会永远显示在线；哈希可以记录每个会话的最近活跃时间，读时顺手剔除过期成员。
type RedisPresenceStore struct {
	store presenceHashStore
}

func NewRedisPresenceStore(store presenceHashStore) *RedisPresenceStore {
	return &RedisPresenceStore{store: store}
}

func presenceKey(userID string) string {
	return presenceKeyPrefix + userID
}

// Touch 登记会话并续期（连接建立、心跳、pong 都调用）。
// added=false 表示该会话早已登记（重复上线/心跳），调用方不应据此重复发布上线事件。
func (s *RedisPresenceStore) Touch(ctx context.Context, userID, sessionID string, now time.Time) (int64, bool, error) {
	key := presenceKey(userID)
	fields, err := s.store.HGetAll(ctx, key)
	if err != nil {
		return 0, false, err
	}
	_, existed := fields[sessionID]

	if err := s.store.HSet(ctx, key, sessionID, now.UnixMilli()); err != nil {
		return 0, existed, err
	}

	fields[sessionID] = strconv.FormatInt(now.UnixMilli(), 10)
	return s.pruneAndCount(ctx, key, fields, now), !existed, nil
}

// Remove 注销会话；removed=false 表示该会话本就不在集合里（重复注销）。
func (s *RedisPresenceStore) Remove(ctx context.Context, userID, sessionID string, now time.Time) (int64, bool, error) {
	key := presenceKey(userID)
	fields, err := s.store.HGetAll(ctx, key)
	if err != nil {
		return 0, false, err
	}
	_, removed := fields[sessionID]
	// 先从事务快照里删掉，再计数，否则会把刚注销的会话算进剩余活跃会话数
	delete(fields, sessionID)

	if err := s.store.HDel(ctx, key, []string{sessionID}); err != nil {
		return 0, removed, err
	}
	return s.pruneAndCount(ctx, key, fields, now), removed, nil
}

// classifyPresence 把哈希快照分成「仍活跃」与「已失效」两类。
//
// 抽成纯函数的原因：这是 presence 正确性的唯一判定点，必须能被穷举测试。
// 规则：
//   - 时间戳解析失败视为失效：脏数据不能让用户永久在线（fail-closed 更安全，
//     代价只是该会话被当成离线，下次心跳会重新登记）；
//   - ts < cutoff 视为失效；ts == cutoff 仍算活跃 —— 边界上用严格小于，
//     与「超过窗口才失效」的语义一致，避免时钟精度差异误杀刚续期的会话。
//
// 返回值 stale 按 sessionID 排序：分批 HDel 与日志需要稳定顺序，否则 map
// 遍历顺序会让同样的输入产生不同的删除批次，测试无法断言。
func classifyPresence(fields map[string]string, now time.Time) (active int64, stale []string) {
	cutoff := now.Add(-presenceStaleAfter).UnixMilli()
	if len(fields) == 0 {
		return 0, nil
	}
	stale = make([]string, 0, len(fields))
	for field, value := range fields {
		ts, err := strconv.ParseInt(value, 10, 64)
		if err != nil || ts < cutoff {
			stale = append(stale, field)
			continue
		}
		active++
	}
	sort.Strings(stale)
	return active, stale
}

// pruneAndCount 剔除超过 presenceStaleAfter 未续期的会话，返回剩余活跃会话数。
// 失效会话按 PresencePruneBatchSize 分批删除，避免单条 HDel 参数包过大。
func (s *RedisPresenceStore) pruneAndCount(ctx context.Context, key string, fields map[string]string, now time.Time) int64 {
	active, stale := classifyPresence(fields, now)
	if len(stale) == 0 {
		return active
	}

	removed := 0
	for start := 0; start < len(stale); start += PresencePruneBatchSize {
		end := start + PresencePruneBatchSize
		if end > len(stale) {
			end = len(stale)
		}
		batch := stale[start:end]
		if err := s.store.HDel(ctx, key, batch); err != nil {
			logger.Warn("presence: 剔除过期会话失败",
				logger.IntField("count", len(batch)),
				logger.IntField("removed_before_error", removed),
				logger.ErrorField(err))
			// 失败即停止：继续删后续批次只会在 Redis 故障时放大压力，
			// 已删成功的部分不会回滚，下一次操作会重新计算剩余失效会话。
			break
		}
		removed += len(batch)
	}

	if removed > 0 {
		obs.RecordPresencePruned(ctx, removed)
	}
	return active
}

// PresencePublisher 发布在线状态事件；*types.EventBus 满足该接口，单测可注入假实现。
type PresencePublisher interface {
	PublishPresenceEvent(ctx context.Context, event *types.UserPresenceEvent) error
}

// PresenceTracker 把「每连接一条 presence 事件」收敛为「用户级跃迁事件」：
// 首个会话上线才发 online，最后一个会话下线才发 offline。
// 会话集合放在 Redis，因此多节点部署下同样正确。
type PresenceTracker struct {
	store     PresenceStore
	publisher PresencePublisher
	now       func() time.Time
}

func NewPresenceTracker(store PresenceStore, publisher PresencePublisher) *PresenceTracker {
	return &PresenceTracker{store: store, publisher: publisher, now: time.Now}
}

// Online 登记会话；只有该用户全网首个活跃会话才发布 online。
// 存储不可用时 fail-open：按旧的单连接语义发布，宁可多发也不丢在线状态。
func (t *PresenceTracker) Online(ctx context.Context, userID, deviceID, sessionID string) {
	if t == nil || userID == "" || sessionID == "" {
		return
	}

	active, added, err := t.store.Touch(ctx, userID, sessionID, t.now())
	if err != nil {
		logger.Warn("presence: 记录上线会话失败，降级为单连接语义",
			logger.StringField("user_id", userID),
			logger.ErrorField(err))
		t.publish(ctx, userID, deviceID, true)
		return
	}

	// 重复登记同一会话，或该用户已有其它活跃会话：都不是「用户上线」跃迁
	if !added || active > 1 {
		return
	}
	t.publish(ctx, userID, deviceID, true)
}

// Offline 注销会话；只有该用户全网最后一个活跃会话才发布 offline。
// 重复注销（会话本就不存在）直接忽略；存储不可用时 fail-open，按旧的单连接语义发布。
func (t *PresenceTracker) Offline(ctx context.Context, userID, deviceID, sessionID string) {
	if t == nil || userID == "" || sessionID == "" {
		return
	}

	active, removed, err := t.store.Remove(ctx, userID, sessionID, t.now())
	if err != nil {
		logger.Warn("presence: 注销上线会话失败，降级为单连接语义",
			logger.StringField("user_id", userID),
			logger.ErrorField(err))
		t.publish(ctx, userID, deviceID, false)
		return
	}

	if !removed || active > 0 {
		return
	}
	t.publish(ctx, userID, deviceID, false)
}

// Touch 心跳续期，防止存活连接被当作过期会话剔除；不发布任何事件。
func (t *PresenceTracker) Touch(ctx context.Context, userID, sessionID string) {
	if t == nil || userID == "" || sessionID == "" {
		return
	}
	if _, _, err := t.store.Touch(ctx, userID, sessionID, t.now()); err != nil {
		logger.Debug("presence: 心跳续期失败",
			logger.StringField("user_id", userID),
			logger.ErrorField(err))
	}
}

func (t *PresenceTracker) publish(ctx context.Context, userID, deviceID string, online bool) {
	if t.publisher == nil {
		return
	}

	event := &types.UserPresenceEvent{
		UserID:    userID,
		DeviceID:  deviceID,
		Online:    online,
		Timestamp: t.now(),
	}
	if err := t.publisher.PublishPresenceEvent(ctx, event); err != nil {
		logger.Warn("presence: 发布在线状态事件失败",
			logger.StringField("user_id", userID),
			logger.BoolField("online", online),
			logger.ErrorField(err))
	}
}
