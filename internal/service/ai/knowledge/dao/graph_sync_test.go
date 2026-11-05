package dao

import (
	"context"
	"errors"
	"strings"
	"testing"

	"Logos/internal/service/ai/knowledge/model"
)

func TestHydrateGraphEntities(t *testing.T) {
	ctx := context.Background()
	lookup := func(ctx context.Context, id string) (*model.Entity, error) {
		switch id {
		case "entity-1":
			return &model.Entity{ID: "entity-1", Type: "city", Name: "北京"}, nil
		case "deleted":
			return nil, nil
		case "broken":
			return nil, errors.New("connection reset")
		default:
			return nil, nil
		}
	}

	t.Run("保留顺序并跳过无效节点", func(t *testing.T) {
		nodes := []map[string]any{
			{"id": "entity-1"},
			{"id": ""},
			{"name": "缺少 id"},
			{"id": 42},
			{"id": "deleted"},
			{"id": "entity-2"},
		}
		lookupWithSecond := func(ctx context.Context, id string) (*model.Entity, error) {
			if id == "entity-2" {
				return &model.Entity{ID: "entity-2", Type: "city", Name: "上海"}, nil
			}
			return lookup(ctx, id)
		}

		entities, err := hydrateGraphEntities(ctx, nodes, "单元测试", lookupWithSecond)
		if err != nil {
			t.Fatalf("未预期的错误: %v", err)
		}
		if len(entities) != 2 || entities[0].ID != "entity-1" || entities[1].ID != "entity-2" {
			t.Fatalf("entities = %#v，期望按 Neo4j 顺序返回两个实体", entities)
		}
	})

	t.Run("数据库错误必须上抛", func(t *testing.T) {
		_, err := hydrateGraphEntities(ctx, []map[string]any{{"id": "broken"}}, "单元测试", lookup)
		if err == nil || !strings.Contains(err.Error(), "单元测试：读取实体 broken 失败") {
			t.Fatalf("错误 = %v，期望带来源与实体 ID", err)
		}
	})

	t.Run("空节点返回空切片", func(t *testing.T) {
		entities, err := hydrateGraphEntities(ctx, nil, "单元测试", lookup)
		if err != nil || entities == nil || len(entities) != 0 {
			t.Fatalf("entities = %#v, err = %v", entities, err)
		}
	})
}

func TestHydrateGraphRelations(t *testing.T) {
	ctx := context.Background()
	lookup := func(ctx context.Context, id string) (*model.Relation, error) {
		switch id {
		case "relation-1":
			return &model.Relation{ID: "relation-1", Type: "located_in"}, nil
		case "broken":
			return nil, errors.New("i/o timeout")
		default:
			return nil, nil
		}
	}

	t.Run("跳过已删除的关系", func(t *testing.T) {
		relations, err := hydrateGraphRelations(ctx, []map[string]any{
			{"id": "relation-1"},
			{"id": "deleted"},
			{"type": "missing-id"},
		}, "单元测试", lookup)
		if err != nil {
			t.Fatalf("未预期的错误: %v", err)
		}
		if len(relations) != 1 || relations[0].ID != "relation-1" {
			t.Fatalf("relations = %#v", relations)
		}
	})

	t.Run("数据库错误必须上抛", func(t *testing.T) {
		_, err := hydrateGraphRelations(ctx, []map[string]any{{"id": "broken"}}, "单元测试", lookup)
		if err == nil || !strings.Contains(err.Error(), "读取关系 broken 失败") {
			t.Fatalf("错误 = %v", err)
		}
	})
}

func TestResolveRelationEndpoints(t *testing.T) {
	ctx := context.Background()
	entities := map[string]*model.Entity{
		"entity-1": {ID: "entity-1", Type: "person"},
		"entity-2": {ID: "entity-2", Type: "city"},
	}
	lookup := func(ctx context.Context, id string) (*model.Entity, error) {
		if id == "broken" {
			return nil, errors.New("connection refused")
		}
		return entities[id], nil
	}

	t.Run("返回两端类型", func(t *testing.T) {
		sourceType, targetType, ok, err := resolveRelationEndpoints(ctx, "entity-1", "entity-2", lookup)
		if err != nil || !ok || sourceType != "person" || targetType != "city" {
			t.Fatalf("sourceType=%q targetType=%q ok=%v err=%v", sourceType, targetType, ok, err)
		}
	})

	t.Run("端点不存在时不报错但不写图", func(t *testing.T) {
		_, _, ok, err := resolveRelationEndpoints(ctx, "entity-1", "missing", lookup)
		if err != nil || ok {
			t.Fatalf("ok=%v err=%v，期望跳过同步", ok, err)
		}
	})

	t.Run("源实体读取失败", func(t *testing.T) {
		_, _, ok, err := resolveRelationEndpoints(ctx, "broken", "entity-2", lookup)
		if err == nil || ok || !strings.Contains(err.Error(), "读取关系源实体 broken 失败") {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
	})

	t.Run("目标实体读取失败", func(t *testing.T) {
		_, _, ok, err := resolveRelationEndpoints(ctx, "entity-1", "broken", lookup)
		if err == nil || ok || !strings.Contains(err.Error(), "读取关系目标实体 broken 失败") {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
	})
}

func TestRelationGraphProperties(t *testing.T) {
	relation := &model.Relation{
		Type:         "located_in",
		CollectionID: "col-1",
		Properties:   model.JSONMap{"weight": "0.9", "collectionId": "override"},
	}

	created := relationGraphProperties(relation, true)
	if _, ok := created["createdAt"]; !ok {
		t.Fatal("创建路径应带 createdAt")
	}
	if created["updatedAt"] == nil {
		t.Fatal("创建路径应带 updatedAt")
	}
	if created["collectionId"] != "override" {
		t.Fatalf("自定义属性应覆盖内置字段，实际 = %v", created["collectionId"])
	}

	updated := relationGraphProperties(relation, false)
	if _, ok := updated["createdAt"]; ok {
		t.Fatal("更新路径不应改写 createdAt")
	}
	if updated["weight"] != "0.9" {
		t.Fatalf("weight = %v", updated["weight"])
	}
}
