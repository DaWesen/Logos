package service

import (
	"encoding/json"
	"fmt"
	"strings"
)

// LLM 的输出属于不可信输入。字段缺失、为 null 或类型不对时，必须显式失败，
// 而不是用裸类型断言把错误悄悄变成空串：后者会在知识图谱里写出
// 「没有名字的实体」「没有端点的关系」，而抽取任务依旧报告成功。
//
// 这里把校验从 IO 循环里抽成纯函数，单测不需要 mock 图数据库。

type extractedEntity struct {
	Type        string
	Name        string
	Properties  map[string]string
	Description *string
}

type extractedRelation struct {
	Type       string
	SourceID   string
	TargetID   string
	Text       string
	Properties map[string]string
}

type extractedTriple struct {
	Subject   string
	Predicate string
	Object    string
}

// decodeTaskParameters 解析任务参数。旧代码忽略 Unmarshal 错误，
// 参数损坏时会退化成「空参数」，于是拿空文本去调模型并成功产出 0 条结果，
// 排障时完全看不出是任务数据坏了。
func decodeTaskParameters(raw string) (map[string]string, error) {
	parameters := make(map[string]string)
	if raw == "" {
		return parameters, nil
	}
	if err := json.Unmarshal([]byte(raw), &parameters); err != nil {
		return nil, fmt.Errorf("解析任务参数失败: %w", err)
	}
	return parameters, nil
}

func requiredString(obj map[string]interface{}, key string) (string, error) {
	raw, exists := obj[key]
	if !exists {
		return "", fmt.Errorf("缺少字段 %q", key)
	}
	if raw == nil {
		return "", fmt.Errorf("字段 %q 为 null", key)
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("字段 %q 期望 string，实际 %T", key, raw)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("字段 %q 为空字符串", key)
	}
	return value, nil
}

// stringProperties 收集对象里的字符串字段作为图属性。
// 非字符串值直接丢弃：属性是可选元数据，缺一条不影响图谱结构。
func stringProperties(obj map[string]interface{}, excluded ...string) map[string]string {
	skip := make(map[string]struct{}, len(excluded))
	for _, key := range excluded {
		skip[key] = struct{}{}
	}

	properties := make(map[string]string)
	for key, value := range obj {
		if _, ok := skip[key]; ok {
			continue
		}
		if str, ok := value.(string); ok {
			properties[key] = str
		}
	}
	return properties
}

// confidenceDescription 只在置信度确实是数字时生成描述。
// 旧代码用 %.2f 直接格式化 interface{}，模型返回字符串时会写出
// "%!f(string=0.9)" 这种垃圾描述。
func confidenceDescription(obj map[string]interface{}) *string {
	raw, exists := obj["confidence"]
	if !exists || raw == nil {
		return nil
	}
	value, ok := raw.(float64)
	if !ok {
		return nil
	}
	description := fmt.Sprintf("置信度: %.2f", value)
	return &description
}

func toExtractedEntity(obj map[string]interface{}) (extractedEntity, error) {
	entityType, err := requiredString(obj, "type")
	if err != nil {
		return extractedEntity{}, err
	}
	name, err := requiredString(obj, "text")
	if err != nil {
		return extractedEntity{}, err
	}

	return extractedEntity{
		Type:        entityType,
		Name:        name,
		Properties:  stringProperties(obj, "type", "text", "confidence"),
		Description: confidenceDescription(obj),
	}, nil
}

func toExtractedRelation(obj map[string]interface{}) (extractedRelation, error) {
	relationType, err := requiredString(obj, "type")
	if err != nil {
		return extractedRelation{}, err
	}
	sourceID, err := requiredString(obj, "sourceId")
	if err != nil {
		return extractedRelation{}, err
	}
	targetID, err := requiredString(obj, "targetId")
	if err != nil {
		return extractedRelation{}, err
	}

	// text 只是关系描述，缺失不影响图谱结构。
	text, _ := obj["text"].(string)

	return extractedRelation{
		Type:       relationType,
		SourceID:   sourceID,
		TargetID:   targetID,
		Text:       text,
		Properties: stringProperties(obj, "type", "sourceId", "targetId", "confidence", "text"),
	}, nil
}

func toExtractedTriple(obj map[string]interface{}) (extractedTriple, error) {
	subject, err := requiredString(obj, "subject")
	if err != nil {
		return extractedTriple{}, err
	}
	predicate, err := requiredString(obj, "predicate")
	if err != nil {
		return extractedTriple{}, err
	}
	object, err := requiredString(obj, "object")
	if err != nil {
		return extractedTriple{}, err
	}

	return extractedTriple{Subject: subject, Predicate: predicate, Object: object}, nil
}
