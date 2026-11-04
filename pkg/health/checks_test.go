package health

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubCache struct {
	pingErr error
}

func (s stubCache) Set(context.Context, string, interface{}, time.Duration) error { return nil }
func (s stubCache) Get(context.Context, string) (string, error)                   { return "", nil }
func (s stubCache) Delete(context.Context, string) error                          { return nil }
func (s stubCache) Exists(context.Context, string) (bool, error)                  { return false, nil }
func (s stubCache) Expire(context.Context, string, time.Duration) error           { return nil }
func (s stubCache) MSet(context.Context, map[string]interface{}) error            { return nil }
func (s stubCache) MGet(context.Context, []string) (map[string]string, error)     { return nil, nil }
func (s stubCache) MDelete(context.Context, []string) error                       { return nil }
func (s stubCache) HSet(context.Context, string, string, interface{}) error       { return nil }
func (s stubCache) HGet(context.Context, string, string) (string, error)          { return "", nil }
func (s stubCache) HMSet(context.Context, string, map[string]interface{}) error   { return nil }
func (s stubCache) HGetAll(context.Context, string) (map[string]string, error)    { return nil, nil }
func (s stubCache) HDel(context.Context, string, []string) error                  { return nil }
func (s stubCache) LPush(context.Context, string, ...interface{}) error           { return nil }
func (s stubCache) RPush(context.Context, string, ...interface{}) error           { return nil }
func (s stubCache) LRange(context.Context, string, int64, int64) ([]string, error) {
	return nil, nil
}
func (s stubCache) LLen(context.Context, string) (int64, error)        { return 0, nil }
func (s stubCache) SAdd(context.Context, string, ...interface{}) error { return nil }
func (s stubCache) SRem(context.Context, string, ...interface{}) error { return nil }
func (s stubCache) SMembers(context.Context, string) ([]string, error) { return nil, nil }
func (s stubCache) SIsMember(context.Context, string, interface{}) (bool, error) {
	return false, nil
}
func (s stubCache) ZAdd(context.Context, string, float64, interface{}) error { return nil }
func (s stubCache) ZRem(context.Context, string, ...interface{}) error       { return nil }
func (s stubCache) ZRange(context.Context, string, int64, int64) ([]string, error) {
	return nil, nil
}
func (s stubCache) ZCount(context.Context, string, float64, float64) (int64, error) {
	return 0, nil
}
func (s stubCache) ZScore(context.Context, string, string) (float64, error) { return 0, nil }
func (s stubCache) Incr(context.Context, string) (int64, error)             { return 0, nil }
func (s stubCache) IncrBy(context.Context, string, int64) (int64, error)    { return 0, nil }
func (s stubCache) Decr(context.Context, string) (int64, error)             { return 0, nil }
func (s stubCache) DecrBy(context.Context, string, int64) (int64, error)    { return 0, nil }
func (s stubCache) EvalSha(context.Context, string, []string, ...interface{}) (interface{}, error) {
	return nil, nil
}
func (s stubCache) PipelineExec(context.Context, []struct {
	Cmd  string
	Args []interface{}
}) ([]interface{}, error) {
	return nil, nil
}
func (s stubCache) Ping(context.Context) error { return s.pingErr }
func (s stubCache) Close() error               { return nil }

func TestRedisCheck_NilCacheFails(t *testing.T) {
	if err := Redis(nil)(context.Background()); err == nil {
		t.Fatal("nil cache 必须返回错误，而不是静默健康")
	}
}

func TestRedisCheck_PropagatesPingError(t *testing.T) {
	err := Redis(stubCache{pingErr: errors.New("connection refused")})(context.Background())
	if err == nil {
		t.Fatal("Ping 失败必须上报")
	}
}

func TestRedisCheck_Healthy(t *testing.T) {
	if err := Redis(stubCache{})(context.Background()); err != nil {
		t.Fatalf("健康缓存不应报错: %v", err)
	}
}

func TestPostgresCheck_NilDBFails(t *testing.T) {
	if err := Postgres(nil)(context.Background()); err == nil {
		t.Fatal("nil db 必须返回错误")
	}
}

func TestTimeout_WrapsCheck(t *testing.T) {
	slow := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	err := Timeout(20*time.Millisecond, slow)(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestFunc_NilIsHealthy(t *testing.T) {
	if err := Func(nil)(context.Background()); err != nil {
		t.Fatalf("nil func 应视为健康: %v", err)
	}
}
