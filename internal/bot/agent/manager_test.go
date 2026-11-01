package agent

import (
	"context"
	"testing"
	"time"

	"Logos/pkg/eino"

	"github.com/cloudwego/eino/schema"
)

type fakeAgent struct{ id string }

func (f *fakeAgent) Chat(context.Context, string) (string, error) { return "", nil }

func (f *fakeAgent) ChatStream(context.Context, string, func(string) error) error { return nil }

func (f *fakeAgent) ChatWithHistory(context.Context, []*schema.Message) (string, error) {
	return "", nil
}

func (f *fakeAgent) ChatStreamWithHistory(context.Context, []*schema.Message, func(string) error) error {
	return nil
}

func (f *fakeAgent) GetName() string         { return f.id }
func (f *fakeAgent) GetDescription() string  { return "" }
func (f *fakeAgent) GetSystemPrompt() string { return "" }

func fastBuilder(cfg *AgentConfig, _ *eino.EinoManager) (BotAgent, error) {
	return &fakeAgent{id: cfg.ID}, nil
}

// TestCreateAgent_DoesNotHoldLockWhileBuilding 构造 Agent（读 prompt、装配模型与工具）
// 不能在写锁内进行：旧实现会让 GetAgent 这类读操作一起排队，表现为管理接口整体卡住。
func TestCreateAgent_DoesNotHoldLockWhileBuilding(t *testing.T) {
	mgr := NewAgentManager(nil)
	mgr.buildAgent = fastBuilder

	if _, err := mgr.CreateAgent(&AgentConfig{ID: "existing", Name: "existing"}); err != nil {
		t.Fatalf("prepare existing agent: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	mgr.buildAgent = func(cfg *AgentConfig, _ *eino.EinoManager) (BotAgent, error) {
		close(started)
		<-release
		return &fakeAgent{id: cfg.ID}, nil
	}

	createDone := make(chan error, 1)
	go func() {
		_, err := mgr.CreateAgent(&AgentConfig{ID: "slow", Name: "slow"})
		createDone <- err
	}()

	<-started // 构造已经开始

	readDone := make(chan error, 1)
	go func() {
		_, err := mgr.GetAgent("existing")
		readDone <- err
	}()

	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("GetAgent(existing) failed: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		close(release) // 避免测试结束时 goroutine 泄漏
		t.Fatal("GetAgent 被构造过程阻塞：慢构造仍持有写锁")
	}

	close(release)
	if err := <-createDone; err != nil {
		t.Fatalf("CreateAgent(slow) failed: %v", err)
	}
	if _, err := mgr.GetAgent("slow"); err != nil {
		t.Fatalf("新建的 agent 未登记: %v", err)
	}
}

// TestGetOrCreateAgent_ConcurrentSameID 并发首次创建同一 ID：构造可能发生两次，
// 但最终只能有一个实例被缓存，两个调用方必须拿到同一个对象。
func TestGetOrCreateAgent_ConcurrentSameID(t *testing.T) {
	mgr := NewAgentManager(nil)
	mgr.buildAgent = func(cfg *AgentConfig, _ *eino.EinoManager) (BotAgent, error) {
		time.Sleep(20 * time.Millisecond)
		return &fakeAgent{id: cfg.ID}, nil
	}

	results := make(chan BotAgent, 2)
	for i := 0; i < 2; i++ {
		go func() {
			a, err := mgr.GetOrCreateAgent(&AgentConfig{ID: "dup", Name: "dup"})
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			results <- a
		}()
	}

	a1, a2 := <-results, <-results
	if a1 != a2 {
		t.Fatalf("并发创建返回了不同实例: %p vs %p", a1, a2)
	}

	got, err := mgr.GetAgent("dup")
	if err != nil || got != a1 {
		t.Fatalf("缓存实例与返回值不一致: %v", err)
	}
}

// TestCreateAgent_DuplicateRejected 同 ID 重复创建必须报错（不是静默覆盖）。
func TestCreateAgent_DuplicateRejected(t *testing.T) {
	mgr := NewAgentManager(nil)
	mgr.buildAgent = fastBuilder

	if _, err := mgr.CreateAgent(&AgentConfig{ID: "a"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := mgr.CreateAgent(&AgentConfig{ID: "a"}); err == nil {
		t.Fatal("重复创建应返回 error")
	}
}
