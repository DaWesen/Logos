package cache

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"Logos/config"

	"github.com/redis/go-redis/v9"
)

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
	PipelineExec(ctx context.Context, cmds []struct {
		Cmd  string
		Args []interface{}
	}) ([]interface{}, error)

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

	for i := 0; i < 30; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, err := client.Ping(ctx).Result()
		cancel()
		if err == nil {
			fmt.Println("Redis连接成功")
			return &RedisCache{client: client}, nil
		}
		fmt.Printf("Redis连接失败(第%d次)，重试中...: %v\n", i+1, err)
		time.Sleep(2 * time.Second)
	}

	return nil, fmt.Errorf("failed to connect redis after 30 attempts")
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
func (c *RedisCache) PipelineExec(ctx context.Context, cmds []struct {
	Cmd  string
	Args []interface{}
}) ([]interface{}, error) {
	pipe := c.client.Pipeline()

	var results []interface{}
	for _, cmd := range cmds {
		switch cmd.Cmd {
		case "ZREMRANGEBYSCORE":
			results = append(results, pipe.ZRemRangeByScore(ctx, safeStringConvert(cmd.Args[0]), safeStringConvert(cmd.Args[1]), safeStringConvert(cmd.Args[2])))
		case "ZADD":
			results = append(results, pipe.ZAdd(ctx, safeStringConvert(cmd.Args[0]), redis.Z{Score: safeFloat64Convert(cmd.Args[1]), Member: cmd.Args[2]}))
		case "ZRANGE":
			results = append(results, pipe.ZRange(ctx, safeStringConvert(cmd.Args[0]), safeInt64Convert(cmd.Args[1]), safeInt64Convert(cmd.Args[2])))
		case "PEXPIRE":
			results = append(results, pipe.Expire(ctx, safeStringConvert(cmd.Args[0]), time.Duration(safeInt64Convert(cmd.Args[1]))*time.Microsecond))
		default:
			results = append(results, nil)
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

func GenerateUserKey(userID int64) string {
	return fmt.Sprintf("user:%d", userID)
}

func GenerateTokenKey(token string) string {
	return fmt.Sprintf("token:%s", token)
}
