package service

import (
	"context"
	"errors"
	"testing"

	"Logos/internal/service/ai/recommend/model"
)

// TestSubmitFeedback_UsesDirectLookupNotFeed 反馈元信息必须按主键查，
// 不能借道推荐列表：列表会排除已产生历史的条目，而用户往往正是对
// 看过的条目点赞/点踩；缓存失效时还会顺带触发整条推荐生成链路。
func TestSubmitFeedback_UsesDirectLookupNotFeed(t *testing.T) {
	repo := &fakeRecommendRepo{
		itemByID: &model.RecommendationItem{ID: "item-1", Type: "article", Title: "真实标题"},
	}
	svc := &recommendServiceImpl{recommendRepo: repo}

	if err := svc.SubmitFeedback(context.Background(), "item-1", 42, "like", 0); err != nil {
		t.Fatalf("SubmitFeedback 返回错误: %v", err)
	}
	if repo.byIDCalls != 1 {
		t.Fatalf("应按主键查询 1 次，实际 %d 次", repo.byIDCalls)
	}
	if repo.calls != 0 {
		t.Fatalf("反馈不应触发推荐列表查询，实际调用 %d 次", repo.calls)
	}
	if len(repo.savedHistory) != 1 {
		t.Fatalf("应落库 1 条反馈，实际 %d 条", len(repo.savedHistory))
	}

	got := repo.savedHistory[0]
	if got.ItemType != "article" || got.Title != "真实标题" {
		t.Fatalf("反馈元信息 = (%s, %s), want (article, 真实标题)", got.ItemType, got.Title)
	}
	if got.Timestamp == 0 {
		t.Fatal("未传时间戳时应补当前时间")
	}
}

// TestSubmitFeedback_LookupFailureStillPersists 查询元信息失败不能丢掉反馈本身，
// 但落库内容必须是可识别的降级值。
func TestSubmitFeedback_LookupFailureStillPersists(t *testing.T) {
	repo := &fakeRecommendRepo{itemByIDErr: errors.New("db down")}
	svc := &recommendServiceImpl{recommendRepo: repo}

	if err := svc.SubmitFeedback(context.Background(), "item-2", 42, "dislike", 1700000000); err != nil {
		t.Fatalf("元信息查询失败时反馈仍应落库，实际返回 %v", err)
	}
	if len(repo.savedHistory) != 1 {
		t.Fatalf("应落库 1 条反馈，实际 %d 条", len(repo.savedHistory))
	}
	got := repo.savedHistory[0]
	if got.ItemType != "unknown" || got.Title != "item-2" {
		t.Fatalf("降级元信息 = (%s, %s), want (unknown, item-2)", got.ItemType, got.Title)
	}
	if got.Timestamp != 1700000000 {
		t.Fatalf("显式时间戳应原样保留，实际 %d", got.Timestamp)
	}
}

// TestSubmitFeedback_UnknownItemKeepsFallback 条目不存在时不能写入空 type/title。
func TestSubmitFeedback_UnknownItemKeepsFallback(t *testing.T) {
	repo := &fakeRecommendRepo{}
	svc := &recommendServiceImpl{recommendRepo: repo}

	if err := svc.SubmitFeedback(context.Background(), "item-3", 42, "like", 0); err != nil {
		t.Fatalf("SubmitFeedback 返回错误: %v", err)
	}
	got := repo.savedHistory[0]
	if got.ItemType != "unknown" || got.Title != "item-3" {
		t.Fatalf("降级元信息 = (%s, %s), want (unknown, item-3)", got.ItemType, got.Title)
	}
}

// TestSubmitFeedback_EmptyTypeKeepsUnknown 数据库里 type 为空时不应覆盖 unknown，
// 否则统计侧会多出一个空字符串分类。
func TestSubmitFeedback_EmptyTypeKeepsUnknown(t *testing.T) {
	repo := &fakeRecommendRepo{
		itemByID: &model.RecommendationItem{ID: "item-4", Type: "", Title: ""},
	}
	svc := &recommendServiceImpl{recommendRepo: repo}

	if err := svc.SubmitFeedback(context.Background(), "item-4", 42, "like", 0); err != nil {
		t.Fatalf("SubmitFeedback 返回错误: %v", err)
	}
	got := repo.savedHistory[0]
	if got.ItemType != "unknown" || got.Title != "item-4" {
		t.Fatalf("空字段应保留降级值，实际 (%s, %s)", got.ItemType, got.Title)
	}
}

// TestSubmitFeedback_SaveHistoryErrorPropagates 落库失败必须上抛，
// 不能给调用方返回「反馈已记录」。
func TestSubmitFeedback_SaveHistoryErrorPropagates(t *testing.T) {
	repo := &fakeRecommendRepo{
		itemByID:       &model.RecommendationItem{ID: "item-5", Type: "video"},
		saveHistoryErr: errors.New("insert failed"),
	}
	svc := &recommendServiceImpl{recommendRepo: repo}

	if err := svc.SubmitFeedback(context.Background(), "item-5", 42, "like", 0); err == nil {
		t.Fatal("落库失败应返回错误")
	}
}
