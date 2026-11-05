package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"Logos/internal/service/ai/knowledge/dao"
	"Logos/internal/service/ai/knowledge/model"
)

// fakeImportRepo 只实现导入链路真正触达的仓储方法，
// 其余方法由嵌入的 nil 接口兜底，避免为了测导入去构造整张表。
type fakeImportRepo struct {
	dao.KnowledgeRepository
	entities  map[string]*model.Entity
	updates   []*model.Entity
	relations []*model.Relation
}

func (f *fakeImportRepo) FindEntityByNameAndType(ctx context.Context, name, entityType, collectionID, userID string) (*model.Entity, error) {
	entity, ok := f.entities[name]
	if !ok {
		return nil, nil
	}
	return entity, nil
}

func (f *fakeImportRepo) UpdateEntity(ctx context.Context, entity *model.Entity) error {
	f.updates = append(f.updates, entity)
	return nil
}

func (f *fakeImportRepo) CreateRelation(ctx context.Context, relation *model.Relation) error {
	f.relations = append(f.relations, relation)
	return nil
}

func TestImportStringField(t *testing.T) {
	tests := []struct {
		name        string
		data        map[string]any
		field       string
		required    bool
		wantValue   string
		wantPresent bool
		wantErr     string
	}{
		{
			name:        "可选字段缺失",
			data:        map[string]any{},
			field:       "description",
			wantPresent: false,
		},
		{
			name:        "可选字段为 null",
			data:        map[string]any{"description": nil},
			field:       "description",
			wantPresent: false,
		},
		{
			name:        "可选字段为空串",
			data:        map[string]any{"description": ""},
			field:       "description",
			wantPresent: true,
		},
		{
			name:     "必填字段缺失",
			data:     map[string]any{},
			field:    "type",
			required: true,
			wantErr:  `缺少字段 "type"`,
		},
		{
			name:     "必填字段类型错误",
			data:     map[string]any{"type": json.Number("123")},
			field:    "type",
			required: true,
			wantErr:  `字段 "type" 应为字符串，实际为数字`,
		},
		{
			name:     "必填字段为空白串",
			data:     map[string]any{"name": "   "},
			field:    "name",
			required: true,
			wantErr:  `字段 "name" 不能为空`,
		},
		{
			name:        "正常取值",
			data:        map[string]any{"name": "北京"},
			field:       "name",
			required:    true,
			wantValue:   "北京",
			wantPresent: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, present, err := importStringField(test.data, test.field, test.required)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("错误 = %v，期望包含 %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("未预期的错误: %v", err)
			}
			if value != test.wantValue || present != test.wantPresent {
				t.Fatalf("取值 = (%q, %v)，期望 (%q, %v)", value, present, test.wantValue, test.wantPresent)
			}
		})
	}
}

func TestImportStringMap(t *testing.T) {
	t.Run("标量按原文本保留", func(t *testing.T) {
		properties, err := importStringMap(map[string]any{
			"properties": map[string]any{
				"name":       "北京",
				"population": json.Number("21710000"),
				"capital":    true,
				"ignored":    nil,
			},
		}, "properties")
		if err != nil {
			t.Fatalf("未预期的错误: %v", err)
		}
		if properties["name"] != "北京" {
			t.Fatalf("name = %q", properties["name"])
		}
		if properties["population"] != "21710000" {
			t.Fatalf("population = %q，期望保留原始数字文本", properties["population"])
		}
		if properties["capital"] != "true" {
			t.Fatalf("capital = %q", properties["capital"])
		}
		if _, ok := properties["ignored"]; ok {
			t.Fatalf("null 属性不应进入结果: %v", properties)
		}
	})

	t.Run("缺失时返回 nil", func(t *testing.T) {
		properties, err := importStringMap(map[string]any{}, "properties")
		if err != nil || properties != nil {
			t.Fatalf("properties = %v, err = %v", properties, err)
		}
	})

	t.Run("properties 不是对象", func(t *testing.T) {
		_, err := importStringMap(map[string]any{"properties": []any{"a"}}, "properties")
		if err == nil || !strings.Contains(err.Error(), `字段 "properties" 应为对象，实际为数组`) {
			t.Fatalf("错误 = %v", err)
		}
	})

	t.Run("嵌套对象显式报错", func(t *testing.T) {
		_, err := importStringMap(map[string]any{
			"properties": map[string]any{"metadata": map[string]any{"level": "high"}},
		}, "properties")
		if err == nil || !strings.Contains(err.Error(), `属性 "metadata" 是对象`) {
			t.Fatalf("错误 = %v", err)
		}
	})
}

func TestDecodeImportObject(t *testing.T) {
	t.Run("保留大整数精度", func(t *testing.T) {
		data, err := decodeImportObject(`{"id": 12345678901234567890}`)
		if err != nil {
			t.Fatalf("未预期的错误: %v", err)
		}
		number, ok := data["id"].(json.Number)
		if !ok || number.String() != "12345678901234567890" {
			t.Fatalf("id = %#v，期望保留 20 位数字", data["id"])
		}
	})

	tests := []struct {
		name    string
		item    string
		wantErr string
	}{
		{name: "null", item: `null`, wantErr: "内容为 null"},
		{name: "数组", item: `[1,2]`, wantErr: "不是合法的 JSON 对象"},
		{name: "截断", item: `{"type":"city"`, wantErr: "不是合法的 JSON 对象"},
		{name: "尾部多余内容", item: `{"type":"city"}{"type":"person"}`, wantErr: "存在多余内容"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeImportObject(test.item)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("错误 = %v，期望包含 %q", err, test.wantErr)
			}
		})
	}
}

func TestImportEntitiesReportsPerItemFailures(t *testing.T) {
	repo := &fakeImportRepo{entities: map[string]*model.Entity{
		"北京": {
			ID:         "entity-1",
			Type:       "city",
			Name:       "北京",
			Properties: model.JSONMap{},
		},
	}}
	svc := &knowledgeServiceImpl{repo: repo}

	err := svc.importEntities(context.Background(), []string{
		`{"type":"city","name":"北京","collection_id":"col-1","properties":{"population":21710000,"capital":true}}`,
		`{"type":123,"name":"上海"}`,
		`{"type":"city"}`,
		`not-json`,
	})

	if err == nil {
		t.Fatal("部分条目失败时不应返回 nil")
	}
	for _, want := range []string{
		"导入实体部分失败（成功 1/4 条）",
		`第 2 条：字段 "type" 应为字符串，实际为数字`,
		`第 3 条：缺少字段 "name"`,
		"第 4 条：不是合法的 JSON 对象",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误 %q 未包含 %q", err.Error(), want)
		}
	}

	if len(repo.updates) != 1 {
		t.Fatalf("更新次数 = %d，期望 1", len(repo.updates))
	}
	properties := repo.updates[0].Properties
	if properties["population"] != "21710000" || properties["capital"] != "true" {
		t.Fatalf("属性未按原文写入: %v", properties)
	}
}

func TestImportRelationsKeepsCollectionID(t *testing.T) {
	repo := &fakeImportRepo{}
	svc := &knowledgeServiceImpl{repo: repo}

	err := svc.importRelations(context.Background(), []string{
		`{"type":"located_in","sourceId":"entity-1","targetId":"entity-2","collection_id":"col-9","properties":{"since":2020}}`,
		`{"type":"located_in","sourceId":"","targetId":"entity-2"}`,
	})

	if err == nil || !strings.Contains(err.Error(), "导入关系部分失败（成功 1/2 条）") {
		t.Fatalf("错误 = %v", err)
	}
	if len(repo.relations) != 1 {
		t.Fatalf("写入关系数 = %d，期望 1", len(repo.relations))
	}
	relation := repo.relations[0]
	if relation.CollectionID != "col-9" {
		t.Fatalf("collection_id = %q，期望 col-9", relation.CollectionID)
	}
	if relation.Properties["since"] != "2020" {
		t.Fatalf("since = %q，期望 2020", relation.Properties["since"])
	}
}

func TestImportBatchErrorSummarizesFailures(t *testing.T) {
	failures := func(count int) []*importItemError {
		result := make([]*importItemError, 0, count)
		for i := 0; i < count; i++ {
			result = append(result, &importItemError{index: i, err: errors.New("坏数据")})
		}
		return result
	}

	partial := (&importBatchError{kind: "实体", total: 5, succeeded: 1, failures: failures(4)}).Error()
	if !strings.Contains(partial, "导入实体部分失败（成功 1/5 条）") {
		t.Fatalf("错误 = %q", partial)
	}
	if !strings.Contains(partial, "第 1 条") || !strings.Contains(partial, "第 3 条") {
		t.Fatalf("错误 = %q，期望列出前三条", partial)
	}
	if strings.Contains(partial, "第 4 条") {
		t.Fatalf("错误 = %q，明细应截断到三条", partial)
	}
	if !strings.Contains(partial, "另有 1 条失败") {
		t.Fatalf("错误 = %q，应给出剩余失败数", partial)
	}

	all := (&importBatchError{kind: "关系", total: 2, succeeded: 0, failures: failures(2)}).Error()
	if !strings.Contains(all, "导入关系全部失败（成功 0/2 条）") {
		t.Fatalf("错误 = %q", all)
	}
}
