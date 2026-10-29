package service

import (
	"testing"

	"Logos/internal/service/messaging/chat/dao"
	"Logos/internal/service/messaging/chat/model"
	"Logos/internal/service/messaging/types"
)

// TestPrivateChatReadReceipt 单聊：对方已读位点 >= 消息 seq 才算已读。
func TestPrivateChatReadReceipt(t *testing.T) {
	const chatID = "private_1001_1002"

	tests := []struct {
		name         string
		peerReadSeq  int64
		wantRead     int
		wantAllRead  bool
		wantMemberCt int
	}{
		{name: "对方未读", peerReadSeq: 4, wantRead: 0, wantAllRead: false, wantMemberCt: 1},
		{name: "对方已读", peerReadSeq: 5, wantRead: 1, wantAllRead: true, wantMemberCt: 1},
		{name: "对方读得更靠后", peerReadSeq: 9, wantRead: 1, wantAllRead: true, wantMemberCt: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &stubChatRepo{
				messages: map[string]*model.Message{
					"m1": {ID: "m1", ChatID: chatID, ChatType: int(model.ChatTypePrivate), SenderID: "1001", Seq: 5},
				},
				positions: map[string][]dao.ReadPosition{
					chatID: {
						{UserID: "1001", LastReadSeq: 5},
						{UserID: "1002", LastReadSeq: tt.peerReadSeq},
					},
				},
			}
			svc := newAccessService(repo)

			receipts, err := svc.GetMessageReadReceipts("1001", chatID, []string{"m1"}, false)
			if err != nil {
				t.Fatalf("查询已读回执失败: %v", err)
			}
			if len(receipts) != 1 {
				t.Fatalf("期望 1 条回执，实际 %d", len(receipts))
			}
			got := receipts[0]
			if got.ReadCount != tt.wantRead || got.MemberCount != tt.wantMemberCt || got.AllRead != tt.wantAllRead {
				t.Fatalf("回执不符: %+v", got)
			}
			if len(got.ReaderIDs) != 0 {
				t.Fatalf("未请求明细时不应返回 reader_ids: %v", got.ReaderIDs)
			}
		})
	}
}

// TestGroupReadReceipt 群聊：按成员位点聚合，发送者本身不计入已读/成员数。
func TestGroupReadReceipt(t *testing.T) {
	const chatID = "group-1"
	repo := &stubChatRepo{
		groups: map[string]*model.Group{chatID: {ID: chatID}},
		members: map[string]*model.GroupMember{
			chatID + "|1001": {GroupID: chatID, UserID: "1001"},
		},
		messages: map[string]*model.Message{
			"m1": {ID: "m1", ChatID: chatID, ChatType: int(model.ChatTypeGroup), SenderID: "1001", Seq: 10},
			"m2": {ID: "m2", ChatID: chatID, ChatType: int(model.ChatTypeGroup), SenderID: "1001", Seq: 11},
			// 他人发的消息：不返回回执
			"m3": {ID: "m3", ChatID: chatID, ChatType: int(model.ChatTypeGroup), SenderID: "2001", Seq: 12},
			// 其他会话的消息：不返回回执
			"m4": {ID: "m4", ChatID: "group-2", ChatType: int(model.ChatTypeGroup), SenderID: "1001", Seq: 12},
		},
		positions: map[string][]dao.ReadPosition{
			chatID: {
				{UserID: "1001", LastReadSeq: 12},
				{UserID: "2001", LastReadSeq: 10},
				{UserID: "2002", LastReadSeq: 9},
			},
		},
	}
	svc := newAccessService(repo)

	receipts, err := svc.GetMessageReadReceipts("1001", chatID, []string{"m1", "m2", "m3", "m4", "m1"}, true)
	if err != nil {
		t.Fatalf("查询已读回执失败: %v", err)
	}
	if len(receipts) != 2 {
		t.Fatalf("只应返回本人发送的 2 条回执，实际 %d: %+v", len(receipts), receipts)
	}

	first := receipts[0]
	if first.MessageID != "m1" || first.ReadCount != 1 || first.MemberCount != 2 || first.AllRead {
		t.Fatalf("m1 回执不符: %+v", first)
	}
	if len(first.ReaderIDs) != 1 || first.ReaderIDs[0] != "2001" {
		t.Fatalf("m1 已读明细不符: %v", first.ReaderIDs)
	}

	second := receipts[1]
	if second.MessageID != "m2" || second.ReadCount != 0 || second.AllRead {
		t.Fatalf("m2 回执不符: %+v", second)
	}
}

// TestReadReceiptsRejectsUnauthorized 非成员、空参数、超批量上限均应失败关闭。
func TestReadReceiptsRejectsUnauthorized(t *testing.T) {
	const chatID = "group-1"
	repo := &stubChatRepo{
		groups: map[string]*model.Group{chatID: {ID: chatID}},
		members: map[string]*model.GroupMember{
			chatID + "|1001": {GroupID: chatID, UserID: "1001"},
		},
	}
	svc := newAccessService(repo)

	if _, err := svc.GetMessageReadReceipts("2001", chatID, []string{"m1"}, false); err != types.ErrChatAccessDenied {
		t.Fatalf("非成员应拒绝，实际: %v", err)
	}

	tooMany := make([]string, maxReadReceiptBatch+1)
	for i := range tooMany {
		tooMany[i] = "m"
	}
	if _, err := svc.GetMessageReadReceipts("1001", chatID, tooMany, false); err != types.ErrChatBadRequest {
		t.Fatalf("超批量上限应拒绝，实际: %v", err)
	}

	if _, err := svc.GetMessageReadReceipts("1001", chatID, nil, false); err != types.ErrChatBadRequest {
		t.Fatalf("空消息列表应拒绝，实际: %v", err)
	}

	if _, err := svc.GetMessageReadReceipts("", chatID, []string{"m1"}, false); err != types.ErrChatAccessDenied {
		t.Fatalf("缺用户应拒绝，实际: %v", err)
	}
}
