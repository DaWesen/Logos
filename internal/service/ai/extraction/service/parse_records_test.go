package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	knowledgeModel "Logos/internal/service/ai/knowledge/model"
)

// recordingKnowledge 只实现落库相关的两个方法，其余方法通过内嵌接口占位。
type recordingKnowledge struct {
	KnowledgeService

	entities     []*knowledgeModel.Entity
	relations    []*knowledgeModel.Relation
	addEntityErr error
}

func (r *recordingKnowledge) AddEntity(
	ctx context.Context, entityType, name, collectionID string,
	properties map[string]string, description *string, color string,
) (*knowledgeModel.Entity, error) {
	if r.addEntityErr != nil {
		return nil, r.addEntityErr
	}
	entity := &knowledgeModel.Entity{
		ID:           "entity-" + name,
		Type:         entityType,
		Name:         name,
		CollectionID: collectionID,
		Properties:   knowledgeModel.JSONMap(properties),
		Description:  description,
	}
	r.entities = append(r.entities, entity)
	return entity, nil
}

func (r *recordingKnowledge) AddRelation(
	ctx context.Context, relationType, sourceID, targetID, collectionID string,
	properties map[string]string, description *string,
) (*knowledgeModel.Relation, error) {
	relation := &knowledgeModel.Relation{
		ID:           "relation-" + relationType,
		Type:         relationType,
		SourceID:     sourceID,
		TargetID:     targetID,
		CollectionID: collectionID,
		Description:  description,
	}
	r.relations = append(r.relations, relation)
	return relation, nil
}

func TestToExtractedEntity_RejectsUnusableFields(t *testing.T) {
	cases := []struct {
		name string
		obj  map[string]interface{}
		want string
	}{
		{
			name: "missing text",
			obj:  map[string]interface{}{"type": "PERSON"},
			want: `缺少字段 "text"`,
		},
		{
			name: "null type",
			obj:  map[string]interface{}{"type": nil, "text": "张三"},
			want: `字段 "type" 为 null`,
		},
		{
			name: "numeric text",
			obj:  map[string]interface{}{"type": "PERSON", "text": float64(42)},
			want: `字段 "text" 期望 string，实际 float64`,
		},
		{
			name: "blank type",
			obj:  map[string]interface{}{"type": "  ", "text": "张三"},
			want: `字段 "type" 为空字符串`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := toExtractedEntity(tc.obj)
			if err == nil {
				t.Fatal("字段不可用时应返回错误")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误应说明具体字段，实际 %q", err.Error())
			}
		})
	}
}

func TestToExtractedEntity_KeepsPropertiesAndConfidence(t *testing.T) {
	entity, err := toExtractedEntity(map[string]interface{}{
		"type":       " PERSON ",
		"text":       " 张三 ",
		"confidence": 0.87,
		"startPos":   float64(0),
		"source":     "简历",
	})
	if err != nil {
		t.Fatalf("合法记录不应报错: %v", err)
	}
	if entity.Type != "PERSON" || entity.Name != "张三" {
		t.Fatalf("类型和名称应去除空白，实际 %q / %q", entity.Type, entity.Name)
	}
	if entity.Properties["source"] != "简历" {
		t.Fatalf("字符串字段应保留为属性，实际 %#v", entity.Properties)
	}
	if _, ok := entity.Properties["text"]; ok {
		t.Fatal("text 是实体名称，不应重复写进属性")
	}
	if entity.Description == nil || *entity.Description != "置信度: 0.87" {
		t.Fatalf("置信度描述不正确，实际 %v", entity.Description)
	}
}

func TestConfidenceDescription_IgnoresUnparsableValue(t *testing.T) {
	if got := confidenceDescription(map[string]interface{}{"confidence": "0.9"}); got != nil {
		t.Fatalf("字符串置信度不应生成描述，实际 %q", *got)
	}
	if got := confidenceDescription(map[string]interface{}{"confidence": nil}); got != nil {
		t.Fatalf("null 置信度不应生成描述，实际 %q", *got)
	}
	if got := confidenceDescription(map[string]interface{}{}); got != nil {
		t.Fatalf("缺失置信度不应生成描述，实际 %q", *got)
	}
}

func TestToExtractedRelation_RequiresEndpoints(t *testing.T) {
	if _, err := toExtractedRelation(map[string]interface{}{
		"type":     "WORKS_AT",
		"sourceId": "张三",
	}); err == nil {
		t.Fatal("缺少 targetId 的关系应被拒绝")
	}

	relation, err := toExtractedRelation(map[string]interface{}{
		"type":     "WORKS_AT",
		"sourceId": "张三",
		"targetId": "Logos",
		"text":     "任职于",
	})
	if err != nil {
		t.Fatalf("合法关系不应报错: %v", err)
	}
	if relation.Text != "任职于" {
		t.Fatalf("关系描述应保留，实际 %q", relation.Text)
	}
	if _, ok := relation.Properties["text"]; ok {
		t.Fatal("text 是关系描述，不应重复写进属性")
	}
}

func TestToExtractedTriple_RequiresAllParts(t *testing.T) {
	cases := []map[string]interface{}{
		{"predicate": "就职于", "object": "Logos"},
		{"subject": "张三", "object": "Logos"},
		{"subject": "张三", "predicate": "就职于"},
		{"subject": "张三", "predicate": "", "object": "Logos"},
	}
	for i, obj := range cases {
		if _, err := toExtractedTriple(obj); err == nil {
			t.Fatalf("第 %d 条三元组缺少必要字段，应被拒绝", i)
		}
	}
}

func TestDecodeTaskParameters_RejectsBrokenJSON(t *testing.T) {
	if _, err := decodeTaskParameters(`{"text":`); err == nil {
		t.Fatal("损坏的任务参数应返回错误")
	}

	parameters, err := decodeTaskParameters("")
	if err != nil || parameters == nil {
		t.Fatalf("空参数应返回空 map，实际 %v / %v", parameters, err)
	}
}

func TestPersistEntities_SkipsInvalidRecordAndKeepsValidOnes(t *testing.T) {
	knowledge := &recordingKnowledge{}
	svc := &extractionServiceImpl{knowledgeService: knowledge}

	err := svc.persistEntities(context.Background(), []map[string]interface{}{
		{"type": "PERSON", "text": float64(42)},
		{"type": "PERSON", "text": "张三"},
	}, "collection-1")
	if err != nil {
		t.Fatalf("部分记录可用时不应让任务失败: %v", err)
	}
	if len(knowledge.entities) != 1 {
		t.Fatalf("应只写入 1 个实体，实际 %d 个", len(knowledge.entities))
	}
	if knowledge.entities[0].Name != "张三" {
		t.Fatalf("写入的实体名称不正确: %q", knowledge.entities[0].Name)
	}
}

func TestPersistEntities_FailsWhenNothingWritten(t *testing.T) {
	knowledge := &recordingKnowledge{}
	svc := &extractionServiceImpl{knowledgeService: knowledge}

	err := svc.persistEntities(context.Background(), []map[string]interface{}{
		{"type": "PERSON"},
	}, "collection-1")
	if err == nil {
		t.Fatal("所有记录都非法时任务必须失败，而不是报告抽取成功")
	}
	if !strings.Contains(err.Error(), "未写入任何实体") {
		t.Fatalf("错误应说明没有落库，实际 %q", err.Error())
	}
}

func TestPersistTriples_FailsWhenEntityWriteFails(t *testing.T) {
	knowledge := &recordingKnowledge{addEntityErr: errors.New("db down")}
	svc := &extractionServiceImpl{knowledgeService: knowledge}

	err := svc.persistTriples(context.Background(), []map[string]interface{}{
		{"subject": "张三", "predicate": "就职于", "object": "Logos"},
	}, "collection-1")
	if err == nil {
		t.Fatal("图数据库写入失败时任务必须失败")
	}
}

func TestCreateTask_RejectsInvalidScheduledTime(t *testing.T) {
	repo := newExecRepo()
	svc := &extractionServiceImpl{repo: repo}
	bad := "2026-13-40 25:00"

	if _, err := svc.CreateTask(context.Background(), 1, "ds-1", "data_source", nil, &bad); err == nil {
		t.Fatal("非法 scheduled_time 应返回错误，而不是静默立即执行")
	}
	if len(repo.created) != 0 {
		t.Fatalf("参数非法时不应落库，实际创建 %d 个任务", len(repo.created))
	}
}
