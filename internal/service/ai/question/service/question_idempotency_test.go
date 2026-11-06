package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"Logos/internal/service/ai/question/dao"
	"Logos/internal/service/ai/question/model"

	"github.com/google/uuid"
)

type stubQARepo struct {
	mu           sync.Mutex
	records      map[string]*model.QARecord
	getResults   []*model.QARecord
	getCalls     int
	getErr       error
	createErr    error
	replaceErr   error
	replaceCalls int
	created      []*model.QARecord
}

func (r *stubQARepo) CreateQARecord(_ context.Context, record *model.QARecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.created = append(r.created, record)
	if r.createErr != nil {
		return r.createErr
	}
	if r.records == nil {
		r.records = map[string]*model.QARecord{}
	}
	r.records[record.ID] = record
	return nil
}

func (r *stubQARepo) GetQARecord(_ context.Context, id string) (*model.QARecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.getCalls++
	if r.getErr != nil {
		return nil, r.getErr
	}
	if len(r.getResults) > 0 {
		record := r.getResults[0]
		r.getResults = r.getResults[1:]
		return record, nil
	}
	return r.records[id], nil
}

func (r *stubQARepo) ReplaceDegradedAnswer(
	_ context.Context,
	id string,
	answer string,
	confidence float64,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.replaceCalls++
	if r.replaceErr != nil {
		return false, r.replaceErr
	}
	record := r.records[id]
	if record == nil || !record.Degraded {
		return false, nil
	}
	record.Answer = answer
	record.Confidence = confidence
	record.Degraded = false
	return true, nil
}

func (r *stubQARepo) ListQARecords(_ context.Context, _ int64, _, _ int) ([]*model.QARecord, int64, error) {
	return nil, 0, nil
}

func (r *stubQARepo) UpdateFeedback(_ context.Context, _ string, _ string, _ *int32) error {
	return nil
}

var _ dao.QARepository = (*stubQARepo)(nil)

func TestQARecordID_DeterministicAndBounded(t *testing.T) {
	short := qaRecordID("message-1")
	if short != "qa:message-1" {
		t.Fatalf("短 ID 应保留原文, got %q", short)
	}
	if again := qaRecordID("message-1"); again != short {
		t.Fatalf("同一输入必须派生同一主键, got %q then %q", short, again)
	}

	long := qaRecordID(strings.Repeat("x", 200))
	if len(long) != qaRecordIDMaxLen {
		t.Fatalf("超长 ID 长度 = %d, want %d", len(long), qaRecordIDMaxLen)
	}
	if long == qaRecordID(strings.Repeat("x", 199)) {
		t.Fatal("不同超长 ID 不应收敛到同一主键")
	}
}

// 幂等命中必须发生在限流和模型调用之前：重放一条已完成的问题
// 不应再消耗配额，更不应该因为限流把已有答案变成失败。
func TestAskQuestion_RequestIDHitReturnsStoredAnswer(t *testing.T) {
	repo := &stubQARepo{records: map[string]*model.QARecord{
		"qa:m-1": {
			ID:         "qa:m-1",
			Question:   "hello",
			Answer:     "stored answer",
			Confidence: 0.9,
			UserID:     7,
			Timestamp:  100,
		},
	}}
	svc := &qaServiceImpl{qaRepo: repo}

	answer, confidence, sources, questionID, timestamp, degraded, err := svc.AskQuestion(
		context.Background(), "hello", 7, nil, "m-1")
	if err != nil {
		t.Fatalf("幂等命中不应报错: %v", err)
	}
	if answer != "stored answer" || confidence != 0.9 {
		t.Fatalf("应返回已落库答案, got %q/%v", answer, confidence)
	}
	if questionID != "qa:m-1" || timestamp != 100 {
		t.Fatalf("应返回原记录标识与时间, got %q/%d", questionID, timestamp)
	}
	if len(sources) != 0 {
		t.Fatalf("重放不应重新检索来源, got %v", sources)
	}
	if degraded {
		t.Fatal("正常记录的重放不应被标记为降级")
	}
	if len(repo.created) != 0 {
		t.Fatalf("幂等命中不应再次写库, created = %d", len(repo.created))
	}
}

func TestAskQuestion_RequestIDRejectsDifferentUser(t *testing.T) {
	repo := &stubQARepo{records: map[string]*model.QARecord{
		"qa:m-1": {ID: "qa:m-1", Question: "hello", Answer: "secret", UserID: 7},
	}}
	svc := &qaServiceImpl{qaRepo: repo}

	_, _, _, _, _, _, err := svc.AskQuestion(context.Background(), "hello", 8, nil, "m-1")
	if !errors.Is(err, ErrQARequestIDConflict) {
		t.Fatalf("不同用户复用 request_id 应返回冲突, got %v", err)
	}
}

func TestAskQuestion_RequestIDRejectsDifferentContent(t *testing.T) {
	repo := &stubQARepo{records: map[string]*model.QARecord{
		"qa:m-1": {ID: "qa:m-1", Question: "hello", Answer: "answer", UserID: 7},
	}}
	svc := &qaServiceImpl{qaRepo: repo}

	_, _, _, _, _, _, err := svc.AskQuestion(context.Background(), "another question", 7, nil, "m-1")
	if !errors.Is(err, ErrQARequestIDConflict) {
		t.Fatalf("同一 request_id 携带不同问题应返回冲突, got %v", err)
	}
}

func TestAskQuestion_EmptyRequestIDKeepsUUIDBehavior(t *testing.T) {
	repo := &stubQARepo{}
	svc := &qaServiceImpl{qaRepo: repo}

	_, _, _, questionID, _, _, err := svc.AskQuestion(context.Background(), "hello", 7, nil, "")
	if err != nil {
		t.Fatalf("普通问答不应报错: %v", err)
	}
	if repo.getCalls != 0 {
		t.Fatalf("没有 request_id 时不应查询幂等记录, calls = %d", repo.getCalls)
	}
	if _, err := uuid.Parse(questionID); err != nil {
		t.Fatalf("没有 request_id 时应保持 UUID 主键, got %q", questionID)
	}
	if len(repo.created) != 1 || repo.created[0].ID != questionID {
		t.Fatalf("普通问答应正常落库, created = %+v", repo.created)
	}
}

// 两个实例同时处理同一条消息时，后写入者会遇到唯一键冲突；
// 此时应回查先写入的答案，而不是把一次成功问答报成失败。
func TestAskQuestion_ConcurrentCreateFallsBackToStoredRecord(t *testing.T) {
	stored := &model.QARecord{
		ID:         "qa:m-1",
		Question:   "hello",
		Answer:     "winner answer",
		Confidence: 0.9,
		UserID:     7,
		Timestamp:  100,
	}
	repo := &stubQARepo{
		getResults: []*model.QARecord{nil, stored},
		createErr:  errors.New("duplicate key"),
	}
	svc := &qaServiceImpl{qaRepo: repo}

	answer, _, _, questionID, _, degraded, err := svc.AskQuestion(
		context.Background(), "hello", 7, nil, "m-1")
	if err != nil {
		t.Fatalf("唯一键冲突后回查成功不应报错: %v", err)
	}
	if answer != "winner answer" || questionID != "qa:m-1" {
		t.Fatalf("应返回先写入者的答案, got %q/%q", answer, questionID)
	}
	if repo.getCalls != 2 {
		t.Fatalf("应在写入前后各查一次, getCalls = %d", repo.getCalls)
	}
	if degraded {
		t.Fatal("并发窗口里的赢家是正常回答，不应被标记为降级")
	}
}

type stubChat struct {
	answer string
	err    error
}

func (c stubChat) HasChatModel() bool { return true }

func (c stubChat) Chat(context.Context, []string) (string, error) {
	return c.answer, c.err
}

// 幂等键不能把一次模型故障固化成永久答案：命中降级记录时应重新生成，
// 成功后再用条件更新覆盖这条记录。
func TestAskQuestion_DegradedHitRegeneratesAndHeals(t *testing.T) {
	repo := &stubQARepo{
		records: map[string]*model.QARecord{
			"qa:m-1": {
				ID:         "qa:m-1",
				Question:   "hello",
				Answer:     "placeholder",
				Confidence: 0.5,
				UserID:     7,
				Timestamp:  100,
				Degraded:   true,
			},
		},
		createErr: errors.New("duplicate key"),
	}
	svc := &qaServiceImpl{
		qaRepo: repo,
		chat:   stubChat{answer: "fresh answer"},
	}

	answer, confidence, _, questionID, timestamp, degraded, err := svc.AskQuestion(
		context.Background(), "hello", 7, nil, "m-1")
	if err != nil {
		t.Fatalf("降级重试成功不应报错: %v", err)
	}
	if answer != "fresh answer" || confidence != 0.9 || degraded {
		t.Fatalf("应返回新生成的正常回答, got %q/%v/degraded=%v", answer, confidence, degraded)
	}
	if questionID != "qa:m-1" || timestamp != 100 {
		t.Fatalf("应保留原幂等记录标识与首次提问时间, got %q/%d", questionID, timestamp)
	}
	if repo.replaceCalls != 1 {
		t.Fatalf("应尝试覆盖降级记录, calls = %d", repo.replaceCalls)
	}
	if stored := repo.records["qa:m-1"]; stored.Degraded || stored.Answer != "fresh answer" {
		t.Fatalf("降级记录未被升级: %+v", stored)
	}
	if len(repo.created) != 1 || repo.created[0].Degraded {
		t.Fatalf("幂等主键已存在时仍应尝试写入正常回答: %+v", repo.created)
	}
}

// 模型仍不可用时，返回的必须是 degraded=true，调用方才能继续重试；
// 不能把同一个占位文案包装成一次成功回答。
func TestAskQuestion_DegradedRecordStaysRetryableWhenModelStillDown(t *testing.T) {
	repo := &stubQARepo{
		records: map[string]*model.QARecord{
			"qa:m-1": {
				ID: "qa:m-1", Question: "hello", Answer: "placeholder",
				UserID: 7, Timestamp: 100, Degraded: true,
			},
		},
		createErr: errors.New("duplicate key"),
	}
	svc := &qaServiceImpl{qaRepo: repo}

	answer, _, _, _, _, degraded, err := svc.AskQuestion(context.Background(), "hello", 7, nil, "m-1")
	if err != nil {
		t.Fatalf("模型不可用不应让幂等重试直接失败: %v", err)
	}
	if answer != "placeholder" || !degraded {
		t.Fatalf("应返回仍处于降级状态的记录, got %q/degraded=%v", answer, degraded)
	}
	if repo.replaceCalls != 0 {
		t.Fatalf("本次没有生成有效回答，不应覆盖降级记录, calls = %d", repo.replaceCalls)
	}
}

// 首次请求就没有模型可用时，落库记录必须带 degraded 标记，
// 否则后续重试会把它当成已完成答案直接返回。
func TestAskQuestion_FirstAnswerWithoutModelIsPersistedAsDegraded(t *testing.T) {
	repo := &stubQARepo{}
	svc := &qaServiceImpl{qaRepo: repo}

	_, confidence, _, _, _, degraded, err := svc.AskQuestion(context.Background(), "hello", 7, nil, "m-1")
	if err != nil {
		t.Fatalf("模型不可用时应返回兜底答案而不是报错: %v", err)
	}
	if !degraded || confidence != 0.5 {
		t.Fatalf("应标记为降级, got degraded=%v/confidence=%v", degraded, confidence)
	}
	if len(repo.created) != 1 || !repo.created[0].Degraded {
		t.Fatalf("落库记录必须带降级标记: %+v", repo.created)
	}
}
