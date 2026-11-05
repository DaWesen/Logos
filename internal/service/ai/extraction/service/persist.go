package service

import (
	"context"
	"errors"
	"fmt"

	"Logos/pkg/logger"
)

// 落库阶段同样不能静默失败：模型可能返回结构合法的 JSON，但字段类型不对；
// 图数据库也可能写失败。这里逐条校验、逐条计数，只要结果集非空却一条都没写进去，
// 就让任务失败，而不是在「抽取成功」的外壳下产出空图谱。

func (s *extractionServiceImpl) persistEntities(ctx context.Context, entities []map[string]interface{}, collectionID string) error {
	written, skipped, failed := 0, 0, 0
	for i, entity := range entities {
		parsed, err := toExtractedEntity(entity)
		if err != nil {
			skipped++
			logger.Warn("跳过字段不符合契约的实体记录",
				logger.IntField("index", i),
				logger.StringField("reason", err.Error()))
			continue
		}
		if _, err := s.knowledgeService.AddEntity(ctx, parsed.Type, parsed.Name, collectionID, parsed.Properties, parsed.Description, ""); err != nil {
			failed++
			logger.Warn("实体写入知识库失败",
				logger.StringField("name", parsed.Name),
				logger.ErrorField(err))
			continue
		}
		written++
	}

	if len(entities) > 0 && written == 0 {
		return fmt.Errorf("实体抽取结果未写入任何实体（共 %d 条，字段非法 %d 条，写入失败 %d 条）", len(entities), skipped, failed)
	}
	if skipped > 0 || failed > 0 {
		logger.Warn("实体抽取结果部分未落库",
			logger.IntField("total", len(entities)),
			logger.IntField("written", written),
			logger.IntField("skipped", skipped),
			logger.IntField("failed", failed))
	}
	return nil
}

func (s *extractionServiceImpl) persistRelations(ctx context.Context, relations []map[string]interface{}, collectionID string) error {
	written, skipped, failed := 0, 0, 0
	for i, rel := range relations {
		parsed, err := toExtractedRelation(rel)
		if err != nil {
			skipped++
			logger.Warn("跳过字段不符合契约的关系记录",
				logger.IntField("index", i),
				logger.StringField("reason", err.Error()))
			continue
		}
		var description *string
		if parsed.Text != "" {
			text := parsed.Text
			description = &text
		}
		if _, err := s.knowledgeService.AddRelation(ctx, parsed.Type, parsed.SourceID, parsed.TargetID, collectionID, parsed.Properties, description); err != nil {
			failed++
			logger.Warn("关系写入知识库失败",
				logger.StringField("type", parsed.Type),
				logger.ErrorField(err))
			continue
		}
		written++
	}

	if len(relations) > 0 && written == 0 {
		return fmt.Errorf("关系抽取结果未写入任何关系（共 %d 条，字段非法 %d 条，写入失败 %d 条）", len(relations), skipped, failed)
	}
	if skipped > 0 || failed > 0 {
		logger.Warn("关系抽取结果部分未落库",
			logger.IntField("total", len(relations)),
			logger.IntField("written", written),
			logger.IntField("skipped", skipped),
			logger.IntField("failed", failed))
	}
	return nil
}

func (s *extractionServiceImpl) persistTriples(ctx context.Context, triples []map[string]interface{}, collectionID string) error {
	written, skipped, failed := 0, 0, 0
	for i, triple := range triples {
		parsed, err := toExtractedTriple(triple)
		if err != nil {
			skipped++
			logger.Warn("跳过字段不符合契约的三元组记录",
				logger.IntField("index", i),
				logger.StringField("reason", err.Error()))
			continue
		}

		sourceEnt, sourceErr := s.knowledgeService.AddEntity(ctx, "ENTITY", parsed.Subject, collectionID, nil, nil, "")
		targetEnt, targetErr := s.knowledgeService.AddEntity(ctx, "ENTITY", parsed.Object, collectionID, nil, nil, "")
		if sourceErr != nil || targetErr != nil {
			failed++
			logger.Warn("三元组实体写入知识库失败",
				logger.StringField("subject", parsed.Subject),
				logger.StringField("object", parsed.Object),
				logger.ErrorField(errors.Join(sourceErr, targetErr)))
			continue
		}

		if sourceEnt == nil || targetEnt == nil || sourceEnt.GetID() == "" || targetEnt.GetID() == "" {
			failed++
			logger.Warn("三元组实体缺少 ID，跳过关系写入",
				logger.StringField("subject", parsed.Subject),
				logger.StringField("object", parsed.Object))
			continue
		}
		if _, err := s.knowledgeService.AddRelation(ctx, parsed.Predicate, sourceEnt.GetID(), targetEnt.GetID(), collectionID, nil, nil); err != nil {
			failed++
			logger.Warn("三元组关系写入知识库失败",
				logger.StringField("predicate", parsed.Predicate),
				logger.ErrorField(err))
			continue
		}
		written++
	}

	if len(triples) > 0 && written == 0 {
		return fmt.Errorf("三元组抽取结果未写入任何三元组（共 %d 条，字段非法 %d 条，写入失败 %d 条）", len(triples), skipped, failed)
	}
	if skipped > 0 || failed > 0 {
		logger.Warn("三元组抽取结果部分未落库",
			logger.IntField("total", len(triples)),
			logger.IntField("written", written),
			logger.IntField("skipped", skipped),
			logger.IntField("failed", failed))
	}
	return nil
}
