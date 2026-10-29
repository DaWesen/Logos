package service

import (
	"testing"

	"Logos/internal/service/messaging/chat/dao"
	"Logos/internal/service/messaging/chat/model"
	"Logos/internal/service/messaging/types"
)

const deletionChatID = "private_1001_1002"

// deletionRepo 构造一个双方都能访问的私聊会话，消息 seq 为 1..3。
func deletionRepo() *stubChatRepo {
	msgs := []*model.Message{
		{ID: "m1", ChatID: deletionChatID, ChatType: int(model.ChatTypePrivate), SenderID: "1001", Seq: 1},
		{ID: "m2", ChatID: deletionChatID, ChatType: int(model.ChatTypePrivate), SenderID: "1002", Seq: 2},
		{ID: "m3", ChatID: deletionChatID, ChatType: int(model.ChatTypePrivate), SenderID: "1001", Seq: 3},
	}
	return &stubChatRepo{
		watermarks: map[string]dao.ParticipantWatermark{
			deletionChatID: {ConversationID: deletionChatID},
		},
		chatSeqs: map[string]int64{deletionChatID: 3},
		maxSeqs:  map[string]int64{deletionChatID: 3},
		chatMsgs: map[string][]*model.Message{deletionChatID: msgs},
		messages: map[string]*model.Message{"m1": msgs[0], "m2": msgs[1], "m3": msgs[2]},
	}
}

func syncOnce(t *testing.T, svc *ChatServiceImpl, userID string, cursor model.SyncCursor, limit int) *model.ChatSyncResult {
	t.Helper()
	results, err := svc.SyncOfflineMessages(userID, []model.SyncCursor{cursor}, limit)
	if err != nil {
		t.Fatalf("同步失败: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("应返回 1 个会话，实际 %d", len(results))
	}
	return results[0]
}

// TestDeleteMessageOnlyHidesForOwner 删除是私有状态：删除者拉不到该消息，对方仍然完整可见。
func TestDeleteMessageOnlyHidesForOwner(t *testing.T) {
	repo := deletionRepo()
	svc := newAccessService(repo)

	if err := svc.DeleteMessage("1001", "m2"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}

	ownerResult := syncOnce(t, svc, "1001", model.SyncCursor{ChatID: deletionChatID}, 50)
	if len(ownerResult.Messages) != 2 || ownerResult.Messages[0].ID != "m1" || ownerResult.Messages[1].ID != "m3" {
		t.Fatalf("删除者应看不到 m2，实际 %+v", ownerResult.Messages)
	}
	if len(ownerResult.DeletedMessageIDs) != 1 || ownerResult.DeletedMessageIDs[0] != "m2" {
		t.Fatalf("应下发被删消息 ID，实际 %v", ownerResult.DeletedMessageIDs)
	}
	if ownerResult.DeletedSeq == 0 || ownerResult.DeletedHasMore {
		t.Fatalf("删除游标应推进且无更多，实际 seq=%d hasMore=%v", ownerResult.DeletedSeq, ownerResult.DeletedHasMore)
	}

	otherResult := syncOnce(t, svc, "1002", model.SyncCursor{ChatID: deletionChatID}, 50)
	if len(otherResult.Messages) != 3 {
		t.Fatalf("对方应仍能拉到 3 条消息，实际 %+v", otherResult.Messages)
	}
	if len(otherResult.DeletedMessageIDs) != 0 || otherResult.DeletedSeq != 0 {
		t.Fatalf("对方不应收到任何墓碑，实际 ids=%v seq=%d", otherResult.DeletedMessageIDs, otherResult.DeletedSeq)
	}
}

// TestDeleteMessageCursorAdvances 带删除游标再次同步时不应重复下发已有墓碑（游标幂等）。
func TestDeleteMessageCursorAdvances(t *testing.T) {
	repo := deletionRepo()
	svc := newAccessService(repo)
	if err := svc.DeleteMessage("1001", "m1"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}

	first := syncOnce(t, svc, "1001", model.SyncCursor{ChatID: deletionChatID}, 50)
	if len(first.DeletedMessageIDs) != 1 {
		t.Fatalf("首次同步应下发 1 条墓碑，实际 %v", first.DeletedMessageIDs)
	}

	second := syncOnce(t, svc, "1001", model.SyncCursor{ChatID: deletionChatID, LastSeq: first.LatestSeq, DeletedSeq: first.DeletedSeq}, 50)
	if len(second.DeletedMessageIDs) != 0 {
		t.Fatalf("游标推进后不应重复下发墓碑，实际 %v", second.DeletedMessageIDs)
	}
	if second.DeletedSeq != first.DeletedSeq {
		t.Fatalf("无新墓碑时游标应回显入参 %d，实际 %d", first.DeletedSeq, second.DeletedSeq)
	}
	if len(second.Messages) != 0 {
		t.Fatalf("没有新消息时应返回空列表，实际 %+v", second.Messages)
	}
}

// TestDeleteMessageIdempotent 弱网重试 / 多端同时删除：重复调用成功且只落一条墓碑。
func TestDeleteMessageIdempotent(t *testing.T) {
	repo := deletionRepo()
	svc := newAccessService(repo)

	for i := 0; i < 2; i++ {
		if err := svc.DeleteMessage("1001", "m2"); err != nil {
			t.Fatalf("第 %d 次删除应幂等成功，实际 %v", i+1, err)
		}
	}
	if len(repo.deletions) != 1 {
		t.Fatalf("重复删除不应产生多条墓碑，实际 %d 条", len(repo.deletions))
	}
	if len(repo.deleted["1001"]) != 1 {
		t.Fatalf("重复删除不应记录多条删除状态，实际 %v", repo.deleted["1001"])
	}
}

// TestDeleteMessageNotFound 消息不存在与无权访问返回同一错误码，避免探测他人 message_id。
func TestDeleteMessageNotFound(t *testing.T) {
	repo := deletionRepo()
	svc := newAccessService(repo)

	if err := svc.DeleteMessage("1001", "not-exist"); err != types.ErrChatMessageNotFound {
		t.Fatalf("消息不存在应返回 ErrChatMessageNotFound，实际 %v", err)
	}
	if err := svc.DeleteMessage("1003", "m1"); err != types.ErrChatMessageNotFound {
		t.Fatalf("非会话成员应返回 ErrChatMessageNotFound（防枚举），实际 %v", err)
	}
	if len(repo.deletions) != 0 {
		t.Fatalf("拒绝的请求不应写墓碑，实际 %+v", repo.deletions)
	}
	if err := svc.DeleteMessage("", "m1"); err != types.ErrChatBadRequest {
		t.Fatalf("缺用户应返回 ErrChatBadRequest，实际 %v", err)
	}
}

// TestSyncDeletedPaging 墓碑与消息共用 limit，被截断时 HasMore 让客户端继续拉。
func TestSyncDeletedPaging(t *testing.T) {
	repo := deletionRepo()
	svc := newAccessService(repo)
	for _, id := range []string{"m1", "m2", "m3"} {
		if err := svc.DeleteMessage("1001", id); err != nil {
			t.Fatalf("删除 %s 失败: %v", id, err)
		}
	}

	first := syncOnce(t, svc, "1001", model.SyncCursor{ChatID: deletionChatID}, 2)
	if len(first.DeletedMessageIDs) != 2 || !first.DeletedHasMore {
		t.Fatalf("limit=2 应截断墓碑并标记还有更多，实际 ids=%v hasMore=%v", first.DeletedMessageIDs, first.DeletedHasMore)
	}

	next := syncOnce(t, svc, "1001", model.SyncCursor{ChatID: deletionChatID, DeletedSeq: first.DeletedSeq}, 2)
	if len(next.DeletedMessageIDs) != 1 || next.DeletedHasMore {
		t.Fatalf("继续拉应返回剩余 1 条且无更多，实际 ids=%v hasMore=%v", next.DeletedMessageIDs, next.DeletedHasMore)
	}
	if next.DeletedMessageIDs[0] != "m3" {
		t.Fatalf("剩余墓碑顺序不符: %v", next.DeletedMessageIDs)
	}
}

// TestClearChatHistoryPurgesDeletions 清空历史时按水位线清理本人墓碑，且只清理本人的。
func TestClearChatHistoryPurgesDeletions(t *testing.T) {
	repo := deletionRepo()
	svc := newAccessService(repo)
	if err := svc.DeleteMessage("1001", "m1"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if err := svc.DeleteMessage("1002", "m2"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}

	if err := svc.DeleteChatHistory("1001", deletionChatID); err != nil {
		t.Fatalf("清空历史失败: %v", err)
	}
	if len(repo.purged) != 1 || repo.purged[0].upToSeq != 3 || repo.purged[0].userID != "1001" {
		t.Fatalf("应以本人视角按最大 seq 清理墓碑，实际 %+v", repo.purged)
	}
	if len(repo.deletions) != 1 || repo.deletions[0].UserID != "1002" {
		t.Fatalf("只应清理本人的墓碑，实际 %+v", repo.deletions)
	}
}
