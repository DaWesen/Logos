package handler

import (
	"context"
	"errors"
	"testing"

	"Logos/internal/service/messaging/message/dao"
	"Logos/internal/service/messaging/message/model"
	"Logos/internal/service/messaging/message/service"
	pb "Logos/proto_gen/message"
)

type fakeMessageService struct {
	service.MessageService
	batchResult []*model.Message
	batchErr    error
	stats       []dao.TopicStats
	statsErr    error
}

func (f *fakeMessageService) GetMessageStats(context.Context) ([]dao.TopicStats, error) {
	return f.stats, f.statsErr
}

func (f *fakeMessageService) BatchSendMessage(context.Context, []struct {
	Topic         int32
	Content       string
	Priority      *int32
	Headers       map[string]string
	CorrelationID *string
}) ([]*model.Message, error) {
	return f.batchResult, f.batchErr
}

// proto Priority 从 0 开始，内部模型是 1..4。
// 直接透传会让 LOW 落库为 0、URGENT 落库为 3（HIGH），消费端读回来整体偏移。
func TestPriorityToInt32_UsesInternalOneBasedScale(t *testing.T) {
	cases := []struct {
		in   pb.Priority
		want int32
	}{
		{pb.Priority_LOW, 1},
		{pb.Priority_NORMAL, 2},
		{pb.Priority_HIGH, 3},
		{pb.Priority_URGENT, 4},
	}
	for _, c := range cases {
		got := priorityToInt32(&c.in)
		if got == nil || *got != c.want {
			t.Fatalf("priorityToInt32(%v) = %v, want %d", c.in, got, c.want)
		}
	}
	if priorityToInt32(nil) != nil {
		t.Fatal("未设置优先级时应返回 nil，由服务层填默认值")
	}
}

func TestTopicFromInternal_RoundTripsAllTopics(t *testing.T) {
	cases := map[string]pb.Topic{
		"DATA_COLLECTION":      pb.Topic_DATA_COLLECTION,
		"KNOWLEDGE_EXTRACTION": pb.Topic_KNOWLEDGE_EXTRACTION,
		"VECTOR_PROCESSING":    pb.Topic_VECTOR_PROCESSING,
		"QA_REQUEST":           pb.Topic_QA_REQUEST,
		"RECOMMENDATION":       pb.Topic_RECOMMENDATION,
		"USER_ACTIVITY":        pb.Topic_USER_ACTIVITY,
		"SYSTEM_EVENT":         pb.Topic_SYSTEM_EVENT,
	}
	for name, want := range cases {
		got, ok := topicFromInternal(name)
		if !ok || got != want {
			t.Fatalf("topicFromInternal(%q) = %v/%v, want %v", name, got, ok, want)
		}
	}
	if _, ok := topicFromInternal("UNKNOWN(9)"); ok {
		t.Fatal("未知 topic 不应映射成 DATA_COLLECTION")
	}
}

// 批量响应曾把所有条目的 topic 硬编码成 pb.Topic(1)：
// 客户端发的 QA_REQUEST 会在响应里变成 KNOWLEDGE_EXTRACTION。
func TestBatchSendMessage_ResponseEchoesActualTopic(t *testing.T) {
	fake := &fakeMessageService{batchResult: []*model.Message{
		{ID: "m1", Topic: "QA_REQUEST", Timestamp: 1},
		{ID: "m2", Topic: "SYSTEM_EVENT", Timestamp: 2},
	}}
	svc := &MessageServiceImpl{MessageService: fake}

	resp, err := svc.BatchSendMessage(context.Background(), &pb.BatchSendMessageReq{
		Messages: []*pb.SendMessageReq{
			{Topic: pb.Topic_QA_REQUEST, Content: "q"},
			{Topic: pb.Topic_SYSTEM_EVENT, Content: "e"},
		},
	})
	if err != nil {
		t.Fatalf("批量发送不应报错: %v", err)
	}
	if len(resp.Responses) != 2 {
		t.Fatalf("响应条数 = %d, want 2", len(resp.Responses))
	}
	if resp.Responses[0].Topic != pb.Topic_QA_REQUEST {
		t.Fatalf("第一条 topic = %v, want QA_REQUEST", resp.Responses[0].Topic)
	}
	if resp.Responses[1].Topic != pb.Topic_SYSTEM_EVENT {
		t.Fatalf("第二条 topic = %v, want SYSTEM_EVENT", resp.Responses[1].Topic)
	}
}

// 统计值曾用 []map[string]interface{} 跨层传递，handler 侧靠 5 次
// `v, _ := m[key].(int64)` 取值。断言失败时全部静默变 0，
// 监控面板会把「有积压」显示成「一切正常」。改成结构体后类型由编译器保证。
func TestGetMessageStats_MapsTypedCounters(t *testing.T) {
	fake := &fakeMessageService{stats: []dao.TopicStats{
		{Topic: "QA_REQUEST", Total: 42, Pending: 7, Processed: 30, Failed: 5},
		{Topic: "UNKNOWN_TOPIC", Total: 9},
	}}
	svc := &MessageServiceImpl{MessageService: fake}

	resp, err := svc.GetMessageStats(context.Background(), &pb.EmptyReq{})
	if err != nil {
		t.Fatalf("统计不应报错: %v", err)
	}
	if len(resp.Stats) != 1 {
		t.Fatalf("未知 topic 应被跳过, got %d 条", len(resp.Stats))
	}
	got := resp.Stats[0]
	if got.Topic != pb.Topic_QA_REQUEST || got.TotalMessages != 42 ||
		got.PendingMessages != 7 || got.ProcessedMessages != 30 || got.ErrorMessages != 5 {
		t.Fatalf("统计字段映射错误: %+v", got)
	}

	fake.statsErr = errors.New("db down")
	resp, err = svc.GetMessageStats(context.Background(), &pb.EmptyReq{})
	if err != nil {
		t.Fatalf("gRPC 层不应把业务错误变成传输错误: %v", err)
	}
	if resp.BaseResp.StatusCode != 500 || len(resp.Stats) != 0 {
		t.Fatalf("查询失败应返回 500 且不带统计, got %+v", resp.BaseResp)
	}
}
