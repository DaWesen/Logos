package service

import (
	"testing"

	"Logos/internal/service/messaging/chat/dao"
	"Logos/internal/service/messaging/chat/model"
	"Logos/internal/service/messaging/types"
)

func seqMessages(chatID string, seqs ...int64) []*model.Message {
	out := make([]*model.Message, 0, len(seqs))
	for _, seq := range seqs {
		out = append(out, &model.Message{ID: chatID + "-" + string(rune('a'+seq)), ChatID: chatID, Seq: seq})
	}
	return out
}

func TestSyncOfflineMessagesBatch(t *testing.T) {
	repo := &stubChatRepo{
		watermarks: map[string]dao.ParticipantWatermark{
			"private_1_2": {ConversationID: "private_1_2"},
			"group-1":     {ConversationID: "group-1", ClearedSeq: 100},
		},
		chatSeqs: map[string]int64{"private_1_2": 7, "group-1": 105},
		chatMsgs: map[string][]*model.Message{
			"private_1_2": seqMessages("private_1_2", 4, 5, 6, 7),
			// 已清空的部分（seq <= 100）不应再被拉回
			"group-1": seqMessages("group-1", 99, 100, 101, 105),
		},
	}
	svc := newAccessService(repo)

	results, err := svc.SyncOfflineMessages("1", []model.SyncCursor{
		{ChatID: "private_1_2", LastSeq: 5},
		{ChatID: "group-1", LastSeq: 0},
		{ChatID: "group-1", LastSeq: 90}, // 重复游标取首个
		{ChatID: "group-not-member", LastSeq: 0},
	}, 50)
	if err != nil {
		t.Fatalf("同步失败: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("非成员会话应被跳过，实际返回 %d 个会话", len(results))
	}

	private := results[0]
	if private.ChatID != "private_1_2" || len(private.Messages) != 2 {
		t.Fatalf("私聊增量不符: %+v", private)
	}
	if private.Messages[0].Seq != 6 || private.Messages[1].Seq != 7 {
		t.Fatalf("私聊增量应按 seq 升序且只含 > 5: %+v", private.Messages)
	}
	if private.LatestSeq != 7 || private.ClearedSeq != 0 || private.HasMore {
		t.Fatalf("私聊元信息不符: %+v", private)
	}

	group := results[1]
	if len(group.Messages) != 2 || group.Messages[0].Seq != 101 {
		t.Fatalf("群聊应只返回清空水位线之后的增量: %+v", group.Messages)
	}
	if group.ClearedSeq != 100 || group.LatestSeq != 105 || group.HasMore {
		t.Fatalf("群聊元信息不符: %+v", group)
	}
}

func TestSyncOfflineMessagesPaging(t *testing.T) {
	many := make([]*model.Message, 0, 260)
	for i := int64(1); i <= 260; i++ {
		many = append(many, &model.Message{ID: "m", ChatID: "group-1", Seq: i})
	}
	repo := &stubChatRepo{
		watermarks: map[string]dao.ParticipantWatermark{"group-1": {ConversationID: "group-1"}},
		chatSeqs:   map[string]int64{"group-1": 260},
		chatMsgs:   map[string][]*model.Message{"group-1": many},
	}
	svc := newAccessService(repo)

	// limit 缺省为 50
	results, err := svc.SyncOfflineMessages("1", []model.SyncCursor{{ChatID: "group-1"}}, 0)
	if err != nil {
		t.Fatalf("同步失败: %v", err)
	}
	if len(results[0].Messages) != defaultSyncLimit || !results[0].HasMore {
		t.Fatalf("缺省 limit 应为 %d 且 HasMore=true，实际 %d/%v", defaultSyncLimit, len(results[0].Messages), results[0].HasMore)
	}

	// 超大 limit 被截断到上限
	results, err = svc.SyncOfflineMessages("1", []model.SyncCursor{{ChatID: "group-1"}}, 10000)
	if err != nil {
		t.Fatalf("同步失败: %v", err)
	}
	if len(results[0].Messages) != maxSyncLimit || !results[0].HasMore {
		t.Fatalf("limit 应被截断到 %d，实际 %d", maxSyncLimit, len(results[0].Messages))
	}
}

func TestSyncOfflineMessagesValidation(t *testing.T) {
	svc := newAccessService(&stubChatRepo{})

	if _, err := svc.SyncOfflineMessages("1", nil, 10); err != types.ErrChatBadRequest {
		t.Fatalf("空游标应拒绝，实际: %v", err)
	}
	if _, err := svc.SyncOfflineMessages("", []model.SyncCursor{{ChatID: "group-1"}}, 10); err != types.ErrChatAccessDenied {
		t.Fatalf("缺用户应拒绝，实际: %v", err)
	}

	tooMany := make([]model.SyncCursor, maxSyncChats+1)
	for i := range tooMany {
		tooMany[i] = model.SyncCursor{ChatID: "chat"}
	}
	if _, err := svc.SyncOfflineMessages("1", tooMany, 10); err != types.ErrChatBadRequest {
		t.Fatalf("超会话上限应拒绝，实际: %v", err)
	}
}
