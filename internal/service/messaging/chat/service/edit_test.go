package service

import (
	"testing"
	"time"

	"Logos/internal/service/messaging/chat/dao"
	"Logos/internal/service/messaging/chat/model"
	"Logos/internal/service/messaging/types"
)

func editRepo() *stubChatRepo {
	msg := &model.Message{
		ID:       "m1",
		ChatID:   "private_1001_1002",
		ChatType: int(model.ChatTypePrivate),
		SenderID: "1001",
		Content:  "原始内容",
		Status:   model.MessageStatusSent.String(),
		Version:  0,
	}
	return &stubChatRepo{messages: map[string]*model.Message{"m1": msg}}
}

// TestEditMessageVersionMatched 版本匹配时写入成功，内容更新且版本自增、状态变为 edited。
func TestEditMessageVersionMatched(t *testing.T) {
	repo := editRepo()
	svc := newAccessService(repo)

	updated, err := svc.EditMessage("m1", "1001", "第一次编辑", 0)
	if err != nil {
		t.Fatalf("版本匹配应编辑成功，实际 %v", err)
	}
	if updated.Content != "第一次编辑" {
		t.Fatalf("内容未更新，实际 %q", updated.Content)
	}
	if updated.Version != 1 {
		t.Fatalf("版本应自增到 1，实际 %d", updated.Version)
	}
	if updated.Status != model.MessageStatusEdited.String() {
		t.Fatalf("状态应为 edited，实际 %q", updated.Status)
	}
}

// TestEditMessageStaleVersionConflict 旧版本再次提交必须失败且不覆盖已有内容（乐观锁核心断言）。
func TestEditMessageStaleVersionConflict(t *testing.T) {
	repo := editRepo()
	svc := newAccessService(repo)

	if _, err := svc.EditMessage("m1", "1001", "先写入的内容", 0); err != nil {
		t.Fatalf("首次编辑失败: %v", err)
	}

	// 模拟另一台设备仍持有 version=0 的旧副本，后提交。
	if _, err := svc.EditMessage("m1", "1001", "后写入的内容", 0); err != types.ErrChatMessageConflict {
		t.Fatalf("旧版本应返回 ErrChatMessageConflict，实际 %v", err)
	}
	if got := repo.messages["m1"].Content; got != "先写入的内容" {
		t.Fatalf("冲突时不得覆盖已有内容，实际 %q", got)
	}
	if got := repo.messages["m1"].Version; got != 1 {
		t.Fatalf("冲突时版本不应变化，实际 %d", got)
	}

	// 客户端刷新拿到 version=1 后重新提交即可成功。
	if _, err := svc.EditMessage("m1", "1001", "刷新后的内容", 1); err != nil {
		t.Fatalf("版本刷新后应编辑成功，实际 %v", err)
	}
	if got := repo.messages["m1"].Version; got != 2 {
		t.Fatalf("再次编辑后版本应为 2，实际 %d", got)
	}
}

// TestEditMessageNotFoundOrNotOwner 消息不存在与非发送者返回同一错误，避免探测他人 message_id。
func TestEditMessageNotFoundOrNotOwner(t *testing.T) {
	repo := editRepo()
	svc := newAccessService(repo)

	if _, err := svc.EditMessage("not-exist", "1001", "任意内容", 0); err != types.ErrChatMessageNotFound {
		t.Fatalf("消息不存在应返回 ErrChatMessageNotFound，实际 %v", err)
	}
	if _, err := svc.EditMessage("m1", "1002", "越权内容", 0); err != types.ErrChatMessageNotFound {
		t.Fatalf("非发送者应返回 ErrChatMessageNotFound，实际 %v", err)
	}
	if got := repo.messages["m1"].Content; got != "原始内容" {
		t.Fatalf("拒绝的请求不得修改内容，实际 %q", got)
	}

	if _, err := svc.EditMessage("m1", "1001", "任意内容", -1); err != types.ErrChatBadRequest {
		t.Fatalf("负数版本应返回 ErrChatBadRequest，实际 %v", err)
	}
	if got := repo.messages["m1"].Version; got != 0 {
		t.Fatalf("非法请求不应推进版本，实际 %d", got)
	}
}

// TestEditMessageRejectsWithdrawn 已撤回的消息不可再编辑，否则会把「已撤回」覆盖回正文。
func TestEditMessageRejectsWithdrawn(t *testing.T) {
	repo := editRepo()
	repo.messages["m1"].Status = model.MessageStatusWithdrawn.String()
	repo.messages["m1"].Content = "[消息已撤回]"
	svc := newAccessService(repo)

	if _, err := svc.EditMessage("m1", "1001", "试图覆盖撤回", 0); err != types.ErrChatMessageNotFound {
		t.Fatalf("撤回后编辑应被拒绝，实际 %v", err)
	}
	if got := repo.messages["m1"].Content; got != "[消息已撤回]" {
		t.Fatalf("撤回内容不应被覆盖，实际 %q", got)
	}
}

// TestEditEventRecipients 编辑事件必须覆盖会话内全部成员，且私聊一定包含发送者本人，
// 否则同账号的其他在线设备收不到自己发起的编辑。
func TestEditEventRecipients(t *testing.T) {
	repo := editRepo()
	svc := newAccessService(repo)

	privateIDs := svc.editRecipients(repo.messages["m1"])
	if len(privateIDs) != 2 || !hasID(privateIDs, "1001") || !hasID(privateIDs, "1002") {
		t.Fatalf("私聊编辑接收者应包含双方，实际 %v", privateIDs)
	}

	repo.members = map[string]*model.GroupMember{
		"group-1|1001": {GroupID: "group-1", UserID: "1001"},
		"group-1|1002": {GroupID: "group-1", UserID: "1002"},
		"group-1|1003": {GroupID: "group-1", UserID: "1003"},
	}
	groupMsg := &model.Message{
		ID:       "g1",
		ChatID:   "group-1",
		ChatType: int(model.ChatTypeGroup),
		SenderID: "1001",
	}
	groupIDs := svc.editRecipients(groupMsg)
	if len(groupIDs) != 3 || !hasID(groupIDs, "1003") {
		t.Fatalf("群聊编辑应投递给全部成员，实际 %v", groupIDs)
	}
}

func hasID(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

// TestEditMessageRecordsChangeLog 编辑必须在同一事务内追加原位变更日志，
// 否则离线设备无论怎么同步都拿不到这次编辑。
func TestEditMessageRecordsChangeLog(t *testing.T) {
	repo := editRepo()
	repo.messages["m1"].Seq = 7
	svc := newAccessService(repo)

	if _, err := svc.EditMessage("m1", "1001", "改过的内容", 0); err != nil {
		t.Fatalf("编辑失败: %v", err)
	}
	if len(repo.changes) != 1 {
		t.Fatalf("应记录 1 条变更日志，实际 %d", len(repo.changes))
	}
	ch := repo.changes[0]
	if ch.ChangeType != model.MessageChangeEdit || ch.MessageID != "m1" || ch.Version != 1 || ch.MessageSeq != 7 {
		t.Fatalf("变更日志字段不符: %+v", ch)
	}

	// 版本冲突不得产生变更日志（否则离线端会收到一条并不存在的编辑）
	if _, err := svc.EditMessage("m1", "1001", "覆盖旧版本", 0); err != types.ErrChatMessageConflict {
		t.Fatalf("旧版本应冲突，实际 %v", err)
	}
	if len(repo.changes) != 1 {
		t.Fatalf("冲突不应新增变更日志，实际 %d", len(repo.changes))
	}
}

// TestWithdrawMessageRecordsChangeLog 撤回同样写入变更日志，并推进 version，
// 让客户端按版本比较时不会被更早的编辑事件覆盖。
func TestWithdrawMessageRecordsChangeLog(t *testing.T) {
	repo := deletionRepo()
	repo.messages["m1"].CreatedAt = time.Now()
	svc := newAccessService(repo)

	if err := svc.WithdrawMessage("m1", "1001"); err != nil {
		t.Fatalf("撤回失败: %v", err)
	}
	if len(repo.changes) != 1 {
		t.Fatalf("应记录 1 条撤回日志，实际 %d", len(repo.changes))
	}
	ch := repo.changes[0]
	if ch.ChangeType != model.MessageChangeWithdraw || ch.Version != 1 || ch.Content != "[消息已撤回]" {
		t.Fatalf("撤回日志字段不符: %+v", ch)
	}
	if repo.messages["m1"].Version != 1 {
		t.Fatalf("撤回后版本应为 1，实际 %d", repo.messages["m1"].Version)
	}
}

// TestSyncDeliversMessageChanges 离线期间发生的编辑必须通过 updated_seq 游标补齐。
func TestSyncDeliversMessageChanges(t *testing.T) {
	repo := deletionRepo()
	svc := newAccessService(repo)

	if _, err := svc.EditMessage("m1", "1001", "离线期间改的内容", 0); err != nil {
		t.Fatalf("编辑失败: %v", err)
	}

	result := syncOnce(t, svc, "1001", model.SyncCursor{ChatID: deletionChatID, LastSeq: 3}, 50)
	if len(result.Changes) != 1 {
		t.Fatalf("应下发 1 条原位变更，实际 %d", len(result.Changes))
	}
	if ch := result.Changes[0]; ch.MessageID != "m1" || ch.Content != "离线期间改的内容" || ch.Version != 1 {
		t.Fatalf("变更内容不符: %+v", ch)
	}
	if result.UpdatedSeq != 1 || result.UpdatedHasMore {
		t.Fatalf("变更游标不符: seq=%d hasMore=%v", result.UpdatedSeq, result.UpdatedHasMore)
	}

	// 客户端已推进到 updated_seq=1 后，不应重复下发
	next := syncOnce(t, svc, "1001", model.SyncCursor{ChatID: deletionChatID, LastSeq: 3, UpdatedSeq: 1}, 50)
	if len(next.Changes) != 0 || next.UpdatedSeq != 1 {
		t.Fatalf("游标推进后应无变更，实际 %+v", next)
	}
}

// TestPurgeExpiredMessageChanges 变更日志按保留期回收：过期日志删除，未过期保留。
// 日志是会话级共享的，不能按单个用户的 cleared_seq 删除，否则其他成员的离线补拉会断。
func TestPurgeExpiredMessageChanges(t *testing.T) {
	now := time.Now()
	repo := &stubChatRepo{changes: []*model.MessageChange{
		{ID: 1, ChatID: deletionChatID, MessageID: "m1", CreatedAt: now.Add(-8 * 24 * time.Hour)},
		{ID: 2, ChatID: deletionChatID, MessageID: "m2", CreatedAt: now},
	}}
	svc := newAccessService(repo)

	if n := svc.purgeExpiredChanges(now); n != 1 {
		t.Fatalf("应只清理 1 条过期日志，实际 %d", n)
	}
	if len(repo.changes) != 1 || repo.changes[0].ID != 2 {
		t.Fatalf("未过期日志应保留，实际 %+v", repo.changes)
	}
}

// TestSyncSkipsChangesBelowClearedSeq 已被本人清空的历史（seq <= cleared_seq）不再下发变更，
// 但游标仍要推进，避免每次同步重复拉取无效日志。
func TestSyncSkipsChangesBelowClearedSeq(t *testing.T) {
	repo := deletionRepo()
	repo.watermarks[deletionChatID] = dao.ParticipantWatermark{ConversationID: deletionChatID, ClearedSeq: 2}
	svc := newAccessService(repo)

	// m1 的 seq=1 已被清空，它的编辑不应再下发
	if _, err := svc.EditMessage("m1", "1001", "被清空历史的编辑", 0); err != nil {
		t.Fatalf("编辑失败: %v", err)
	}

	result := syncOnce(t, svc, "1001", model.SyncCursor{ChatID: deletionChatID, LastSeq: 2}, 50)
	if len(result.Changes) != 0 {
		t.Fatalf("水位线以下的变更不应下发，实际 %+v", result.Changes)
	}
	if result.UpdatedSeq != 1 {
		t.Fatalf("即使全被过滤，游标也要推进到 1，实际 %d", result.UpdatedSeq)
	}
}
