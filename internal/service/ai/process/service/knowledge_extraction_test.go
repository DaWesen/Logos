package service

import (
	"context"
	"errors"
	"testing"

	knowledgeModel "Logos/internal/service/ai/knowledge/model"
	"Logos/internal/service/ai/process/model"
)

type fakeExtractionService struct {
	entities []map[string]interface{}
	triples  []map[string]interface{}
	err      error
}

func (f *fakeExtractionService) ExtractFromText(
	ctx context.Context, text string, taskType int32, parameters map[string]string,
) ([]map[string]interface{}, []map[string]interface{}, []map[string]interface{}, *string, []string, error) {
	return f.entities, nil, f.triples, nil, nil, f.err
}

// fakeKnowledgeService 只实现知识提取真正用到的方法，其余返回零值。
type fakeKnowledgeService struct {
	entities  map[string]*knowledgeModel.Entity
	relations []*knowledgeModel.Relation
	findErr   error
	relErr    error
	findCalls int
	seq       int
}

func newFakeKnowledgeService() *fakeKnowledgeService {
	return &fakeKnowledgeService{entities: map[string]*knowledgeModel.Entity{}}
}

func (f *fakeKnowledgeService) FindOrCreateEntity(
	ctx context.Context, entityType, name, collectionID string, properties map[string]string, description *string, color string,
) (*knowledgeModel.Entity, error) {
	f.findCalls++
	if f.findErr != nil {
		return nil, f.findErr
	}
	key := collectionID + "|" + entityType + "|" + name
	if existing, ok := f.entities[key]; ok {
		return existing, nil
	}
	f.seq++
	entity := &knowledgeModel.Entity{
		ID:           "entity-" + name,
		Type:         entityType,
		Name:         name,
		CollectionID: collectionID,
		Properties:   knowledgeModel.JSONMap(properties),
	}
	f.entities[key] = entity
	return entity, nil
}

func (f *fakeKnowledgeService) AddEntity(
	ctx context.Context, entityType, name, collectionID string, properties map[string]string, description *string, color string,
) (*knowledgeModel.Entity, error) {
	return f.FindOrCreateEntity(ctx, entityType, name, collectionID, properties, description, color)
}

func (f *fakeKnowledgeService) AddRelation(
	ctx context.Context, relationType, sourceId, targetId, collectionID string, properties map[string]string, description *string,
) (*knowledgeModel.Relation, error) {
	if f.relErr != nil {
		return nil, f.relErr
	}
	relation := &knowledgeModel.Relation{
		ID:           "relation-1",
		Type:         relationType,
		SourceID:     sourceId,
		TargetID:     targetId,
		CollectionID: collectionID,
	}
	f.relations = append(f.relations, relation)
	return relation, nil
}

func (f *fakeKnowledgeService) UpdateEntity(context.Context, string, string, string, string, map[string]string, *string, string) (*knowledgeModel.Entity, error) {
	return nil, nil
}

func (f *fakeKnowledgeService) DeleteEntity(context.Context, string) error { return nil }

func (f *fakeKnowledgeService) GetEntity(context.Context, string) (*knowledgeModel.Entity, error) {
	return nil, nil
}

func (f *fakeKnowledgeService) QueryEntities(context.Context, string, string, string, map[string]string, int, int) ([]*knowledgeModel.Entity, int64, error) {
	return nil, 0, nil
}

func (f *fakeKnowledgeService) SearchEntities(context.Context, string, *string, string, int, int) ([]*knowledgeModel.Entity, int64, error) {
	return nil, 0, nil
}

func (f *fakeKnowledgeService) UpdateRelation(context.Context, string, string, string, string, map[string]string, *string) (*knowledgeModel.Relation, error) {
	return nil, nil
}

func (f *fakeKnowledgeService) DeleteRelation(context.Context, string) error { return nil }

func (f *fakeKnowledgeService) GetRelation(context.Context, string) (*knowledgeModel.Relation, error) {
	return nil, nil
}

func (f *fakeKnowledgeService) QueryRelations(context.Context, string, string, string, string, int, int) ([]*knowledgeModel.Relation, int64, error) {
	return nil, 0, nil
}

func (f *fakeKnowledgeService) GetGraphStats(context.Context, string) (*knowledgeModel.GraphStats, error) {
	return &knowledgeModel.GraphStats{}, nil
}

func (f *fakeKnowledgeService) GetRelatedEntities(context.Context, string, string) ([]*knowledgeModel.Entity, error) {
	return nil, nil
}

func (f *fakeKnowledgeService) GetSubgraph(context.Context, string, int, string) (*knowledgeModel.Subgraph, error) {
	return nil, nil
}

func (f *fakeKnowledgeService) GetEntityPaths(context.Context, string, string, int, string) ([]*knowledgeModel.EntityPath, error) {
	return nil, nil
}

func (f *fakeKnowledgeService) ImportData(context.Context, string, []string) error { return nil }

func (f *fakeKnowledgeService) WithTransaction(ctx context.Context, fn func(txService KnowledgeService) error) error {
	return fn(f)
}

func newKnowledgeExtractionService(ex ExtractionService, kn KnowledgeService) *ProcessService {
	return &ProcessService{extractionService: ex, knowledgeService: kn}
}

func extractionDoc() *model.Document {
	return &model.Document{ID: "doc-1", FileName: "handbook.md", VectorCollectionID: "col-1"}
}

func TestProcessKnowledgeExtraction_AllEntityCreatesFailReturnsError(t *testing.T) {
	ex := &fakeExtractionService{entities: []map[string]interface{}{
		{"type": "PERSON", "text": "Alice"},
		{"type": "PERSON", "text": "Bob"},
	}}
	kn := newFakeKnowledgeService()
	kn.findErr = errors.New("db 写入超时")
	svc := newKnowledgeExtractionService(ex, kn)

	err := svc.processKnowledgeExtraction(context.Background(), extractionDoc(), "Alice 和 Bob 在讨论发版")
	if err == nil {
		t.Fatal("实体全部写入失败时应返回错误，而不是当成提取成功")
	}
	if !errors.Is(err, ErrKnowledgeExtractionAllFailed) {
		t.Fatalf("错误应可识别为 ErrKnowledgeExtractionAllFailed，实际 %v", err)
	}
	if kn.findCalls != 2 {
		t.Fatalf("两个实体都应尝试写入，findCalls = %d", kn.findCalls)
	}
}

func TestProcessKnowledgeExtraction_MalformedEntitiesAreSkippedWithoutDroppingValidOnes(t *testing.T) {
	ex := &fakeExtractionService{entities: []map[string]interface{}{
		{"type": "PERSON"},
		{"text": 123},
		{"text": "   "},
		{"type": 123, "text": "Carol"},
		{"text": " Alice "},
	}}
	kn := newFakeKnowledgeService()
	svc := newKnowledgeExtractionService(ex, kn)

	err := svc.processKnowledgeExtraction(context.Background(), extractionDoc(), "Alice 在准备发版")
	if err != nil {
		t.Fatalf("只有部分实体非法时不应整体失败: %v", err)
	}
	if len(kn.entities) != 1 {
		t.Fatalf("只应写入唯一合法实体，got %d", len(kn.entities))
	}
	entity := kn.entities["col-1|ENTITY|Alice"]
	if entity == nil {
		t.Fatalf("合法实体应被裁剪空白后写入，got %+v", kn.entities)
	}
}

func TestProcessKnowledgeExtraction_AllTriplesInvalidReturnsError(t *testing.T) {
	ex := &fakeExtractionService{triples: []map[string]interface{}{
		{"subject": "", "predicate": "KNOWS", "object": "Bob"},
		{"predicate": "KNOWS", "object": "Bob"},
		{"subject": "Alice", "predicate": 1, "object": "Bob"},
	}}
	kn := newFakeKnowledgeService()
	svc := newKnowledgeExtractionService(ex, kn)

	err := svc.processKnowledgeExtraction(context.Background(), extractionDoc(), "Alice 认识 Bob")
	if err == nil {
		t.Fatal("三元组全部非法时应返回错误")
	}
	if !errors.Is(err, ErrKnowledgeExtractionAllFailed) {
		t.Fatalf("错误应可识别为 ErrKnowledgeExtractionAllFailed，实际 %v", err)
	}
	if len(kn.relations) != 0 {
		t.Fatalf("非法三元组不应写入关系，got %d", len(kn.relations))
	}
}

func TestProcessKnowledgeExtraction_RelationWriteFailureReturnsError(t *testing.T) {
	ex := &fakeExtractionService{triples: []map[string]interface{}{
		{"subject": "Alice", "predicate": "KNOWS", "object": "Bob"},
	}}
	kn := newFakeKnowledgeService()
	kn.relErr = errors.New("relation insert failed")
	svc := newKnowledgeExtractionService(ex, kn)

	err := svc.processKnowledgeExtraction(context.Background(), extractionDoc(), "Alice 认识 Bob")
	if err == nil {
		t.Fatal("关系写入失败且没有其他产物时应返回错误")
	}
	if !errors.Is(err, ErrKnowledgeExtractionAllFailed) {
		t.Fatalf("错误应可识别为 ErrKnowledgeExtractionAllFailed，实际 %v", err)
	}
}

func TestProcessKnowledgeExtraction_HappyPath(t *testing.T) {
	ex := &fakeExtractionService{
		entities: []map[string]interface{}{
			{"text": "Alice", "role": "owner"},
			{"text": "Bob"},
		},
		triples: []map[string]interface{}{
			{"subject": "Alice", "predicate": "KNOWS", "object": "Bob"},
		},
	}
	kn := newFakeKnowledgeService()
	svc := newKnowledgeExtractionService(ex, kn)

	if err := svc.processKnowledgeExtraction(context.Background(), extractionDoc(), "Alice 认识 Bob"); err != nil {
		t.Fatalf("正常提取不应失败: %v", err)
	}
	if len(kn.entities) != 2 {
		t.Fatalf("关系端点应复用已有实体，got %d", len(kn.entities))
	}
	if len(kn.relations) != 1 || kn.relations[0].Type != "KNOWS" {
		t.Fatalf("应写入一条 KNOWS 关系，got %+v", kn.relations)
	}
	if got := kn.entities["col-1|ENTITY|Alice"].Properties["role"]; got != "owner" {
		t.Fatalf("实体属性应保留，got %q", got)
	}
}

func TestProcessKnowledgeExtraction_NoDataIsNotFailure(t *testing.T) {
	kn := newFakeKnowledgeService()
	svc := newKnowledgeExtractionService(&fakeExtractionService{}, kn)

	if err := svc.processKnowledgeExtraction(context.Background(), extractionDoc(), "一段没有实体的文本"); err != nil {
		t.Fatalf("模型确实没提取到内容时不应报错: %v", err)
	}
	if kn.findCalls != 0 {
		t.Fatalf("没有抽取结果时不应触达知识服务，findCalls = %d", kn.findCalls)
	}
}
