package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"Logos/internal/service/messaging/message/dao"
	"Logos/internal/service/messaging/message/model"
	"Logos/pkg/mq"
	"Logos/pkg/outbox"

	"gorm.io/gorm"
)

type fakeAnswer struct{}

func (fakeAnswer) GetID() string { return "answer-1" }

type fakeQuestionService struct {
	calls  int
	err    error
	gotCtx context.Context
	ctxErr error
	block  bool
}

func (f *fakeQuestionService) AskQuestion(ctx context.Context, _ string, _ *string) (interface{ GetID() string }, error) {
	f.calls++
	f.gotCtx = ctx
	f.ctxErr = ctx.Err()
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return fakeAnswer{}, f.err
}

type stubMessageRepo struct {
	dao.MessageRepository
	batchCalls int
	lastIDs    []string
	lastStatus string
	batchErr   error
	updateErr  error
	updatedID  string
	updatedTo  string

	claimCalls   int
	claimedTopic string
	claimedGroup string
	leaseUntil   time.Time
	claimLimit   int
	claimResult  []*model.Message
	claimResults [][]*model.Message
	claimErr     error
	claimCtxErr  []error

	statsRows []dao.TopicStats
	statsErr  error
}

func (s *stubMessageRepo) AckMessages(_ context.Context, ids []string, _ string) error {
	s.batchCalls++
	s.lastIDs = append([]string(nil), ids...)
	s.lastStatus = "ACKED"
	return s.batchErr
}

func (s *stubMessageRepo) UpdateMessageStatus(_ context.Context, id string, status string) error {
	s.updatedID = id
	s.updatedTo = status
	return s.updateErr
}

func (s *stubMessageRepo) AckMessage(_ context.Context, _ string, _ string) error {
	return s.updateErr
}

func (s *stubMessageRepo) ClaimPendingMessages(
	ctx context.Context,
	topic string,
	limit int,
	consumerGroup string,
	leaseUntil time.Time,
) ([]*model.Message, error) {
	s.claimCalls++
	s.claimedTopic = topic
	s.claimLimit = limit
	s.claimedGroup = consumerGroup
	s.leaseUntil = leaseUntil
	s.claimCtxErr = append(s.claimCtxErr, ctx.Err())
	if len(s.claimResults) > 0 {
		result := s.claimResults[0]
		s.claimResults = s.claimResults[1:]
		return result, s.claimErr
	}
	return s.claimResult, s.claimErr
}

func (s *stubMessageRepo) GetMessageStatsByTopic(_ context.Context) ([]dao.TopicStats, error) {
	return s.statsRows, s.statsErr
}

// 内部 topic 名与 Kafka topic 必须一一对应。历史上 outbox 直接把
// "QA_REQUEST" 当 Kafka topic 发，而消费者订阅的是 "qa_request"，
// 消息显示投递成功却永远不会被处理。
func TestKafkaTopicFor_MapsInternalNames(t *testing.T) {
	cases := map[string]string{
		"DATA_COLLECTION":      mq.TopicDataCollection,
		"KNOWLEDGE_EXTRACTION": mq.TopicKnowledgeExtraction,
		"VECTOR_PROCESSING":    mq.TopicVectorProcessing,
		"QA_REQUEST":           mq.TopicQARequest,
		"RECOMMENDATION":       mq.TopicRecommendation,
		"USER_ACTIVITY":        mq.TopicUserActivity,
		"SYSTEM_EVENT":         mq.TopicSystemEvent,
		"":                     mq.TopicSystemEvent,
		"unknown":              mq.TopicSystemEvent,
	}
	for internal, want := range cases {
		if got := kafkaTopicFor(internal); got != want {
			t.Fatalf("kafkaTopicFor(%q) = %q, want %q", internal, got, want)
		}
	}
}

// proto Topic 枚举从 0 开始，DAO 若按 1 开始解析会整体错位：
// DATA_COLLECTION 变成 UNKNOWN(0)，SYSTEM_EVENT 永远不可达。
func TestTopicString_MatchesProtoEnum(t *testing.T) {
	cases := map[int]string{
		0: "DATA_COLLECTION",
		1: "KNOWLEDGE_EXTRACTION",
		2: "VECTOR_PROCESSING",
		3: "QA_REQUEST",
		4: "RECOMMENDATION",
		5: "USER_ACTIVITY",
		6: "SYSTEM_EVENT",
	}
	for value, want := range cases {
		if got := dao.TopicString(value); got != want {
			t.Fatalf("TopicString(%d) = %q, want %q", value, got, want)
		}
		if !dao.ValidTopic(value) {
			t.Fatalf("ValidTopic(%d) = false, want true", value)
		}
	}
	if got := dao.TopicString(7); got != "UNKNOWN(7)" {
		t.Fatalf("越界枚举应显式成为 UNKNOWN, got %q", got)
	}
	if dao.ValidTopic(-1) || dao.ValidTopic(7) {
		t.Fatal("越界枚举不应通过校验")
	}
}

func TestNewMessage_RejectsInvalidTopicAndPriority(t *testing.T) {
	if _, err := newMessage(7, "x", nil, nil, nil); err == nil {
		t.Fatal("越界 topic 应被拒绝，不能静默写成 UNKNOWN(7)")
	}
	low := int32(0)
	if _, err := newMessage(3, "x", &low, nil, nil); err == nil {
		t.Fatal("越界优先级应被拒绝")
	}
	msg, err := newMessage(3, "x", nil, nil, nil)
	if err != nil {
		t.Fatalf("合法消息不应报错: %v", err)
	}
	if msg.Topic != "QA_REQUEST" {
		t.Fatalf("topic = %q, want QA_REQUEST", msg.Topic)
	}
	if msg.Priority != 2 {
		t.Fatalf("默认优先级 = %d, want 2(NORMAL)", msg.Priority)
	}
}

type recordingOutbox struct {
	outbox.OutboxRepository
	topics []string
	keys   []string
	err    error
}

func (o *recordingOutbox) SaveWithTx(_ context.Context, _ *gorm.DB, topic, key string, _ interface{}) error {
	o.topics = append(o.topics, topic)
	o.keys = append(o.keys, key)
	return o.err
}

type batchRepo struct {
	dao.MessageRepository
	txCalls        int
	committed      bool
	rolledBack     bool
	saveBatchCalls int
	saved          []*model.Message
	saveErr        error
}

func (r *batchRepo) WithTransaction(_ context.Context, fn func(dao.MessageRepository) error) error {
	r.txCalls++
	if err := fn(r); err != nil {
		r.rolledBack = true
		return err
	}
	r.committed = true
	return nil
}

func (r *batchRepo) SaveMessage(_ context.Context, msg *model.Message) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, msg)
	return nil
}

func (r *batchRepo) SaveMessages(_ context.Context, msgs []*model.Message) error {
	r.saveBatchCalls++
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, msgs...)
	return nil
}

func (r *batchRepo) DB() *gorm.DB { return nil }

// 批量发送必须一次事务、一次多值 INSERT、N 条 outbox 事件：
// 否则中途失败会留下已提交的子集，调用方重试又产生重复消息。
func TestBatchSendMessage_SingleTransaction(t *testing.T) {
	repo := &batchRepo{}
	ob := &recordingOutbox{}
	svc := &messageServiceImpl{repo: repo, outboxRepo: ob}

	inputs := []struct {
		Topic         int32
		Content       string
		Priority      *int32
		Headers       map[string]string
		CorrelationID *string
	}{
		{Topic: 3, Content: "q1"},
		{Topic: 6, Content: "e1"},
	}
	msgs, err := svc.BatchSendMessage(context.Background(), inputs)
	if err != nil {
		t.Fatalf("批量发送不应报错: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("返回条数 = %d, want 2", len(msgs))
	}
	if repo.txCalls != 1 {
		t.Fatalf("事务次数 = %d, want 1", repo.txCalls)
	}
	if repo.saveBatchCalls != 1 {
		t.Fatalf("批量 INSERT 次数 = %d, want 1", repo.saveBatchCalls)
	}
	if !repo.committed || repo.rolledBack {
		t.Fatalf("事务应提交: committed=%v rolledBack=%v", repo.committed, repo.rolledBack)
	}
	if msgs[0].Topic != "QA_REQUEST" || msgs[1].Topic != "SYSTEM_EVENT" {
		t.Fatalf("topic 映射错误: %q, %q", msgs[0].Topic, msgs[1].Topic)
	}
	if len(ob.topics) != 2 || ob.topics[0] != mq.TopicQARequest || ob.topics[1] != mq.TopicSystemEvent {
		t.Fatalf("outbox topic 错误: %v", ob.topics)
	}
}

func TestBatchSendMessage_ErrorRollsBackWholeBatch(t *testing.T) {
	repo := &batchRepo{saveErr: errors.New("db down")}
	ob := &recordingOutbox{}
	svc := &messageServiceImpl{repo: repo, outboxRepo: ob}

	_, err := svc.BatchSendMessage(context.Background(), []struct {
		Topic         int32
		Content       string
		Priority      *int32
		Headers       map[string]string
		CorrelationID *string
	}{
		{Topic: 3, Content: "q1"},
	})
	if err == nil {
		t.Fatal("写库失败必须返回错误，不能把部分成功当全部成功")
	}
	if !repo.rolledBack || repo.committed {
		t.Fatalf("失败应回滚整批: committed=%v rolledBack=%v", repo.committed, repo.rolledBack)
	}
	if len(ob.topics) != 0 {
		t.Fatalf("事务失败不应留下 outbox 事件: %v", ob.topics)
	}
}

func TestBatchSendMessage_RejectsInvalidItemBeforeAnyWrite(t *testing.T) {
	repo := &batchRepo{}
	ob := &recordingOutbox{}
	svc := &messageServiceImpl{repo: repo, outboxRepo: ob}

	_, err := svc.BatchSendMessage(context.Background(), []struct {
		Topic         int32
		Content       string
		Priority      *int32
		Headers       map[string]string
		CorrelationID *string
	}{
		{Topic: 3, Content: "ok"},
		{Topic: 99, Content: "bad"},
	})
	if err == nil {
		t.Fatal("含非法条目时应拒绝整批")
	}
	if !strings.Contains(err.Error(), "第 2 条") {
		t.Fatalf("错误信息应定位到具体条目: %v", err)
	}
	if repo.txCalls != 0 {
		t.Fatalf("校验失败不应开启事务, calls = %d", repo.txCalls)
	}
}

func TestBatchSendMessage_RejectsOversizedBatch(t *testing.T) {
	svc := &messageServiceImpl{repo: &batchRepo{}, outboxRepo: &recordingOutbox{}}
	inputs := make([]struct {
		Topic         int32
		Content       string
		Priority      *int32
		Headers       map[string]string
		CorrelationID *string
	}, maxBatchSize+1)
	if _, err := svc.BatchSendMessage(context.Background(), inputs); err == nil {
		t.Fatal("超过批量上限应直接拒绝，避免长事务")
	}
}

// 消费必须先领取再返回：旧实现返回前就把消息改成 PROCESSED，
// 客户端崩溃未 ACK 时消息再也不会被任何消费者看到。
func TestConsumeMessages_ClaimsWithLeaseInsteadOfPreMarking(t *testing.T) {
	repo := &stubMessageRepo{
		claimResult: []*model.Message{{ID: "m1", Topic: "QA_REQUEST"}},
	}
	svc := &messageServiceImpl{repo: repo}

	before := time.Now()
	msgs, err := svc.ConsumeMessages(context.Background(), "group-a", 3, 10, 0)
	if err != nil {
		t.Fatalf("领取消息不应报错: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ID != "m1" {
		t.Fatalf("返回消息不符: %+v", msgs)
	}
	if repo.claimCalls != 1 {
		t.Fatalf("应调用一次原子领取, calls = %d", repo.claimCalls)
	}
	if repo.claimedTopic != "QA_REQUEST" || repo.claimedGroup != "group-a" {
		t.Fatalf("领取参数错误: topic=%q group=%q", repo.claimedTopic, repo.claimedGroup)
	}
	if repo.claimLimit != 10 {
		t.Fatalf("默认 limit = %d, want 10", repo.claimLimit)
	}
	if !repo.leaseUntil.After(before.Add(claimLease - time.Second)) {
		t.Fatalf("租约到期时间过早: %v", repo.leaseUntil)
	}
	if repo.batchCalls != 0 {
		t.Fatalf("不应在返回前额外标记状态, calls = %d", repo.batchCalls)
	}
}

func TestConsumeMessages_RejectsInvalidTopicAndPropagatesClaimError(t *testing.T) {
	svc := &messageServiceImpl{repo: &stubMessageRepo{}}
	if _, err := svc.ConsumeMessages(context.Background(), "g", 7, 10, 0); err == nil {
		t.Fatal("越界 topic 应被拒绝")
	}

	repo := &stubMessageRepo{claimErr: errors.New("db down")}
	svc = &messageServiceImpl{repo: repo}
	if _, err := svc.ConsumeMessages(context.Background(), "g", 3, 10, 0); err == nil {
		t.Fatal("领取失败必须上抛，不能返回空列表让客户端以为队列为空")
	}
}

// timeout_ms 是长轮询预算：空结果不能立刻返回，否则客户端只能空转；
// 预算内拿到消息要立即返回，不能等满整个窗口。
func TestConsumeMessages_LongPollRetriesUntilMessageArrives(t *testing.T) {
	repo := &stubMessageRepo{
		claimResults: [][]*model.Message{
			nil,
			nil,
			{{ID: "m1", Topic: "QA_REQUEST"}},
		},
	}
	svc := &messageServiceImpl{repo: repo}

	start := time.Now()
	msgs, err := svc.ConsumeMessages(context.Background(), "group-a", 3, 10, 600)
	if err != nil {
		t.Fatalf("长轮询不应报错: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ID != "m1" {
		t.Fatalf("应返回第三次领取到的消息, got %+v", msgs)
	}
	if repo.claimCalls != 3 {
		t.Fatalf("应在空结果后继续领取, calls = %d", repo.claimCalls)
	}
	if elapsed := time.Since(start); elapsed < consumePollInterval*2 {
		t.Fatalf("空结果之间必须有间隔, elapsed = %v", elapsed)
	}
	if elapsed := time.Since(start); elapsed > 600*time.Millisecond {
		t.Fatalf("拿到消息应立即返回, elapsed = %v", elapsed)
	}
}

// 预算耗尽返回空列表是正常的长轮询超时，不是错误；
// 但调用必须真的等满预算，而不是“假装”等过。
func TestConsumeMessages_LongPollReturnsEmptyAfterBudget(t *testing.T) {
	repo := &stubMessageRepo{}
	svc := &messageServiceImpl{repo: repo}

	start := time.Now()
	msgs, err := svc.ConsumeMessages(context.Background(), "group-a", 3, 10, 450)
	if err != nil {
		t.Fatalf("预算耗尽应返回空列表而不是错误: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("队列为空时应返回空列表, got %+v", msgs)
	}
	if elapsed := time.Since(start); elapsed < 400*time.Millisecond {
		t.Fatalf("未等满预算就返回, elapsed = %v", elapsed)
	}
	if repo.claimCalls < 2 {
		t.Fatalf("预算内应多次尝试领取, calls = %d", repo.claimCalls)
	}
}

// 客户端断开必须立刻停止轮询，且不能把取消错误吞成“队列为空”。
func TestConsumeMessages_LongPollStopsOnContextCancel(t *testing.T) {
	repo := &stubMessageRepo{}
	svc := &messageServiceImpl{repo: repo}
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(120 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := svc.ConsumeMessages(ctx, "group-a", 3, 10, 5000)
	cancel()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消应上抛 context.Canceled, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("取消后应立即退出, elapsed = %v", elapsed)
	}
	if repo.claimCalls > 3 {
		t.Fatalf("取消后不应继续领取, calls = %d", repo.claimCalls)
	}
}

// timeout_ms <= 0 保持旧语义（只尝试一次），超上限收敛到 30s，
// 避免调用方用天文数字把连接钉死。
func TestNormalizeConsumeTimeout(t *testing.T) {
	cases := []struct {
		in   int32
		want time.Duration
	}{
		{-1, 0},
		{0, 0},
		{100, 100 * time.Millisecond},
		{int32(maxConsumeTimeout / time.Millisecond), maxConsumeTimeout},
		{60000, maxConsumeTimeout},
	}
	for _, c := range cases {
		if got := normalizeConsumeTimeout(c.in); got != c.want {
			t.Fatalf("normalizeConsumeTimeout(%d) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestAcknowledgeMessage_PropagatesNotClaimed(t *testing.T) {
	repo := &stubMessageRepo{updateErr: dao.ErrMessageNotClaimed}
	svc := &messageServiceImpl{repo: repo}
	err := svc.AcknowledgeMessage(context.Background(), "g", "m1", 3)
	if !errors.Is(err, dao.ErrMessageNotClaimed) {
		t.Fatalf("应保留 not claimed 语义供 handler 映射 404, got %v", err)
	}
}

// 统计必须一次 GROUP BY 返回 7 个 topic，且空 topic 补零；
// 旧实现按 topic 查 8 次，空表时 SUM(NULL) 扫描进 int64 直接报错。
func TestGetMessageStats_FillsAllTopicsAndPropagatesError(t *testing.T) {
	repo := &stubMessageRepo{statsRows: []dao.TopicStats{
		{Topic: "QA_REQUEST", Total: 5, Pending: 1, Processed: 3, Failed: 1},
	}}
	svc := &messageServiceImpl{repo: repo}

	stats, err := svc.GetMessageStats(context.Background())
	if err != nil {
		t.Fatalf("统计不应报错: %v", err)
	}
	if len(stats) != 7 {
		t.Fatalf("应返回 7 个 topic, got %d", len(stats))
	}
	byTopic := make(map[string]dao.TopicStats, len(stats))
	for _, stat := range stats {
		byTopic[stat.Topic] = stat
	}
	if byTopic["DATA_COLLECTION"].Total != 0 {
		t.Fatal("没有数据的 topic 应补零而不是缺行")
	}
	qa := byTopic["QA_REQUEST"]
	if qa.Total != 5 || qa.Pending != 1 || qa.Processed != 3 || qa.Failed != 1 {
		t.Fatalf("QA_REQUEST 统计错误: %+v", qa)
	}

	repo.statsErr = errors.New("db down")
	if _, err := svc.GetMessageStats(context.Background()); err == nil {
		t.Fatal("统计查询失败必须上抛，不能返回空列表冒充正常")
	}
}

func TestHandleGenericMessage_QAErrorIsReturnedForRetry(t *testing.T) {
	question := &fakeQuestionService{err: errors.New("llm unavailable")}
	svc := &messageServiceImpl{questionService: question}

	msg := &mq.Message{
		Topic:   mq.TopicQARequest,
		Value:   []byte(`{"content":"hello"}`),
		Context: context.Background(),
	}
	if err := svc.handleGenericMessage(msg); err == nil {
		t.Fatal("问答失败必须返回错误，否则 offset 会被提交、消息永久丢失")
	}
	if question.calls != 1 {
		t.Fatalf("AskQuestion 调用次数 = %d, want 1", question.calls)
	}
}

func TestHandleGenericMessage_QAUsesBoundedChildContext(t *testing.T) {
	question := &fakeQuestionService{}
	svc := &messageServiceImpl{questionService: question}

	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	msg := &mq.Message{
		Topic:   mq.TopicQARequest,
		Value:   []byte(`{"content":"hello"}`),
		Context: parent,
	}
	if err := svc.handleGenericMessage(msg); err != nil {
		t.Fatalf("正常问答不应报错: %v", err)
	}
	if question.gotCtx == nil {
		t.Fatal("AskQuestion 没有收到 ctx")
	}
	if _, ok := question.gotCtx.Deadline(); !ok {
		t.Fatal("问答应带独立超时，避免一条消息永久占住分区")
	}
	if question.ctxErr != nil {
		t.Fatalf("父 ctx 正常时调用瞬间的子 ctx 不应已取消: %v", question.ctxErr)
	}
}

func TestHandleGenericMessage_QAStopsOnLifecycleCancellation(t *testing.T) {
	question := &fakeQuestionService{block: true}
	svc := &messageServiceImpl{questionService: question}

	parent, cancel := context.WithCancel(context.Background())
	cancel()
	msg := &mq.Message{
		Topic:   mq.TopicQARequest,
		Value:   []byte(`{"content":"hello"}`),
		Context: parent,
	}

	done := make(chan error, 1)
	go func() { done <- svc.handleGenericMessage(msg) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("生命周期 ctx 已取消时应返回错误")
		}
	case <-time.After(time.Second):
		t.Fatal("生命周期 ctx 取消后 handler 没有退出")
	}
}

func TestHandleGenericMessage_NonQADoesNotCallQuestionService(t *testing.T) {
	question := &fakeQuestionService{}
	svc := &messageServiceImpl{questionService: question}

	msg := &mq.Message{
		Topic:   mq.TopicRecommendation,
		Value:   []byte(`{"content":"hello"}`),
		Context: context.Background(),
	}
	if err := svc.handleGenericMessage(msg); err != nil {
		t.Fatalf("非问答消息不应报错: %v", err)
	}
	if question.calls != 0 {
		t.Fatalf("非问答消息不应触发 AskQuestion, calls = %d", question.calls)
	}
}

// 批量确认必须一条 SQL 完成，并过滤空/重复 id；失败要上抛而不是当成成功。
func TestBatchAcknowledgeMessages_SingleBatchAndErrorPropagation(t *testing.T) {
	repo := &stubMessageRepo{}
	svc := &messageServiceImpl{repo: repo}

	if err := svc.BatchAcknowledgeMessages(context.Background(), "g", []string{"a", "", "b", "a"}, 4); err != nil {
		t.Fatalf("正常批量确认不应报错: %v", err)
	}
	if repo.batchCalls != 1 {
		t.Fatalf("应只发一条批量 UPDATE, calls = %d", repo.batchCalls)
	}
	if repo.lastStatus != "ACKED" {
		t.Fatalf("status = %q, want ACKED", repo.lastStatus)
	}
	if len(repo.lastIDs) != 2 || repo.lastIDs[0] != "a" || repo.lastIDs[1] != "b" {
		t.Fatalf("id 去重/过滤结果不符: %v", repo.lastIDs)
	}

	repo.batchErr = errors.New("db down")
	if err := svc.BatchAcknowledgeMessages(context.Background(), "g", []string{"a"}, 4); err == nil {
		t.Fatal("批量更新失败必须返回错误，不能被 continue 吞掉")
	}
}

func TestBatchAcknowledgeMessages_EmptyIDsSkipsDatabase(t *testing.T) {
	repo := &stubMessageRepo{}
	svc := &messageServiceImpl{repo: repo}

	if err := svc.BatchAcknowledgeMessages(context.Background(), "g", []string{"", ""}, 4); err != nil {
		t.Fatalf("空批次不应报错: %v", err)
	}
	if repo.batchCalls != 0 {
		t.Fatalf("没有有效 id 时不应访问数据库, calls = %d", repo.batchCalls)
	}
}

// 处理成功但状态更新失败时必须返回错误：否则 offset 被提交，
// 数据库里这条消息会永远停在 PENDING/PROCESSED 之前的状态。
func TestHandleGenericMessage_StatusUpdateErrorIsReturned(t *testing.T) {
	repo := &stubMessageRepo{updateErr: errors.New("db down")}
	svc := &messageServiceImpl{repo: repo}

	msg := &mq.Message{
		Topic:   mq.TopicRecommendation,
		Value:   []byte(`{"message_id":"m-1"}`),
		Context: context.Background(),
	}
	if err := svc.handleGenericMessage(msg); err == nil {
		t.Fatal("状态更新失败应返回错误以触发重试")
	}
}

// Kafka 消费成功后必须写统计认识的状态：历史上写 PROCESSED_FROM_KAFKA，
// 而统计只累加 PROCESSED/ACKED，处理量因此永远显示 0。
func TestHandleGenericMessage_WritesCountedStatus(t *testing.T) {
	repo := &stubMessageRepo{}
	svc := &messageServiceImpl{repo: repo}

	msg := &mq.Message{
		Topic:   mq.TopicRecommendation,
		Value:   []byte(`{"message_id":"m-1"}`),
		Context: context.Background(),
	}
	if err := svc.handleGenericMessage(msg); err != nil {
		t.Fatalf("正常消费不应报错: %v", err)
	}
	if repo.updatedID != "m-1" || repo.updatedTo != model.StatusProcessed {
		t.Fatalf("状态更新 = %q/%q, want m-1/%s", repo.updatedID, repo.updatedTo, model.StatusProcessed)
	}
}

// 类型断言失败曾被 `_` 吞掉，content 非字符串会被当成「没有内容」，
// handler 返回 nil 后 offset 提交，这条问答就永久消失了。
func TestHandleGenericMessage_NonStringContentIsSkipped(t *testing.T) {
	question := &fakeQuestionService{}
	svc := &messageServiceImpl{questionService: question}

	msg := &mq.Message{
		Topic:   mq.TopicQARequest,
		Value:   []byte(`{"message_id":"m-1","content":42}`),
		Context: context.Background(),
	}

	err := svc.handleGenericMessage(msg)
	if !errors.Is(err, mq.ErrSkipMessage) {
		t.Fatalf("类型错误应标记为不可恢复消息，err = %v", err)
	}
	if question.calls != 0 {
		t.Fatalf("坏数据不应调用 AskQuestion, calls = %d", question.calls)
	}
}

// message_id 类型错误时继续处理会更新不了任何记录，消息永远停在 PENDING；
// 必须显式失败，让监控看到生产者 schema 已经和消费者不一致。
func TestHandleGenericMessage_NonStringMessageIDIsSkipped(t *testing.T) {
	repo := &stubMessageRepo{}
	question := &fakeQuestionService{}
	svc := &messageServiceImpl{repo: repo, questionService: question}

	msg := &mq.Message{
		Topic:   mq.TopicQARequest,
		Value:   []byte(`{"message_id":42,"content":"hello"}`),
		Context: context.Background(),
	}

	err := svc.handleGenericMessage(msg)
	if !errors.Is(err, mq.ErrSkipMessage) {
		t.Fatalf("message_id 类型错误应标记为不可恢复消息，err = %v", err)
	}
	if repo.updatedID != "" {
		t.Fatalf("坏数据不应写数据库, updatedID = %q", repo.updatedID)
	}
	if question.calls != 0 {
		t.Fatalf("字段校验失败后不应继续处理业务, calls = %d", question.calls)
	}
}

func TestHandleGenericMessage_MissingQAContentIsSkipped(t *testing.T) {
	question := &fakeQuestionService{}
	svc := &messageServiceImpl{questionService: question}

	msg := &mq.Message{
		Topic:   mq.TopicQARequest,
		Value:   []byte(`{"message_id":"m-1"}`),
		Context: context.Background(),
	}

	err := svc.handleGenericMessage(msg)
	if !errors.Is(err, mq.ErrSkipMessage) {
		t.Fatalf("缺少 content 应标记为不可恢复消息，err = %v", err)
	}
	if question.calls != 0 {
		t.Fatalf("空问题不应调用 AskQuestion, calls = %d", question.calls)
	}
}

// questionService 未注入属于装配错误，返回普通 error 触发重试/死信，
// 而不是把问答静默标成 PROCESSED。
func TestHandleGenericMessage_NilQuestionServiceFails(t *testing.T) {
	svc := &messageServiceImpl{}

	msg := &mq.Message{
		Topic:   mq.TopicQARequest,
		Value:   []byte(`{"message_id":"m-1","content":"hello"}`),
		Context: context.Background(),
	}

	err := svc.handleGenericMessage(msg)
	if err == nil {
		t.Fatal("问答服务未初始化时必须报错，不能静默成功")
	}
	if errors.Is(err, mq.ErrSkipMessage) {
		t.Fatalf("装配错误可以重试，不应按坏数据跳过: %v", err)
	}
}

// message_id 缺失（JSON null 也一样）表示该事件不需要回写消息状态，
// 属于契约允许的情况，不能被误判成类型错误。
func TestHandleGenericMessage_MissingMessageIDSkipsStatusUpdate(t *testing.T) {
	repo := &stubMessageRepo{}
	svc := &messageServiceImpl{repo: repo}

	msg := &mq.Message{
		Topic:   mq.TopicRecommendation,
		Value:   []byte(`{"message_id":null}`),
		Context: context.Background(),
	}

	if err := svc.handleGenericMessage(msg); err != nil {
		t.Fatalf("缺少 message_id 不应报错: %v", err)
	}
	if repo.updatedID != "" {
		t.Fatalf("没有 message_id 时不应写数据库, updatedID = %q", repo.updatedID)
	}
}
