package scache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"Logos/pkg/cache"
)

var errFakeMiss = errors.New("fake redis: miss")

// fakeRedis 只实现 scache 真正用到的命令；其余方法显式返回错误，
// 避免测试里出现「以为验证了 Redis 行为，其实什么都没发生」。
type fakeRedis struct {
	mu       sync.Mutex
	hashes   map[string]map[string]string
	sets     map[string]map[string]struct{}
	zsets    map[string]map[string]float64
	strings  map[string]string
	counters map[string]int64
	expires  map[string]time.Duration
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{
		hashes:   make(map[string]map[string]string),
		sets:     make(map[string]map[string]struct{}),
		zsets:    make(map[string]map[string]float64),
		strings:  make(map[string]string),
		counters: make(map[string]int64),
		expires:  make(map[string]time.Duration),
	}
}

func (f *fakeRedis) Set(_ context.Context, key string, value interface{}, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.strings[key] = fmt.Sprint(value)
	return nil
}

func (f *fakeRedis) Get(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.strings[key]
	if !ok {
		return "", errFakeMiss
	}
	return v, nil
}

func (f *fakeRedis) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.hashes, key)
	delete(f.sets, key)
	delete(f.zsets, key)
	delete(f.strings, key)
	delete(f.counters, key)
	return nil
}

func (f *fakeRedis) Exists(_ context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.hashes[key]; ok {
		return true, nil
	}
	if _, ok := f.sets[key]; ok {
		return true, nil
	}
	if _, ok := f.strings[key]; ok {
		return true, nil
	}
	return false, nil
}

func (f *fakeRedis) Expire(_ context.Context, key string, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expires[key] = ttl
	return nil
}

func (f *fakeRedis) MSet(context.Context, map[string]interface{}) error { return errFakeUnsupported }
func (f *fakeRedis) MGet(context.Context, []string) (map[string]string, error) {
	return nil, errFakeUnsupported
}
func (f *fakeRedis) MDelete(context.Context, []string) error { return errFakeUnsupported }

func (f *fakeRedis) HSet(_ context.Context, key, field string, value interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hash(key)[field] = fmt.Sprint(value)
	return nil
}

func (f *fakeRedis) HGet(_ context.Context, key, field string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.hashes[key][field]
	if !ok {
		return "", errFakeMiss
	}
	return v, nil
}

func (f *fakeRedis) HMSet(_ context.Context, key string, values map[string]interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for field, value := range values {
		f.hash(key)[field] = fmt.Sprint(value)
	}
	return nil
}

func (f *fakeRedis) HGetAll(_ context.Context, key string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(f.hashes[key]))
	for field, value := range f.hashes[key] {
		out[field] = value
	}
	return out, nil
}

func (f *fakeRedis) HDel(_ context.Context, key string, fields []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, field := range fields {
		delete(f.hashes[key], field)
	}
	return nil
}

func (f *fakeRedis) LPush(context.Context, string, ...interface{}) error { return errFakeUnsupported }
func (f *fakeRedis) RPush(context.Context, string, ...interface{}) error { return errFakeUnsupported }
func (f *fakeRedis) LRange(context.Context, string, int64, int64) ([]string, error) {
	return nil, errFakeUnsupported
}
func (f *fakeRedis) LLen(context.Context, string) (int64, error) { return 0, errFakeUnsupported }

func (f *fakeRedis) SAdd(_ context.Context, key string, members ...interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, member := range members {
		f.set(key)[fmt.Sprint(member)] = struct{}{}
	}
	return nil
}

func (f *fakeRedis) SRem(_ context.Context, key string, members ...interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, member := range members {
		delete(f.sets[key], fmt.Sprint(member))
	}
	return nil
}

func (f *fakeRedis) SMembers(_ context.Context, key string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.sets[key]))
	for member := range f.sets[key] {
		out = append(out, member)
	}
	return out, nil
}

func (f *fakeRedis) SIsMember(_ context.Context, key string, member interface{}) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.sets[key][fmt.Sprint(member)]
	return ok, nil
}

func (f *fakeRedis) ZAdd(_ context.Context, key string, score float64, member interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.zsets[key] == nil {
		f.zsets[key] = make(map[string]float64)
	}
	f.zsets[key][fmt.Sprint(member)] = score
	return nil
}

func (f *fakeRedis) ZRem(_ context.Context, key string, members ...interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, member := range members {
		delete(f.zsets[key], fmt.Sprint(member))
	}
	return nil
}

func (f *fakeRedis) ZRange(context.Context, string, int64, int64) ([]string, error) {
	return nil, errFakeUnsupported
}
func (f *fakeRedis) ZCount(context.Context, string, float64, float64) (int64, error) {
	return 0, errFakeUnsupported
}
func (f *fakeRedis) ZScore(context.Context, string, string) (float64, error) {
	return 0, errFakeUnsupported
}

func (f *fakeRedis) Incr(_ context.Context, key string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counters[key]++
	return f.counters[key], nil
}

func (f *fakeRedis) IncrBy(_ context.Context, key string, value int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counters[key] += value
	return f.counters[key], nil
}

func (f *fakeRedis) Decr(context.Context, string) (int64, error) { return 0, errFakeUnsupported }
func (f *fakeRedis) DecrBy(context.Context, string, int64) (int64, error) {
	return 0, errFakeUnsupported
}

func (f *fakeRedis) EvalSha(_ context.Context, script string, keys []string, args ...interface{}) (interface{}, error) {
	if script != persistScript {
		return nil, errFakeUnsupported
	}
	if len(keys) != 5 || len(args) < 15 {
		return nil, errors.New("fake redis: bad persist script arguments")
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	id := fmt.Sprint(args[0])
	f.hash(keys[0])[id] = fmt.Sprint(args[1])
	if f.zsets[keys[1]] == nil {
		f.zsets[keys[1]] = make(map[string]float64)
	}
	f.zsets[keys[1]][id] = float64(toInt64(args[2]))
	f.hash(keys[2])[fmt.Sprint(args[4])] = id
	meta := f.hash(keys[3])
	meta["tenant"] = fmt.Sprint(args[5])
	meta["bot"] = fmt.Sprint(args[6])
	meta["user"] = fmt.Sprint(args[7])
	meta["kb"] = fmt.Sprint(args[8])
	meta["prompt"] = fmt.Sprint(args[9])
	meta["model"] = fmt.Sprint(args[10])
	meta["embed"] = fmt.Sprint(args[11])
	meta["created_at"] = fmt.Sprint(args[2])
	f.expires[keys[0]] = time.Duration(toInt64(args[3])) * time.Second
	f.expires[keys[1]] = time.Duration(toInt64(args[3])) * time.Second
	f.expires[keys[2]] = time.Duration(toInt64(args[13])) * time.Second
	f.expires[keys[3]] = time.Duration(toInt64(args[12])) * time.Second
	f.expires[keys[4]] = time.Duration(toInt64(args[12])) * time.Second
	f.set(keys[4])[fmt.Sprint(args[14])] = struct{}{}
	return int64(1), nil
}

func (f *fakeRedis) PipelineExec(context.Context, []cache.PipelineCmd) ([]interface{}, error) {
	return nil, errFakeUnsupported
}

func (f *fakeRedis) Ping(context.Context) error { return nil }
func (f *fakeRedis) Close() error               { return nil }

var errFakeUnsupported = errors.New("fake redis: unsupported command")

func (f *fakeRedis) hash(key string) map[string]string {
	if f.hashes[key] == nil {
		f.hashes[key] = make(map[string]string)
	}
	return f.hashes[key]
}

func (f *fakeRedis) set(key string) map[string]struct{} {
	if f.sets[key] == nil {
		f.sets[key] = make(map[string]struct{})
	}
	return f.sets[key]
}

func (f *fakeRedis) ttlOf(key string) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.expires[key]
}

func toInt64(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return 0
	}
}
