package service

import (
	"errors"
	"strings"
	"testing"

	"Logos/internal/service/messaging/chat/model"
	"Logos/internal/service/messaging/types"
)

// 已读位点只用「本次回执消息的最大 seq」，不能拿客户端传入的顺序或条数估算。
func TestMarkMessagesRead_AdvancesToMaxSeqOfExplicitIDs(t *testing.T) {
	const chatID = "private_1001_1002"
	repo := &stubChatRepo{
		messages: map[string]*model.Message{
			"m1": {ID: "m1", ChatID: chatID, Seq: 10},
			"m2": {ID: "m2", ChatID: chatID, Seq: 12},
			"m3": {ID: "m3", ChatID: chatID, Seq: 11},
		},
	}
	svc := newAccessService(repo)

	if err := svc.MarkMessagesRead([]string{"m1", "m2", "m3"}, "1001", chatID); err != nil {
		t.Fatalf("标记已读应成功: %v", err)
	}
	if len(repo.advanceCalls) != 1 {
		t.Fatalf("应只推进一次位点，实际 %d 次: %+v", len(repo.advanceCalls), repo.advanceCalls)
	}
	got := repo.advanceCalls[0]
	if got.chatID != chatID || got.userID != "1001" || got.seq != 12 {
		t.Fatalf("位点推进参数不符: %+v", got)
	}
}

// 不带 msgIDs 表示「进入会话全部已读」，目标位点取会话当前最大 seq。
func TestMarkMessagesRead_WithoutIDsUsesChatMaxSeq(t *testing.T) {
	const chatID = "private_1001_1002"
	repo := &stubChatRepo{maxSeqs: map[string]int64{chatID: 42}}
	svc := newAccessService(repo)

	if err := svc.MarkMessagesRead(nil, "1002", chatID); err != nil {
		t.Fatalf("全部已读应成功: %v", err)
	}
	if len(repo.advanceCalls) != 1 || repo.advanceCalls[0].seq != 42 {
		t.Fatalf("全部已读应推进到会话最大 seq，实际 %+v", repo.advanceCalls)
	}
}

// 缺身份或越权时不得触达位点表（回归「空 chat_id 退化成全局标记」）。
func TestMarkMessagesRead_RejectsMissingIdentityOrOutsider(t *testing.T) {
	const chatID = "private_1001_1002"
	repo := &stubChatRepo{}
	svc := newAccessService(repo)

	if err := svc.MarkMessagesRead([]string{"m1"}, "", chatID); !errors.Is(err, types.ErrChatAccessDenied) {
		t.Fatalf("缺用户应拒绝，实际 %v", err)
	}
	if err := svc.MarkMessagesRead([]string{"m1"}, "1003", chatID); !errors.Is(err, types.ErrChatAccessDenied) {
		t.Fatalf("非参与者应拒绝，实际 %v", err)
	}
	if len(repo.advanceCalls) != 0 {
		t.Fatalf("被拒请求不应推进位点: %+v", repo.advanceCalls)
	}
}

// 查询 seq 失败时若继续发布回执，发送方会看到「已读」而阅读方未读数不变。
// 现在直接上抛，让事件消费重试。
func TestHandleMessageReadEvent_SeqLookupFailureSurfacesError(t *testing.T) {
	const chatID = "private_1001_1002"
	repo := &stubChatRepo{maxSeqByIDsErr: errors.New("db down")}
	svc := newAccessService(repo)

	err := svc.handleMessageReadEvent(&types.MessageReadEvent{
		ChatID:     chatID,
		ReaderID:   "1001",
		MessageIDs: []string{"m1"},
	})
	if err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("seq 查询失败应原样上抛，实际 %v", err)
	}
	if len(repo.advanceCalls) != 0 {
		t.Fatalf("失败时不应推进位点: %+v", repo.advanceCalls)
	}
}

// 消息已被清理或 ID 全部无效时没有可推进的位点，也不应为空回执通知发送方。
func TestHandleMessageReadEvent_NoMatchingMessagesSkipsReceipt(t *testing.T) {
	const chatID = "private_1001_1002"
	repo := &stubChatRepo{messages: map[string]*model.Message{}}
	svc := newAccessService(repo)

	err := svc.handleMessageReadEvent(&types.MessageReadEvent{
		ChatID:     chatID,
		ReaderID:   "1001",
		MessageIDs: []string{"gone"},
	})
	if err != nil {
		t.Fatalf("无匹配消息应安静跳过，实际 %v", err)
	}
	if len(repo.advanceCalls) != 0 {
		t.Fatalf("无匹配消息不应推进位点: %+v", repo.advanceCalls)
	}
}
