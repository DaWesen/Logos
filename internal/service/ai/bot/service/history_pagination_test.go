package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	botmodel "Logos/internal/service/ai/bot/model"
)

func descMessages(n int, content string) []*botmodel.Message {
	base := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	msgs := make([]*botmodel.Message, 0, n)
	for i := 0; i < n; i++ {
		msgs = append(msgs, &botmodel.Message{
			ID:        fmt.Sprintf("m%03d", i),
			Content:   content,
			CreatedAt: base.Add(-time.Duration(i) * time.Second),
		})
	}
	return msgs
}

// fakePager 按 created_at DESC 返回一页；语义与 dao.GetMessages 一致（严格 <）。
func fakePager(data []*botmodel.Message) messagePageFetcher {
	return func(before *time.Time, limit int) ([]*botmodel.Message, error) {
		out := make([]*botmodel.Message, 0, limit)
		for _, m := range data {
			if before != nil && !m.CreatedAt.Before(*before) {
				continue
			}
			out = append(out, m)
			if len(out) == limit {
				break
			}
		}
		return out, nil
	}
}

func TestCollectHistoryPages_GoesBeyondFirstPage(t *testing.T) {
	msgs := descMessages(250, "")
	// 每条空消息估算 50 token：第一页 500 未达标，第二页 950 超过 900 即停。
	// 游标 +1ns 会把上一页最后一条重新取回（同时间戳保护），由 ID 去重后
	// 第二页净增 9 条，因此这里是 19 而不是 20。
	got, err := collectHistoryPages(fakePager(msgs), 10, 500, 900)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 19 {
		t.Fatalf("should fetch past the first page until token target, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].CreatedAt.Before(got[i].CreatedAt) {
			t.Fatalf("result must stay created_at DESC at index %d", i)
		}
	}
	if got[0].ID != "m000" || got[len(got)-1].ID != "m018" {
		t.Errorf("unexpected window: first=%s last=%s", got[0].ID, got[len(got)-1].ID)
	}
}

func TestCollectHistoryPages_RespectsMaxMessages(t *testing.T) {
	msgs := descMessages(100, "")
	got, err := collectHistoryPages(fakePager(msgs), 10, 15, 1<<30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 15 {
		t.Fatalf("hard cap should stop fetching at 15, got %d", len(got))
	}
}

func TestCollectHistoryPages_StopsWhenPageMakesNoProgress(t *testing.T) {
	// 所有消息同一时间戳：+1ns 游标会把同一页再取一次，必须靠去重终止。
	msgs := descMessages(12, "")
	same := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	for _, m := range msgs {
		m.CreatedAt = same
	}

	calls := 0
	base := fakePager(msgs)
	fetch := func(before *time.Time, limit int) ([]*botmodel.Message, error) {
		calls++
		return base(before, limit)
	}

	got, err := collectHistoryPages(fetch, 5, 500, 1<<30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("should keep the first page and stop on duplicates, got %d", len(got))
	}
	if calls != 2 {
		t.Fatalf("duplicate page should trigger exactly one extra fetch, got %d calls", calls)
	}
}

func TestCollectHistoryPages_PropagatesFetchError(t *testing.T) {
	want := errors.New("db down")
	_, err := collectHistoryPages(func(*time.Time, int) ([]*botmodel.Message, error) {
		return nil, want
	}, 10, 500, 1000)
	if !errors.Is(err, want) {
		t.Fatalf("fetch error should reach the caller, got %v", err)
	}
}

func TestCollectHistoryPages_InvalidWindow(t *testing.T) {
	called := false
	got, err := collectHistoryPages(func(*time.Time, int) ([]*botmodel.Message, error) {
		called = true
		return nil, nil
	}, 0, 500, 1000)
	if err != nil || got != nil {
		t.Fatalf("invalid window should be a no-op, got %v, %v", got, err)
	}
	if called {
		t.Fatal("invalid window must not hit the repository")
	}
}
