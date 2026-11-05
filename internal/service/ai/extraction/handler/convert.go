package handler

import (
	"encoding/json"
	"fmt"
	"math"

	pb "Logos/proto_gen/extraction"
)

// parseStringField 区分「字段缺失」与「字段类型不对」。
// 旧实现统一写 `v, _ := m[key].(string)`，类型不对时静默得到空串：
// 响应里会出现没有 text 的实体、没有 source/target 的关系，
// 调用方无法判断是模型没抽到，还是服务把数据弄丢了。
func parseStringField(m map[string]interface{}, key string) (string, error) {
	v, ok := m[key]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("字段 %s 类型错误：期望 string，实际 %T", key, v)
	}
	return s, nil
}

func parseFloatField(m map[string]interface{}, key string) (float64, error) {
	v, ok := m[key]
	if !ok || v == nil {
		return 0, nil
	}
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case int32:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, fmt.Errorf("字段 %s 不是合法数字：%w", key, err)
		}
		return f, nil
	}
	return 0, fmt.Errorf("字段 %s 类型错误：期望 number，实际 %T", key, v)
}

// parseInt32Field 额外做范围检查：位置字段来自外部模型输出，
// 直接 int32(f) 在越界时是实现相关的截断，负位置也没有意义。
func parseInt32Field(m map[string]interface{}, key string) (int32, error) {
	f, err := parseFloatField(m, key)
	if err != nil {
		return 0, err
	}
	if f < math.MinInt32 || f > math.MaxInt32 {
		return 0, fmt.Errorf("字段 %s 超出 int32 范围：%v", key, f)
	}
	if f < 0 {
		return 0, fmt.Errorf("字段 %s 不能为负数：%v", key, f)
	}
	return int32(f), nil
}

func entitiesToProto(list []map[string]interface{}) ([]*pb.ExtractedEntity, error) {
	entities := make([]*pb.ExtractedEntity, 0, len(list))
	for i, e := range list {
		id, err := parseStringField(e, "id")
		if err != nil {
			return nil, fmt.Errorf("第 %d 个实体: %w", i+1, err)
		}
		text, err := parseStringField(e, "text")
		if err != nil {
			return nil, fmt.Errorf("第 %d 个实体: %w", i+1, err)
		}
		typ, err := parseStringField(e, "type")
		if err != nil {
			return nil, fmt.Errorf("第 %d 个实体: %w", i+1, err)
		}
		confidence, err := parseFloatField(e, "confidence")
		if err != nil {
			return nil, fmt.Errorf("第 %d 个实体: %w", i+1, err)
		}
		startPos, err := parseInt32Field(e, "startPos")
		if err != nil {
			return nil, fmt.Errorf("第 %d 个实体: %w", i+1, err)
		}
		endPos, err := parseInt32Field(e, "endPos")
		if err != nil {
			return nil, fmt.Errorf("第 %d 个实体: %w", i+1, err)
		}
		if id == "" {
			id = text + "_" + typ
		}
		entities = append(entities, &pb.ExtractedEntity{
			Id:         id,
			Text:       text,
			Type:       typ,
			Confidence: confidence,
			StartPos:   startPos,
			EndPos:     endPos,
		})
	}
	return entities, nil
}

func relationsToProto(list []map[string]interface{}) ([]*pb.ExtractedRelation, error) {
	relations := make([]*pb.ExtractedRelation, 0, len(list))
	for i, r := range list {
		id, err := parseStringField(r, "id")
		if err != nil {
			return nil, fmt.Errorf("第 %d 个关系: %w", i+1, err)
		}
		typ, err := parseStringField(r, "type")
		if err != nil {
			return nil, fmt.Errorf("第 %d 个关系: %w", i+1, err)
		}
		sourceID, err := parseStringField(r, "sourceId")
		if err != nil {
			return nil, fmt.Errorf("第 %d 个关系: %w", i+1, err)
		}
		targetID, err := parseStringField(r, "targetId")
		if err != nil {
			return nil, fmt.Errorf("第 %d 个关系: %w", i+1, err)
		}
		confidence, err := parseFloatField(r, "confidence")
		if err != nil {
			return nil, fmt.Errorf("第 %d 个关系: %w", i+1, err)
		}
		textVal, err := parseStringField(r, "text")
		if err != nil {
			return nil, fmt.Errorf("第 %d 个关系: %w", i+1, err)
		}
		if id == "" {
			id = sourceID + "_" + typ + "_" + targetID
		}
		relations = append(relations, &pb.ExtractedRelation{
			Id:         id,
			Type:       typ,
			SourceId:   sourceID,
			TargetId:   targetID,
			Confidence: confidence,
			Text:       textVal,
		})
	}
	return relations, nil
}

func triplesToProto(list []map[string]interface{}) ([]*pb.Triple, error) {
	triples := make([]*pb.Triple, 0, len(list))
	for i, t := range list {
		id, err := parseStringField(t, "id")
		if err != nil {
			return nil, fmt.Errorf("第 %d 个三元组: %w", i+1, err)
		}
		subject, err := parseStringField(t, "subject")
		if err != nil {
			return nil, fmt.Errorf("第 %d 个三元组: %w", i+1, err)
		}
		predicate, err := parseStringField(t, "predicate")
		if err != nil {
			return nil, fmt.Errorf("第 %d 个三元组: %w", i+1, err)
		}
		object, err := parseStringField(t, "object")
		if err != nil {
			return nil, fmt.Errorf("第 %d 个三元组: %w", i+1, err)
		}
		confidence, err := parseFloatField(t, "confidence")
		if err != nil {
			return nil, fmt.Errorf("第 %d 个三元组: %w", i+1, err)
		}
		if id == "" {
			id = subject + "_" + predicate + "_" + object
		}
		triples = append(triples, &pb.Triple{
			Id:         id,
			Subject:    subject,
			Predicate:  predicate,
			Object:     object,
			Confidence: confidence,
		})
	}
	return triples, nil
}
