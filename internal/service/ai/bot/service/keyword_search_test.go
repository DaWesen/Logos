package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestKeywordSearchCoolingDown(t *testing.T) {
	now := time.Now().Unix()

	if keywordSearchCoolingDown(0, now) {
		t.Fatal("0 表示未熔断")
	}
	if !keywordSearchCoolingDown(now+10, now) {
		t.Fatal("窗口内应判定为熔断")
	}
	if keywordSearchCoolingDown(now, now) {
		t.Fatal("到期应放行（半开）")
	}
	if keywordSearchCoolingDown(now-1, now) {
		t.Fatal("过期的熔断窗口应放行")
	}
}

type failingSearchService struct {
	calls atomic.Int32
}

func (f *failingSearchService) Search(_ context.Context, _ string, _ int) ([]*SearchResultItem, error) {
	f.calls.Add(1)
	return nil, errors.New("search backend down")
}

// TestSearchKeyword_ErrorIsNotDisguisedAsEmpty 失败必须以 error 返回，
// 并进入短时熔断；窗口内不再打后端（旧实现是永久 bool 熔断 + 数据竞争）。
func TestSearchKeyword_ErrorIsNotDisguisedAsEmpty(t *testing.T) {
	svc := &failingSearchService{}
	a := &knowledgeSearchAdapter{searchService: svc}

	results, err := a.SearchKeyword(context.Background(), "q", 5)
	if err == nil {
		t.Fatalf("后端失败必须返回 error，got results=%v err=nil", results)
	}
	if results != nil {
		t.Fatalf("失败不应返回半成品结果: %v", results)
	}

	downUntil := a.searchDownUntil.Load()
	if downUntil <= time.Now().Unix() {
		t.Fatalf("失败后应写入熔断窗口，got %d", downUntil)
	}

	// 熔断窗口内：直接跳过，不打后端、也不算失败
	results, err = a.SearchKeyword(context.Background(), "q2", 5)
	if err != nil || results != nil {
		t.Fatalf("熔断窗口内应静默跳过，got results=%v err=%v", results, err)
	}
	if n := svc.calls.Load(); n != 1 {
		t.Fatalf("熔断窗口内不应再调用后端，calls=%d", n)
	}
}

type stubSearchService struct {
	items []*SearchResultItem
}

func (s *stubSearchService) Search(_ context.Context, _ string, _ int) ([]*SearchResultItem, error) {
	return s.items, nil
}

// TestSearchKeyword_SuccessClearsCooldown 成功要清零熔断窗口，否则一次抖动会顺延。
func TestSearchKeyword_SuccessClearsCooldown(t *testing.T) {
	a := &knowledgeSearchAdapter{
		searchService:   &stubSearchService{},
		searchDownUntil: atomic.Int64{},
	}
	a.searchDownUntil.Store(time.Now().Add(time.Minute).Unix())

	// 人为过期后放行（半开），成功再把窗口清掉
	a.searchDownUntil.Store(time.Now().Unix() - 1)
	results, err := a.SearchKeyword(context.Background(), "q", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("empty backend should yield empty results, got %v", results)
	}
	if a.searchDownUntil.Load() != 0 {
		t.Fatalf("成功后应清零熔断窗口，got %d", a.searchDownUntil.Load())
	}
}
