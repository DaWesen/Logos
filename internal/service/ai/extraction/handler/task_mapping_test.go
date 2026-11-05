package handler

import (
	"strings"
	"testing"
	"time"

	"Logos/internal/service/ai/extraction/model"
	pb "Logos/proto_gen/extraction"
)

// proto 是 0 基、存储层是 1 基，且 SUMMARIZATION 插在 KEYPHRASE 前面。
// 这组用例锁住「逐个显式映射」，防止有人改回 int32(req.Type)。
func TestTaskTypeFromProto(t *testing.T) {
	tests := []struct {
		name    string
		input   pb.ExtractionTaskType
		want    int32
		wantErr bool
	}{
		{name: "实体识别", input: pb.ExtractionTaskType_ENTITY_RECOGNITION, want: model.TaskTypeEntityRecognition},
		{name: "关系抽取", input: pb.ExtractionTaskType_RELATION_EXTRACTION, want: model.TaskTypeRelationExtraction},
		{name: "三元组抽取", input: pb.ExtractionTaskType_TRIPLE_EXTRACTION, want: model.TaskTypeTripleExtraction},
		{name: "关键短语", input: pb.ExtractionTaskType_KEYPHRASE_EXTRACTION, want: model.TaskTypeKeyphraseExtraction},
		{name: "摘要未实现", input: pb.ExtractionTaskType_SUMMARIZATION, wantErr: true},
		{name: "未知类型", input: pb.ExtractionTaskType(99), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := taskTypeFromProto(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("类型 %d 应返回错误", int32(tt.input))
				}
				return
			}
			if err != nil {
				t.Fatalf("类型 %d 不应报错，实际 %v", int32(tt.input), err)
			}
			if got != tt.want {
				t.Fatalf("映射错误，期望 %d 实际 %d", tt.want, got)
			}
		})
	}
}

func TestTaskTypeToProto(t *testing.T) {
	tests := []struct {
		name    string
		input   int
		want    pb.ExtractionTaskType
		wantErr bool
	}{
		{name: "实体识别", input: model.TaskTypeEntityRecognition, want: pb.ExtractionTaskType_ENTITY_RECOGNITION},
		{name: "关系抽取", input: model.TaskTypeRelationExtraction, want: pb.ExtractionTaskType_RELATION_EXTRACTION},
		{name: "三元组抽取", input: model.TaskTypeTripleExtraction, want: pb.ExtractionTaskType_TRIPLE_EXTRACTION},
		{name: "摘要", input: model.TaskTypeSummarization, want: pb.ExtractionTaskType_SUMMARIZATION},
		{name: "关键短语", input: model.TaskTypeKeyphraseExtraction, want: pb.ExtractionTaskType_KEYPHRASE_EXTRACTION},
		{name: "未知类型", input: 99, wantErr: true},
		{name: "零值不合法", input: 0, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := taskTypeToProto(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("类型 %d 应返回错误", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("类型 %d 不应报错，实际 %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("映射错误，期望 %v 实际 %v", tt.want, got)
			}
		})
	}
}

// 双向映射必须能原样往返，否则创建时存进去的类型读出来会变成另一种。
func TestTaskTypeRoundTrip(t *testing.T) {
	for _, protoType := range []pb.ExtractionTaskType{
		pb.ExtractionTaskType_ENTITY_RECOGNITION,
		pb.ExtractionTaskType_RELATION_EXTRACTION,
		pb.ExtractionTaskType_TRIPLE_EXTRACTION,
		pb.ExtractionTaskType_KEYPHRASE_EXTRACTION,
	} {
		stored, err := taskTypeFromProto(protoType)
		if err != nil {
			t.Fatalf("proto %v 无法转换: %v", protoType, err)
		}
		back, err := taskTypeToProto(int(stored))
		if err != nil {
			t.Fatalf("存储类型 %d 无法转回: %v", stored, err)
		}
		if back != protoType {
			t.Fatalf("往返后类型变了，期望 %v 实际 %v", protoType, back)
		}
	}
}

func TestTaskStatusToProto(t *testing.T) {
	tests := []struct {
		name    string
		input   int
		want    pb.TaskStatus
		wantErr bool
	}{
		{name: "待执行", input: model.TaskStatusPending, want: pb.TaskStatus_PENDING},
		{name: "运行中", input: model.TaskStatusRunning, want: pb.TaskStatus_RUNNING},
		{name: "成功", input: model.TaskStatusSucceeded, want: pb.TaskStatus_SUCCESS},
		{name: "失败", input: model.TaskStatusFailed, want: pb.TaskStatus_FAILED},
		{name: "已取消", input: model.TaskStatusCancelled, want: pb.TaskStatus_CANCELLED},
		{name: "未知状态", input: 99, wantErr: true},
		{name: "零值不合法", input: 0, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := taskStatusToProto(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("状态 %d 应返回错误", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("状态 %d 不应报错，实际 %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("映射错误，期望 %v 实际 %v", tt.want, got)
			}
		})
	}
}

type fakeTask struct {
	id        string
	taskType  int
	status    int
	params    string
	createdAt time.Time
	updatedAt time.Time
}

func (f fakeTask) GetID() string           { return f.id }
func (f fakeTask) GetType() int            { return f.taskType }
func (f fakeTask) GetDataID() string       { return "d-1" }
func (f fakeTask) GetDataType() string     { return "text" }
func (f fakeTask) GetStatus() int          { return f.status }
func (f fakeTask) GetParameters() string   { return f.params }
func (f fakeTask) GetCreatedAt() time.Time { return f.createdAt }
func (f fakeTask) GetUpdatedAt() time.Time { return f.updatedAt }

func TestExtractionToProto_MapsTypeAndStatus(t *testing.T) {
	got, err := extractionToProto(fakeTask{
		id:        "t-1",
		taskType:  model.TaskTypeKeyphraseExtraction,
		status:    model.TaskStatusSucceeded,
		params:    `{"lang":"zh"}`,
		createdAt: time.Unix(100, 0),
		updatedAt: time.Unix(200, 0),
	})
	if err != nil {
		t.Fatalf("合法任务不应报错，实际 %v", err)
	}
	if got.Type != pb.ExtractionTaskType_KEYPHRASE_EXTRACTION {
		t.Fatalf("任务类型映射错误，实际 %v", got.Type)
	}
	if got.Status != pb.TaskStatus_SUCCESS {
		t.Fatalf("任务状态映射错误，实际 %v", got.Status)
	}
	if got.Parameters["lang"] != "zh" {
		t.Fatalf("参数解析错误，实际 %#v", got.Parameters)
	}
}

func TestExtractionToProto_InvalidEnumIsError(t *testing.T) {
	// 历史坏行如果被强转回包，前端会把 PENDING 当 RUNNING；
	// 这里要求转换失败而不是猜一个「最接近」的值。
	_, err := extractionToProto(fakeTask{id: "t-1", taskType: 99, status: model.TaskStatusPending})
	if err == nil || !strings.Contains(err.Error(), "任务类型") {
		t.Fatalf("非法任务类型应返回错误，实际 %v", err)
	}

	_, err = extractionToProto(fakeTask{id: "t-1", taskType: model.TaskTypeEntityRecognition, status: 0})
	if err == nil || !strings.Contains(err.Error(), "任务状态") {
		t.Fatalf("非法任务状态应返回错误，实际 %v", err)
	}
}

func TestExtractionToProto_MalformedParametersIsError(t *testing.T) {
	_, err := extractionToProto(fakeTask{
		id:       "t-1",
		taskType: model.TaskTypeEntityRecognition,
		status:   model.TaskStatusPending,
		params:   `{"lang":`,
	})
	if err == nil {
		t.Fatal("参数 JSON 损坏时应返回错误，而不是当作空参数")
	}
}
