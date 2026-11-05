package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"Logos/pkg/logger"
)

// importFieldProblem 区分「没给」「给了空串」「给错类型」三种失败，
// 否则 `"type": 123` 会和字段缺失一样只报「缺少必要字段」，无法定位问题。
type importFieldProblem int

const (
	importFieldMissing importFieldProblem = iota
	importFieldEmpty
	importFieldWrongType
)

type importFieldError struct {
	field   string
	problem importFieldProblem
	actual  any
}

func (e *importFieldError) Error() string {
	switch e.problem {
	case importFieldMissing:
		return fmt.Sprintf("缺少字段 %q", e.field)
	case importFieldEmpty:
		return fmt.Sprintf("字段 %q 不能为空", e.field)
	default:
		return fmt.Sprintf("字段 %q 应为字符串，实际为%s", e.field, importValueType(e.actual))
	}
}

// importValueType 把 JSON 解码后的 Go 类型翻译成可读的类型名，
// 避免把 json.Number、map[string]interface{} 之类的实现细节直接回给调用方。
func importValueType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case string:
		return "字符串"
	case bool:
		return "布尔值"
	case json.Number, float64, int, int64:
		return "数字"
	case map[string]any:
		return "对象"
	case []any:
		return "数组"
	default:
		return fmt.Sprintf("%T", value)
	}
}

// importStringField 读取字符串字段。present 表示键是否存在且非 null，
// 调用方据此区分「没提供 description」与「提供了空 description」。
func importStringField(data map[string]any, field string, required bool) (value string, present bool, err error) {
	raw, ok := data[field]
	if !ok || raw == nil {
		if required {
			return "", false, &importFieldError{field: field, problem: importFieldMissing}
		}
		return "", false, nil
	}
	text, ok := raw.(string)
	if !ok {
		return "", true, &importFieldError{field: field, problem: importFieldWrongType, actual: raw}
	}
	if required && strings.TrimSpace(text) == "" {
		return "", true, &importFieldError{field: field, problem: importFieldEmpty}
	}
	return text, true, nil
}

// importStringMap 把 properties 压平成 map[string]string。
// JSON 标量按原文本保留（UseNumber 保证大整数不丢精度），
// null 视为未提供；对象/数组无法无损表达，直接报错而不是静默丢弃。
func importStringMap(data map[string]any, field string) (map[string]string, error) {
	raw, ok := data[field]
	if !ok || raw == nil {
		return nil, nil
	}
	props, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("字段 %q 应为对象，实际为%s", field, importValueType(raw))
	}
	result := make(map[string]string, len(props))
	for key, value := range props {
		switch typed := value.(type) {
		case nil:
			continue
		case string:
			result[key] = typed
		case json.Number:
			result[key] = typed.String()
		case bool:
			result[key] = strconv.FormatBool(typed)
		default:
			return nil, fmt.Errorf("字段 %q 的属性 %q 是%s，属性值只支持标量", field, key, importValueType(value))
		}
	}
	return result, nil
}

// decodeImportObject 解析单条导入数据。
// 使用 UseNumber 保留数字字面量，避免 19 位 ID 在 float64 里被改写。
func decodeImportObject(item string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(item))
	decoder.UseNumber()

	var data map[string]any
	if err := decoder.Decode(&data); err != nil {
		return nil, fmt.Errorf("不是合法的 JSON 对象：%w", err)
	}
	if data == nil {
		return nil, fmt.Errorf("不是合法的 JSON 对象：内容为 null")
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("JSON 对象之后存在多余内容")
	}
	return data, nil
}

// importItemError 把失败定位到批次内的具体条目，而不是只留一行日志。
type importItemError struct {
	index int
	err   error
}

func (e *importItemError) Error() string {
	return fmt.Sprintf("第 %d 条：%v", e.index+1, e.err)
}

// importBatchError 汇总一次批量导入的成败。
// 以前逐条 Warn 后仍返回 nil，调用方收到的是「导入成功」，丢了多少数据无人知晓。
type importBatchError struct {
	kind      string
	total     int
	succeeded int
	failures  []*importItemError
}

func (e *importBatchError) Error() string {
	state := "部分失败"
	if e.succeeded == 0 {
		state = "全部失败"
	}

	const maxDetails = 3
	var builder strings.Builder
	fmt.Fprintf(&builder, "导入%s%s（成功 %d/%d 条）", e.kind, state, e.succeeded, e.total)
	for i, failure := range e.failures {
		if i == maxDetails {
			fmt.Fprintf(&builder, "；另有 %d 条失败", len(e.failures)-maxDetails)
			break
		}
		builder.WriteString("；")
		builder.WriteString(failure.Error())
	}
	return builder.String()
}

func (s *knowledgeServiceImpl) importEntities(ctx context.Context, data []string) error {
	succeeded := 0
	failures := make([]*importItemError, 0)
	for index, item := range data {
		if err := s.importEntity(ctx, item); err != nil {
			logger.Warn("导入实体失败",
				logger.IntField("index", index+1),
				logger.ErrorField(err))
			failures = append(failures, &importItemError{index: index, err: err})
			continue
		}
		succeeded++
	}

	logger.Info("导入实体完成",
		logger.IntField("total", len(data)),
		logger.IntField("succeeded", succeeded),
		logger.IntField("failed", len(failures)))
	if len(failures) > 0 {
		return &importBatchError{kind: "实体", total: len(data), succeeded: succeeded, failures: failures}
	}
	return nil
}

func (s *knowledgeServiceImpl) importEntity(ctx context.Context, item string) error {
	entityData, err := decodeImportObject(item)
	if err != nil {
		return err
	}

	entityType, _, err := importStringField(entityData, "type", true)
	if err != nil {
		return err
	}
	name, _, err := importStringField(entityData, "name", true)
	if err != nil {
		return err
	}
	collectionID, _, err := importStringField(entityData, "collection_id", false)
	if err != nil {
		return err
	}
	properties, err := importStringMap(entityData, "properties")
	if err != nil {
		return err
	}

	descriptionText, hasDescription, err := importStringField(entityData, "description", false)
	if err != nil {
		return err
	}
	var description *string
	if hasDescription {
		description = &descriptionText
	}

	_, err = s.FindOrCreateEntity(ctx, entityType, name, collectionID, properties, description, "")
	return err
}

func (s *knowledgeServiceImpl) importRelations(ctx context.Context, data []string) error {
	succeeded := 0
	failures := make([]*importItemError, 0)
	for index, item := range data {
		if err := s.importRelation(ctx, item); err != nil {
			logger.Warn("导入关系失败",
				logger.IntField("index", index+1),
				logger.ErrorField(err))
			failures = append(failures, &importItemError{index: index, err: err})
			continue
		}
		succeeded++
	}

	logger.Info("导入关系完成",
		logger.IntField("total", len(data)),
		logger.IntField("succeeded", succeeded),
		logger.IntField("failed", len(failures)))
	if len(failures) > 0 {
		return &importBatchError{kind: "关系", total: len(data), succeeded: succeeded, failures: failures}
	}
	return nil
}

func (s *knowledgeServiceImpl) importRelation(ctx context.Context, item string) error {
	relationData, err := decodeImportObject(item)
	if err != nil {
		return err
	}

	relationType, _, err := importStringField(relationData, "type", true)
	if err != nil {
		return err
	}
	sourceID, _, err := importStringField(relationData, "sourceId", true)
	if err != nil {
		return err
	}
	targetID, _, err := importStringField(relationData, "targetId", true)
	if err != nil {
		return err
	}
	// 关系与实体必须落在同一个集合里；这里以前硬编码空串，
	// 导致导入的关系永远查不到 collection_id。
	collectionID, _, err := importStringField(relationData, "collection_id", false)
	if err != nil {
		return err
	}
	properties, err := importStringMap(relationData, "properties")
	if err != nil {
		return err
	}

	descriptionText, hasDescription, err := importStringField(relationData, "description", false)
	if err != nil {
		return err
	}
	var description *string
	if hasDescription {
		description = &descriptionText
	}

	_, err = s.AddRelation(ctx, relationType, sourceID, targetID, collectionID, properties, description)
	return err
}
