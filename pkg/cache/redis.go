package cache

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"Logos/config"

	"github.com/redis/go-redis/v9"
)

// PipelineCmd 是 PipelineExec 的单条命令描述。
//
// 用命名类型替代匿名结构体，让校验函数、接口与实现共享同一类型；
// 两条约束必须一致，否则调用方传 []struct{...} 会因为类型不同而编译失败。
type PipelineCmd = struct {
	Cmd  string
	Args []interface{}
}

// Redis 启动重连参数。
//
// 原实现固定重试 30 次，每次「3s ping 超时 + 2s sleep」，最坏阻塞启动
// 30×(3+2)=150s：容器编排的健康检查早就判定启动失败并重启，Redis 恢复后
// 进程也未必还在。现在改成指数退避 + 更少次数：
//   - 200ms 起步、逐次翻倍、封顶 2s，10 次合计约 13s，覆盖绝大多数「Redis 比应用晚几秒就绪」的场景；
//   - 最终失败时关闭已创建的 client，避免连接池与后台 goroutine 泄漏。
const (
	redisStartupAttempts = 10
	redisPingTimeout     = 3 * time.Second
	redisRetryBaseDelay  = 200 * time.Millisecond
	redisRetryMaxDelay   = 2 * time.Second
)

// StartupRetryDelay 返回第 attempt 次失败后应等待的时长（attempt 从 1 开始）。
// 序列：200ms → 400ms → 800ms → 1.6s → 2s → 2s ……
func StartupRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := redisRetryBaseDelay
	for i := 1; i < attempt; i++ {
		if d > redisRetryMaxDelay/2 {
			return redisRetryMaxDelay
		}
		d *= 2
	}
	return d
}

// ErrUnsupportedPipelineCommand 表示 PipelineExec 收到了不支持的命令。
//
// 原实现遇到未知命令会把 nil 塞进结果切片（与「命令执行成功但返回 nil」无法区分），
// 调用方以为整段管道执行成功，实际上命令被静默丢弃。
var ErrUnsupportedPipelineCommand = errors.New("cache: pipeline 含不支持的命令")

// ValidatePipelineCommands 在真正下发前校验命令与参数个数。
//
// 参数越界（如 ZADD 只给 2 个参数）在旧实现里会直接 index out of range panic，
// 而现在明确返回错误，调用方可以降级为逐条命令执行。
func ValidatePipelineCommands(cmds []PipelineCmd) error {
	for i, cmd := range cmds {
		need := 0
		switch cmd.Cmd {
		case "ZREMRANGEBYSCORE", "ZADD", "ZRANGE":
			need = 3
		case "PEXPIRE":
			need = 2
		default:
			return fmt.Errorf("%w: 第 %d 条 %q", ErrUnsupportedPipelineCommand, i, cmd.Cmd)
		}
		if len(cmd.Args) < need {
			return fmt.Errorf("cache: 第 %d 条 %s 需要 %d 个参数，实际 %d 个", i, cmd.Cmd, need, len(cmd.Args))
		}
	}
	return nil
}

type Cache interface {
	//基本操作
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
	Get(ctx context.Context, key string) (string, error)
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
	Expire(ctx context.Context, key string, expiration time.Duration) error

	//批量操作
	MSet(ctx context.Context, values map[string]interface{}) error
	MGet(ctx context.Context, keys []string) (map[string]string, error)
	MDelete(ctx context.Context, keys []string) error

	//哈希操作
	HSet(ctx context.Context, key, field string, value interface{}) error
	HGet(ctx context.Context, key, field string) (string, error)
	HMSet(ctx context.Context, key string, values map[string]interface{}) error
	HGetAll(ctx context.Context, key string) (map[string]string, error)
	HDel(ctx context.Context, key string, fields []string) error

	//列表操作
	LPush(ctx context.Context, key string, values ...interface{}) error
	RPush(ctx context.Context, key string, values ...interface{}) error
	LRange(ctx context.Context, key string, start, stop int64) ([]string, error)
	LLen(ctx context.Context, key string) (int64, error)

	//集合操作
	SAdd(ctx context.Context, key string, members ...interface{}) error
	SRem(ctx context.Context, key string, members ...interface{}) error
	SMembers(ctx context.Context, key string) ([]string, error)
	SIsMember(ctx context.Context, key string, member interface{}) (bool, error)

	//有序集合操作
	ZAdd(ctx context.Context, key string, score float64, member interface{}) error
	ZRem(ctx context.Context, key string, members ...interface{}) error
	ZRange(ctx context.Context, key string, start, stop int64) ([]string, error)
	ZCount(ctx context.Context, key string, min, max float64) (int64, error)
	ZScore(ctx context.Context, key string, member string) (float64, error)

	//计数器操作
	Incr(ctx context.Context, key string) (int64, error)
	IncrBy(ctx context.Context, key string, value int64) (int64, error)
	Decr(ctx context.Context, key string) (int64, error)
	DecrBy(ctx context.Context, key string, value int64) (int64, error)

	//Lua 脚本执行
	EvalSha(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error)
	PipelineExec(ctx context.Context, cmds []PipelineCmd) ([]interface{}, error)

	//连接管理
	Ping(ctx context.Context) error
	Close() error
}

type RedisCache struct {
	client *redis.Client
}

// safeStringConvert 安全地将interface{}转换为string
func safeStringConvert(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case int, int8, int16, int32, int64:
		return fmt.Sprint(val)
	case uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(val)
	case float32, float64:
		return fmt.Sprint(val)
	case bool:
		return fmt.Sprint(val)
	default:
		return fmt.Sprint(val)
	}
}

// safeInt64Convert 安全地将interface{}转换为int64
func safeInt64Convert(v interface{}) int64 {
	switch val := v.(type) {
	case int:
		return int64(val)
	case int8:
		return int64(val)
	case int16:
		return int64(val)
	case int32:
		return int64(val)
	case int64:
		return val
	case uint:
		return int64(val)
	case uint8:
		return int64(val)
	case uint16:
		return int64(val)
	case uint32:
		return int64(val)
	case uint64:
		return int64(val)
	case float32:
		return int64(val)
	case float64:
		return int64(val)
	case string:
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			return i
		}
		return 0
	default:
		return 0
	}
}

// safeFloat64Convert 安全地将interface{}转换为float64
func safeFloat64Convert(v interface{}) float64 {
	switch val := v.(type) {
	case float32:
		return float64(val)
	case float64:
		return val
	case int:
		return float64(val)
	case int8:
		return float64(val)
	case int16:
		return float64(val)
	case int32:
		return float64(val)
	case int64:
		return float64(val)
	case string:
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
		return 0.0
	default:
		return 0.0
	}
}

var (
	cacheInstance Cache
	cacheOnce     sync.Once
)

func NewRedisCache() Cache {
	cacheOnce.Do(func() {
		cfg := config.GetConfig()
		cacheInstance, _ = InitRedis(cfg.Redis)
	})
	return cacheInstance
}

func InitRedis(redisConfig config.Redis) (Cache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", redisConfig.Host, redisConfig.Port),
		Password: redisConfig.Password,
		DB:       redisConfig.DB,
		PoolSize: redisConfig.PoolSize,
	})

	// 注意顺序：失败时必须先 Close client 再返回。redis.Client 内部有连接池
	// 与后台 goroutine，直接丢弃会让它们挂到进程退出。
	for attempt := 1; attempt <= redisStartupAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), redisPingTimeout)
		_, err := client.Ping(ctx).Result()
		cancel()
		if err == nil {
			fmt.Println("Redis连接成功")
			return &RedisCache{client: client}, nil
		}

		if attempt == redisStartupAttempts {
			_ = client.Close()
			return nil, fmt.Errorf("failed to connect redis after %d attempts: %w", redisStartupAttempts, err)
		}

		delay := StartupRetryDelay(attempt)
		fmt.Printf("Redis连接失败(第%d次)，%s 后重试: %v\n", attempt, delay, err)
		time.Sleep(delay)
	}

	_ = client.Close()
	return nil, errors.New("failed to connect redis")
}

// 设置键值对
func (c *RedisCache) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return c.client.Set(ctx, key, value, expiration).Err()
}

// 获取值
func (c *RedisCache) Get(ctx context.Context, key string) (string, error) {
	return c.client.Get(ctx, key).Result()
}

// 删除键
func (c *RedisCache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}

// 设置过期时间
func (c *RedisCache) Expire(ctx context.Context, key string, expiration time.Duration) error {
	return c.client.Expire(ctx, key, expiration).Err()
}

// 检查键是否存在
func (c *RedisCache) Exists(ctx context.Context, key string) (bool, error) {
	result, err := c.client.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return result > 0, nil
}

// 批量设置
func (c *RedisCache) MSet(ctx context.Context, values map[string]interface{}) error {
	return c.client.MSet(ctx, values).Err()
}

// 批量获取
func (c *RedisCache) MGet(ctx context.Context, keys []string) (map[string]string, error) {
	result, err := c.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	values := make(map[string]string)
	for i, key := range keys {
		if result[i] != nil {
			// 安全转换：支持多种类型转换为字符串
			switch v := result[i].(type) {
			case string:
				values[key] = v
			case int, int8, int16, int32, int64:
				values[key] = fmt.Sprint(v)
			case uint, uint8, uint16, uint32, uint64:
				values[key] = fmt.Sprint(v)
			case float32, float64:
				values[key] = fmt.Sprint(v)
			case bool:
				values[key] = fmt.Sprint(v)
			default:
				values[key] = fmt.Sprint(v)
			}
		}
	}
	return values, nil
}

// 批量删除
func (c *RedisCache) MDelete(ctx context.Context, keys []string) error {
	return c.client.Del(ctx, keys...).Err()
}

// 设置哈希字段
func (c *RedisCache) HSet(ctx context.Context, key, field string, value interface{}) error {
	return c.client.HSet(ctx, key, field, value).Err()
}

// 获取哈希字段
func (c *RedisCache) HGet(ctx context.Context, key, field string) (string, error) {
	return c.client.HGet(ctx, key, field).Result()
}

// 批量设置哈希字段
func (c *RedisCache) HMSet(ctx context.Context, key string, values map[string]interface{}) error {
	return c.client.HMSet(ctx, key, values).Err()
}

// 获取哈希所有字段
func (c *RedisCache) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return c.client.HGetAll(ctx, key).Result()
}

// 删除哈希字段
func (c *RedisCache) HDel(ctx context.Context, key string, fields []string) error {
	return c.client.HDel(ctx, key, fields...).Err()
}

// 左侧推入列表
func (c *RedisCache) LPush(ctx context.Context, key string, values ...interface{}) error {
	return c.client.LPush(ctx, key, values...).Err()
}

// 右侧推入列表
func (c *RedisCache) RPush(ctx context.Context, key string, values ...interface{}) error {
	return c.client.RPush(ctx, key, values...).Err()
}

// 获取列表范围
func (c *RedisCache) LRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return c.client.LRange(ctx, key, start, stop).Result()
}

// 获取列表长度
func (c *RedisCache) LLen(ctx context.Context, key string) (int64, error) {
	return c.client.LLen(ctx, key).Result()
}

// 添加集合成员
func (c *RedisCache) SAdd(ctx context.Context, key string, members ...interface{}) error {
	return c.client.SAdd(ctx, key, members...).Err()
}

// 删除集合成员
func (c *RedisCache) SRem(ctx context.Context, key string, members ...interface{}) error {
	return c.client.SRem(ctx, key, members...).Err()
}

// 获取集合所有成员
func (c *RedisCache) SMembers(ctx context.Context, key string) ([]string, error) {
	return c.client.SMembers(ctx, key).Result()
}

// 检查是否为集合成员
func (c *RedisCache) SIsMember(ctx context.Context, key string, member interface{}) (bool, error) {
	return c.client.SIsMember(ctx, key, member).Result()
}

// 添加有序集合成员
func (c *RedisCache) ZAdd(ctx context.Context, key string, score float64, member interface{}) error {
	return c.client.ZAdd(ctx, key, redis.Z{Score: score, Member: member}).Err()
}

// 删除有序集合成员
func (c *RedisCache) ZRem(ctx context.Context, key string, members ...interface{}) error {
	return c.client.ZRem(ctx, key, members...).Err()
}

// 获取有序集合范围
func (c *RedisCache) ZRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return c.client.ZRange(ctx, key, start, stop).Result()
}

// 有序集合计数
func (c *RedisCache) ZCount(ctx context.Context, key string, min, max float64) (int64, error) {
	return c.client.ZCount(ctx, key, fmt.Sprintf("%f", min), fmt.Sprintf("%f", max)).Result()
}

// 获取有序集合成员分数
func (c *RedisCache) ZScore(ctx context.Context, key string, member string) (float64, error) {
	return c.client.ZScore(ctx, key, member).Result()
}

// 递增计数器
func (c *RedisCache) Incr(ctx context.Context, key string) (int64, error) {
	return c.client.Incr(ctx, key).Result()
}

// 递增指定值
func (c *RedisCache) IncrBy(ctx context.Context, key string, value int64) (int64, error) {
	return c.client.IncrBy(ctx, key, value).Result()
}

// 递减计数器
func (c *RedisCache) Decr(ctx context.Context, key string) (int64, error) {
	return c.client.Decr(ctx, key).Result()
}

// 递减指定值
func (c *RedisCache) DecrBy(ctx context.Context, key string, value int64) (int64, error) {
	return c.client.DecrBy(ctx, key, value).Result()
}

// 执行 Lua 脚本
func (c *RedisCache) EvalSha(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error) {
	return c.client.Eval(ctx, script, keys, args...).Result()
}

// 管道执行多个命令
func (c *RedisCache) PipelineExec(ctx context.Context, cmds []PipelineCmd) ([]interface{}, error) {
	// 先校验再下发：未知命令/参数缺失必须在本地暴露，不能塞 nil 当成功
	if err := ValidatePipelineCommands(cmds); err != nil {
		return nil, err
	}

	pipe := c.client.Pipeline()

	results := make([]interface{}, 0, len(cmds))
	for _, cmd := range cmds {
		switch cmd.Cmd {
		case "ZREMRANGEBYSCORE":
			results = append(results, pipe.ZRemRangeByScore(ctx, safeStringConvert(cmd.Args[0]), safeStringConvert(cmd.Args[1]), safeStringConvert(cmd.Args[2])))
		case "ZADD":
			results = append(results, pipe.ZAdd(ctx, safeStringConvert(cmd.Args[0]), redis.Z{Score: safeFloat64Convert(cmd.Args[1]), Member: cmd.Args[2]}))
		case "ZRANGE":
			results = append(results, pipe.ZRange(ctx, safeStringConvert(cmd.Args[0]), safeInt64Convert(cmd.Args[1]), safeInt64Convert(cmd.Args[2])))
		case "PEXPIRE":
			results = append(results, pipe.Expire(ctx, safeStringConvert(cmd.Args[0]), time.Duration(safeInt64Convert(cmd.Args[1]))*time.Millisecond))
		}
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return nil, err
	}

	return results, nil
}

// 测试连接
func (c *RedisCache) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

// 关闭连接
func (c *RedisCache) Close() error {
	return c.client.Close()
}

// RawClient 暴露底层 *redis.Client，供 PubSub 等高级特性复用连接池
// 调用方不应关闭返回的 client（由 RedisCache.Close 统一管理）
func (c *RedisCache) RawClient() *redis.Client {
	return c.client
}

func GenerateUserKey(userID int64) string {
	return fmt.Sprintf("user:%d", userID)
}

func GenerateTokenKey(token string) string {
	return fmt.Sprintf("token:%s", token)
}
