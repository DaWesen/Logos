package handler

import (
	"strings"
	"testing"

	"Logos/internal/service/ai/extraction/model"
)

func TestResultToProto_ValidPayload(t *testing.T) {
	keyphrases := `["a","b"]`
	result := &model.ExtractionResult{
		ID:     "r-1",
		TaskID: "t-1",
		Status: 3,
		Entities: `[{"id":"e1","text":"北京","type":"LOC","confidence":0.9,
			"startPos":0,"endPos":2}]`,
		Relations:  `[{"id":"rel1","type":"located_in","sourceId":"e1","targetId":"e2","text":"位于"}]`,
		Triples:    `[{"subject":"北京","predicate":"是","object":"首都"}]`,
		Keyphrases: &keyphrases,
		StartTime:  100,
		EndTime:    200,
	}

	got, err := resultToProto(result)
	if err != nil {
		t.Fatalf("合法结果不应报错，实际 %v", err)
	}
	if len(got.Entities) != 1 || got.Entities[0].Text != "北京" {
		t.Fatalf("实体解析错误，实际 %#v", got.Entities)
	}
	if got.Entities[0].StartPos != 0 || got.Entities[0].EndPos != 2 {
		t.Fatalf("位置字段解析错误，实际 %#v", got.Entities[0])
	}
	if len(got.Relations) != 1 || got.Relations[0].SourceId != "e1" {
		t.Fatalf("关系解析错误，实际 %#v", got.Relations)
	}
	if len(got.Triples) != 1 || got.Triples[0].Object != "首都" {
		t.Fatalf("三元组解析错误，实际 %#v", got.Triples)
	}
	if len(got.Keyphrases) != 2 {
		t.Fatalf("关键词解析错误，实际 %#v", got.Keyphrases)
	}
}

func TestResultToProto_MalformedJSONIsError(t *testing.T) {
	// 旧实现丢弃 Unmarshal 错误，坏数据会返回 200 + 空实体列表，
	// 与「这次确实没抽到」完全无法区分。
	result := &model.ExtractionResult{ID: "r-1", Entities: `{"not":"an array"}`}

	if _, err := resultToProto(result); err == nil {
		t.Fatal("实体 JSON 结构不合法时应返回错误")
	}
}

func TestResultToProto_FieldTypeMismatchIsError(t *testing.T) {
	// 旧实现 `id, _ := e["id"].(string)` 会把数字 id 静默变成空串。
	result := &model.ExtractionResult{
		ID:       "r-1",
		Entities: `[{"id":123,"text":"x","type":"T"}]`,
	}

	_, err := resultToProto(result)
	if err == nil {
		t.Fatal("字段类型不匹配时应返回错误")
	}
	if !strings.Contains(err.Error(), "第 1 个实体") {
		t.Fatalf("错误应定位到具体条目，实际 %v", err)
	}
}

func TestResultToProto_NegativePositionIsError(t *testing.T) {
	result := &model.ExtractionResult{
		ID:       "r-1",
		Entities: `[{"text":"x","type":"T","startPos":-5}]`,
	}

	if _, err := resultToProto(result); err == nil {
		t.Fatal("负位置应返回错误")
	}
}

func TestEntitiesToProto_DerivesIDWhenMissing(t *testing.T) {
	got, err := entitiesToProto([]map[string]interface{}{
		{"text": "北京", "type": "LOC"},
	})
	if err != nil {
		t.Fatalf("缺省 id 不应报错，实际 %v", err)
	}
	if got[0].Id != "北京_LOC" {
		t.Fatalf("缺省 id 应按 text_type 生成，实际 %q", got[0].Id)
	}
}
