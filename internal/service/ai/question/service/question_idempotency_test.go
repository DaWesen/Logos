package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"Logos/internal/service/ai/question/dao"
	"Logos/internal/service/ai/question/model"

	"github.com/google/uuid"
)

type stubQARepo struct {
	records    map[string]*model.QARecord
	getResults []*model.QARecord
	getCalls   int
	getErr     error
	createErr  error
	created    []*model.QARecord
}

func (r *stubQARepo) CreateQARecord(_ context.Context, record *model.QARecord) error {
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

	answer, confidence, sources, questionID, timestamp, err := svc.AskQuestion(
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
	if len(repo.created) != 0 {
		t.Fatalf("幂等命中不应再次写库, created = %d", len(repo.created))
	}
}

func TestAskQuestion_RequestIDRejectsDifferentUser(t *testing.T) {
	repo := &stubQARepo{records: map[string]*model.QARecord{
		"qa:m-1": {ID: "qa:m-1", Question: "hello", Answer: "secret", UserID: 7},
	}}
	svc := &qaServiceImpl{qaRepo: repo}

	_, _, _, _, _, err := svc.AskQuestion(context.Background(), "hello", 8, nil, "m-1")
	if !errors.Is(err, ErrQARequestIDConflict) {
		t.Fatalf("不同用户复用 request_id 应返回冲突, got %v", err)
	}
}

func TestAskQuestion_RequestIDRejectsDifferentContent(t *testing.T) {
	repo := &stubQARepo{records: map[string]*model.QARecord{
		"qa:m-1": {ID: "qa:m-1", Question: "hello", Answer: "answer", UserID: 7},
	}}
	svc := &qaServiceImpl{qaRepo: repo}

	_, _, _, _, _, err := svc.AskQuestion(context.Background(), "another question", 7, nil, "m-1")
	if !errors.Is(err, ErrQARequestIDConflict) {
		t.Fatalf("同一 request_id 携带不同问题应返回冲突, got %v", err)
	}
}

func TestAskQuestion_EmptyRequestIDKeepsUUIDBehavior(t *testing.T) {
	repo := &stubQARepo{}
	svc := &qaServiceImpl{qaRepo: repo}

	_, _, _, questionID, _, err := svc.AskQuestion(context.Background(), "hello", 7, nil, "")
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

	answer, _, _, questionID, _, err := svc.AskQuestion(
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
}
