package service

import (
	"context"
	"strings"
	"testing"

	"Logos/internal/service/ai/extraction/model"
)

func TestValidateTaskType(t *testing.T) {
	tests := []struct {
		name    string
		input   int32
		wantErr bool
		// wantMsg 用于区分「未实现」和「完全不认识」两种拒绝原因。
		wantMsg string
	}{
		{name: "实体识别", input: model.TaskTypeEntityRecognition},
		{name: "关系抽取", input: model.TaskTypeRelationExtraction},
		{name: "三元组抽取", input: model.TaskTypeTripleExtraction},
		{name: "关键短语", input: model.TaskTypeKeyphraseExtraction},
		{name: "摘要未实现", input: model.TaskTypeSummarization, wantErr: true, wantMsg: "尚未实现"},
		{name: "零值不合法", input: 0, wantErr: true, wantMsg: "不支持"},
		{name: "未知类型", input: 99, wantErr: true, wantMsg: "不支持"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTaskType(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("类型 %d 应被拒绝", tt.input)
				}
				if tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
					t.Fatalf("错误信息应包含 %q，实际 %v", tt.wantMsg, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("类型 %d 应被接受，实际 %v", tt.input, err)
			}
		})
	}
}

func TestCreateTask_RejectsInvalidTypeBeforePersisting(t *testing.T) {
	for _, taskType := range []int32{0, model.TaskTypeSummarization, 99} {
		repo := &execRepo{}
		svc := NewExtractionService(repo, nil, nil, nil, nil)

		_, err := svc.CreateTask(context.Background(), taskType, "d-1", "text", nil, nil)
		if err == nil {
			t.Fatalf("类型 %d 应被拒绝", taskType)
		}
		// 关键：不能出现「接口返回失败，但库里多了一条待执行任务」。
		if len(repo.created) != 0 {
			t.Fatalf("类型 %d 校验失败后不应落库，实际写入 %d 条", taskType, len(repo.created))
		}
	}
}

func TestCreateTask_PersistsStoredEnumNotProtoValue(t *testing.T) {
	repo := &execRepo{}
	svc := NewExtractionService(repo, nil, nil, nil, nil)

	// service 层只接受已映射好的存储枚举：实体识别必须落 1。
	// handler 负责把 proto 的 0 转成 1，任何一层漏转都会在执行阶段
	// 变成「不支持的任务类型」。
	task, err := svc.CreateTask(context.Background(), 1, "d-1", "text", nil, nil)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if task.Type != model.TaskTypeEntityRecognition {
		t.Fatalf("落库类型应为 %d，实际 %d", model.TaskTypeEntityRecognition, task.Type)
	}
	if task.Status != model.TaskStatusPending {
		t.Fatalf("初始状态应为 %d，实际 %d", model.TaskStatusPending, task.Status)
	}
}

func TestUpdateTask_InvalidTypeDoesNotPersist(t *testing.T) {
	repo := newExecRepo()
	svc := NewExtractionService(repo, nil, nil, nil, nil)
	invalid := int32(model.TaskTypeSummarization)

	_, err := svc.UpdateTask(context.Background(), "task-1", &invalid, nil, nil)
	if err == nil {
		t.Fatal("摘要任务应被拒绝")
	}
	if len(repo.updates) != 0 {
		t.Fatalf("校验失败不应调用 UpdateTask，实际调用 %d 次", len(repo.updates))
	}
	if repo.task.Type != model.TaskTypeEntityRecognition {
		t.Fatalf("校验失败不应改动内存中的类型，实际 %d", repo.task.Type)
	}
}
