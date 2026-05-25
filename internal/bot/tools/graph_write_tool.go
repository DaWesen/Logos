package tools

import (
	"context"
	"fmt"
	"strings"

	"Logos/pkg/logger"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type GraphWriteService interface {
	FindOrCreateEntity(ctx context.Context, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*GraphEntity, error)
	AddRelation(ctx context.Context, relationType, sourceID, targetID, collectionID string, properties map[string]string, description *string) (*GraphRelation, error)
	UpdateEntity(ctx context.Context, id, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*GraphEntity, error)
	DeleteEntity(ctx context.Context, id string) error
	DeleteRelation(ctx context.Context, id string) error
}

type GraphCreateEntityInput struct {
	Name         string            `json:"name" jsonschema:"description=实体名称,必填"`
	Type         string            `json:"type" jsonschema:"description=实体类型(如person,organization,concept,event,technology等),必填"`
	Description  string            `json:"description,omitempty" jsonschema:"description=实体描述,可选"`
	Properties   map[string]string `json:"properties,omitempty" jsonschema:"description=实体属性键值对,可选"`
	CollectionID string            `json:"collection_id,omitempty" jsonschema:"description=所属集合ID,可选"`
	Color        string            `json:"color,omitempty" jsonschema:"description=实体显示颜色,6位十六进制颜色码如#6366f1,可选"`
}

type GraphCreateRelationInput struct {
	SourceName   string            `json:"source_name,omitempty" jsonschema:"description=源实体名称(与source_id二选一)"`
	SourceID     string            `json:"source_id,omitempty" jsonschema:"description=源实体ID(与source_name二选一)"`
	TargetName   string            `json:"target_name,omitempty" jsonschema:"description=目标实体名称(与target_id二选一)"`
	TargetID     string            `json:"target_id,omitempty" jsonschema:"description=目标实体ID(与target_name二选一)"`
	RelationType string            `json:"relation_type" jsonschema:"description=关系类型(如works_for,created,located_in等),必填"`
	Description  string            `json:"description,omitempty" jsonschema:"description=关系描述,可选"`
	Properties   map[string]string `json:"properties,omitempty" jsonschema:"description=关系属性键值对,可选"`
	CollectionID string            `json:"collection_id,omitempty" jsonschema:"description=所属集合ID,可选"`
}

type GraphUpdateEntityInput struct {
	ID          string            `json:"id" jsonschema:"description=要更新的实体ID,必填"`
	Name        string            `json:"name,omitempty" jsonschema:"description=新名称,可选"`
	Type        string            `json:"type,omitempty" jsonschema:"description=新类型,可选"`
	Description string            `json:"description,omitempty" jsonschema:"description=新描述,可选"`
	Properties  map[string]string `json:"properties,omitempty" jsonschema:"description=要更新的属性键值对,会合并到已有属性中,可选"`
	Color       string            `json:"color,omitempty" jsonschema:"description=实体显示颜色,6位十六进制颜色码如#6366f1,可选"`
}

type GraphDeleteEntityInput struct {
	ID string `json:"id" jsonschema:"description=要删除的实体ID,必填"`
}

type GraphDeleteRelationInput struct {
	ID string `json:"id" jsonschema:"description=要删除的关系ID,必填"`
}

func NewGraphCreateEntityTool(svc GraphWriteService, defaultCollectionID string) (tool.InvokableTool, error) {
	t, err := utils.InferTool("graph_create_entity",
		"知识图谱创建实体工具：在知识图谱中创建新的实体节点。"+
			"当你从对话中发现了新的、有价值的事实性知识（人物、组织、概念、事件等）时使用此工具。"+
			"如果同名同类型的实体已存在，会自动合并属性而不会重复创建。"+
			"请确保创建的实体信息准确可靠。",
		func(ctx context.Context, input *GraphCreateEntityInput) (string, error) {
			if input.Name == "" || input.Type == "" {
				return "创建实体失败: 名称和类型为必填项", nil
			}

			collectionID := input.CollectionID
			if collectionID == "" {
				collectionID = defaultCollectionID
			}

			logger.Info("GraphCreateEntityTool 调用",
				logger.StringField("name", input.Name),
				logger.StringField("type", input.Type),
				logger.StringField("collectionId", collectionID))

			properties := input.Properties
			if properties == nil {
				properties = make(map[string]string)
			}
			properties["source"] = "ai_conversation"

			var description *string
			if input.Description != "" {
				description = &input.Description
			}

			entity, err := svc.FindOrCreateEntity(ctx, input.Type, input.Name, collectionID, properties, description, input.Color)
			if err != nil {
				logger.Error("创建实体失败", logger.ErrorField(err))
				return fmt.Sprintf("创建实体失败: %s", err.Error()), nil
			}

			if entity == nil {
				return "创建实体失败: 返回结果为空", nil
			}

			return fmt.Sprintf("实体操作成功:\n- ID: %s\n- 名称: %s\n- 类型: %s\n- 描述: %s\n- 集合: %s\n提示: 可以使用 graph_create_relation 工具为此实体创建关系。",
				entity.ID, entity.Name, entity.Type, entity.Description, entity.CollectionID), nil
		})
	if err != nil {
		return nil, fmt.Errorf("创建 GraphCreateEntityTool 失败: %w", err)
	}
	return t, nil
}

func NewGraphCreateRelationTool(svc GraphWriteService, searchSvc GraphService, defaultCollectionID string) (tool.InvokableTool, error) {
	t, err := utils.InferTool("graph_create_relation",
		"知识图谱创建关系工具：在两个实体之间创建关系。"+
			"当你发现了两个实体之间的关联（如人物与组织的关系、事件与地点的关系等）时使用此工具。"+
			"可以通过实体ID或名称指定源和目标实体。使用名称时会自动搜索匹配的实体。",
		func(ctx context.Context, input *GraphCreateRelationInput) (string, error) {
			if input.RelationType == "" {
				return "创建关系失败: 关系类型为必填项", nil
			}

			collectionID := input.CollectionID
			if collectionID == "" {
				collectionID = defaultCollectionID
			}

			sourceID := input.SourceID
			targetID := input.TargetID

			if sourceID == "" && input.SourceName != "" {
				entities, err := searchSvc.SearchEntities(ctx, input.SourceName, nil, collectionID, 1, 5)
				if err != nil || len(entities) == 0 {
					return fmt.Sprintf("创建关系失败: 找不到源实体 '%s'。请先使用 graph_create_entity 创建该实体。", input.SourceName), nil
				}
				sourceID = entities[0].ID
				logger.Info("通过名称找到源实体",
					logger.StringField("name", input.SourceName),
					logger.StringField("id", sourceID))
			}

			if targetID == "" && input.TargetName != "" {
				entities, err := searchSvc.SearchEntities(ctx, input.TargetName, nil, collectionID, 1, 5)
				if err != nil || len(entities) == 0 {
					return fmt.Sprintf("创建关系失败: 找不到目标实体 '%s'。请先使用 graph_create_entity 创建该实体。", input.TargetName), nil
				}
				targetID = entities[0].ID
				logger.Info("通过名称找到目标实体",
					logger.StringField("name", input.TargetName),
					logger.StringField("id", targetID))
			}

			if sourceID == "" || targetID == "" {
				return "创建关系失败: 必须指定源实体和目标实体（通过ID或名称）", nil
			}

			logger.Info("GraphCreateRelationTool 调用",
				logger.StringField("sourceId", sourceID),
				logger.StringField("targetId", targetID),
				logger.StringField("type", input.RelationType),
				logger.StringField("collectionId", collectionID))

			properties := input.Properties
			if properties == nil {
				properties = make(map[string]string)
			}
			properties["source"] = "ai_conversation"

			var description *string
			if input.Description != "" {
				description = &input.Description
			}

			relation, err := svc.AddRelation(ctx, input.RelationType, sourceID, targetID, collectionID, properties, description)
			if err != nil {
				logger.Error("创建关系失败", logger.ErrorField(err))
				return fmt.Sprintf("创建关系失败: %s", err.Error()), nil
			}

			if relation == nil {
				return "创建关系失败: 返回结果为空", nil
			}

			return fmt.Sprintf("关系创建成功:\n- ID: %s\n- 类型: %s\n- 源: %s\n- 目标: %s\n- 描述: %s",
				relation.ID, relation.Type, relation.SourceID, relation.TargetID, relation.Description), nil
		})
	if err != nil {
		return nil, fmt.Errorf("创建 GraphCreateRelationTool 失败: %w", err)
	}
	return t, nil
}

func NewGraphUpdateEntityTool(svc GraphWriteService) (tool.InvokableTool, error) {
	t, err := utils.InferTool("graph_update_entity",
		"知识图谱更新实体工具：更新知识图谱中已有实体的信息。"+
			"当你发现已有实体的描述、属性等信息需要补充或修正时使用此工具。"+
			"只会更新你指定的字段，不会影响其他字段。",
		func(ctx context.Context, input *GraphUpdateEntityInput) (string, error) {
			if input.ID == "" {
				return "更新实体失败: 实体ID为必填项", nil
			}

			logger.Info("GraphUpdateEntityTool 调用",
				logger.StringField("id", input.ID))

			entity, err := svc.UpdateEntity(ctx, input.ID, input.Type, input.Name, "", input.Properties, nil, input.Color)
			if err != nil {
				logger.Error("更新实体失败", logger.ErrorField(err))
				return fmt.Sprintf("更新实体失败: %s", err.Error()), nil
			}

			if entity == nil {
				return "更新实体失败: 实体不存在", nil
			}

			var updated []string
			if input.Name != "" {
				updated = append(updated, fmt.Sprintf("名称→%s", entity.Name))
			}
			if input.Type != "" {
				updated = append(updated, fmt.Sprintf("类型→%s", entity.Type))
			}
			if len(input.Properties) > 0 {
				updated = append(updated, fmt.Sprintf("属性→%d项", len(input.Properties)))
			}
			if input.Color != "" {
				updated = append(updated, fmt.Sprintf("颜色→%s", input.Color))
			}

			return fmt.Sprintf("实体更新成功:\n- ID: %s\n- 更新内容: %s",
				entity.ID, strings.Join(updated, ", ")), nil
		})
	if err != nil {
		return nil, fmt.Errorf("创建 GraphUpdateEntityTool 失败: %w", err)
	}
	return t, nil
}

func NewGraphDeleteEntityTool(svc GraphWriteService) (tool.InvokableTool, error) {
	t, err := utils.InferTool("graph_delete_entity",
		"知识图谱删除实体工具：从知识图谱中永久删除指定实体以及该实体相关的所有关系。"+
			"当你需要清理过时、错误或不再需要的实体时使用此工具。"+
			"注意：删除实体后相关的关系也会被自动删除，此操作不可逆。",
		func(ctx context.Context, input *GraphDeleteEntityInput) (string, error) {
			if input.ID == "" {
				return "删除实体失败: 实体ID为必填项", nil
			}

			logger.Info("GraphDeleteEntityTool 调用",
				logger.StringField("id", input.ID))

			if err := svc.DeleteEntity(ctx, input.ID); err != nil {
				logger.Error("删除实体失败", logger.ErrorField(err))
				return fmt.Sprintf("删除实体失败: %s", err.Error()), nil
			}

			return fmt.Sprintf("实体已成功删除:\n- ID: %s\n注意: 该实体相关的所有关系也已自动删除。", input.ID), nil
		})
	if err != nil {
		return nil, fmt.Errorf("创建 GraphDeleteEntityTool 失败: %w", err)
	}
	return t, nil
}

func NewGraphDeleteRelationTool(svc GraphWriteService) (tool.InvokableTool, error) {
	t, err := utils.InferTool("graph_delete_relation",
		"知识图谱删除关系工具：从知识图谱中删除两个实体之间的指定关系。"+
			"当你需要移除不再准确或无关的实体关联时使用此工具。"+
			"注意：此操作只会删除关系，不会影响关系两端的实体本身。",
		func(ctx context.Context, input *GraphDeleteRelationInput) (string, error) {
			if input.ID == "" {
				return "删除关系失败: 关系ID为必填项", nil
			}

			logger.Info("GraphDeleteRelationTool 调用",
				logger.StringField("id", input.ID))

			if err := svc.DeleteRelation(ctx, input.ID); err != nil {
				logger.Error("删除关系失败", logger.ErrorField(err))
				return fmt.Sprintf("删除关系失败: %s", err.Error()), nil
			}

			return fmt.Sprintf("关系已成功删除:\n- ID: %s", input.ID), nil
		})
	if err != nil {
		return nil, fmt.Errorf("创建 GraphDeleteRelationTool 失败: %w", err)
	}
	return t, nil
}

func BuildGraphWriteTools(svc GraphWriteService, searchSvc GraphService, defaultCollectionID string) []tool.BaseTool {
	var tools []tool.BaseTool

	createEntityTool, err := NewGraphCreateEntityTool(svc, defaultCollectionID)
	if err != nil {
		logger.Warn("创建图谱实体创建工具失败", logger.ErrorField(err))
	} else {
		tools = append(tools, createEntityTool)
	}

	createRelationTool, err := NewGraphCreateRelationTool(svc, searchSvc, defaultCollectionID)
	if err != nil {
		logger.Warn("创建图谱关系创建工具失败", logger.ErrorField(err))
	} else {
		tools = append(tools, createRelationTool)
	}

	updateEntityTool, err := NewGraphUpdateEntityTool(svc)
	if err != nil {
		logger.Warn("创建图谱实体更新工具失败", logger.ErrorField(err))
	} else {
		tools = append(tools, updateEntityTool)
	}

	deleteEntityTool, err := NewGraphDeleteEntityTool(svc)
	if err != nil {
		logger.Warn("创建图谱实体删除工具失败", logger.ErrorField(err))
	} else {
		tools = append(tools, deleteEntityTool)
	}

	deleteRelationTool, err := NewGraphDeleteRelationTool(svc)
	if err != nil {
		logger.Warn("创建图谱关系删除工具失败", logger.ErrorField(err))
	} else {
		tools = append(tools, deleteRelationTool)
	}

	return tools
}

func FormatGraphToolsInfo(createdTools []tool.BaseTool) string {
	toolNames := make(map[string]bool)
	for _, t := range createdTools {
		info, err := t.Info(context.Background())
		if err == nil && info != nil {
			toolNames[info.Name] = true
		}
	}

	var sb strings.Builder
	sb.WriteString("\n你可以通过以下工具操作知识图谱：\n\n")

	readTools := []struct {
		name string
		desc string
	}{
		{"graph_search", "搜索实体（按关键词、类型）"},
		{"graph_explore", "探索实体关联（从某实体展开子图）"},
		{"graph_path", "查找两个实体之间的路径"},
	}

	writeTools := []struct {
		name string
		desc string
	}{
		{"graph_create_entity", "创建新实体（自动去重）"},
		{"graph_create_relation", "创建实体间关系"},
		{"graph_update_entity", "更新实体信息"},
	}

	deleteTools := []struct {
		name string
		desc string
	}{
		{"graph_delete_entity", "删除实体（同时删除其所有关系）"},
		{"graph_delete_relation", "删除指定关系"},
	}

	hasRead := false
	for _, t := range readTools {
		if toolNames[t.name] {
			if !hasRead {
				sb.WriteString("**读取工具：**\n")
				hasRead = true
			}
			sb.WriteString(fmt.Sprintf("- `%s` — %s\n", t.name, t.desc))
		}
	}

	hasWrite := false
	for _, t := range writeTools {
		if toolNames[t.name] {
			if !hasWrite {
				if hasRead {
					sb.WriteString("\n")
				}
				sb.WriteString("**写入工具：**\n")
				hasWrite = true
			}
			sb.WriteString(fmt.Sprintf("- `%s` — %s\n", t.name, t.desc))
		}
	}

	hasDelete := false
	for _, t := range deleteTools {
		if toolNames[t.name] {
			if !hasDelete {
				if hasRead || hasWrite {
					sb.WriteString("\n")
				}
				sb.WriteString("**删除工具：**\n")
				hasDelete = true
			}
			sb.WriteString(fmt.Sprintf("- `%s` — %s\n", t.name, t.desc))
		}
	}

	if hasRead || hasWrite || hasDelete {
		sb.WriteString("\n**使用建议：**\n")
		sb.WriteString("- 当对话中产生新的、可靠的事实性知识时，主动使用写入工具保存到图谱\n")
		sb.WriteString("- 回答涉及实体关系的问题时，先用读取工具查图谱再回答\n")
		sb.WriteString("- 创建关系时可以用实体名称代替ID，系统会自动查找\n")
	}

	return sb.String()
}
