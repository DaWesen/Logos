package consumer

import (
	"context"
	"encoding/json"

	"Logos/internal/ai/knowledge/model"
	"Logos/pkg/es"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
)

type ESConsumer struct {
	consumer  *mq.Consumer
	esManager *es.ESManager
}

func NewESConsumer(consumer *mq.Consumer, esManager *es.ESManager) *ESConsumer {
	return &ESConsumer{
		consumer:  consumer,
		esManager: esManager,
	}
}

func (c *ESConsumer) Start(ctx context.Context) error {
	if c.esManager == nil {
		logger.Warn("ES未初始化，跳过ES消费者启动")
		return nil
	}

	if c.consumer == nil {
		logger.Warn("Kafka消费者未初始化，跳过ES消费者启动")
		return nil
	}

	logger.Info("启动ES消费者")

	err := c.consumer.Subscribe(ctx, c.handleMessage)
	if err != nil {
		logger.Error("订阅knowledge_events失败", logger.ErrorField(err))
		return err
	}

	return nil
}

func (c *ESConsumer) handleMessage(msg *mq.Message) error {
	logger.Info("收到知识变更事件",
		logger.StringField("topic", msg.Topic),
		logger.Int64Field("offset", msg.Offset))

	var event map[string]interface{}
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		logger.Error("解析事件失败", logger.ErrorField(err))
		return err
	}

	action, _ := event["action"].(string)

	switch action {
	case "create":
		return c.handleCreate(event)
	case "update":
		return c.handleUpdate(event)
	case "delete":
		return c.handleDelete(event)
	default:
		logger.Warn("未知的事件类型", logger.StringField("action", action))
		return nil
	}
}

func (c *ESConsumer) handleCreate(event map[string]interface{}) error {
	entityData, ok := event["entity"]
	if !ok {
		logger.Warn("事件缺少entity字段")
		return nil
	}

	entityJSON, err := json.Marshal(entityData)
	if err != nil {
		logger.Error("序列化实体失败", logger.ErrorField(err))
		return err
	}

	var entity model.Entity
	if err := json.Unmarshal(entityJSON, &entity); err != nil {
		logger.Error("反序列化实体失败", logger.ErrorField(err))
		return err
	}

	if err := c.esManager.AddDocument("entities", entity.ID, entity); err != nil {
		logger.Error("索引实体到ES失败",
			logger.StringField("id", entity.ID),
			logger.ErrorField(err))
		return err
	}

	logger.Info("异步索引实体到ES成功", logger.StringField("id", entity.ID))
	return nil
}

func (c *ESConsumer) handleUpdate(event map[string]interface{}) error {
	entityData, ok := event["entity"]
	if !ok {
		logger.Warn("事件缺少entity字段")
		return nil
	}

	entityJSON, err := json.Marshal(entityData)
	if err != nil {
		logger.Error("序列化实体失败", logger.ErrorField(err))
		return err
	}

	var entity model.Entity
	if err := json.Unmarshal(entityJSON, &entity); err != nil {
		logger.Error("反序列化实体失败", logger.ErrorField(err))
		return err
	}

	if err := c.esManager.UpdateDocument("entities", entity.ID, entity); err != nil {
		logger.Error("更新ES索引失败",
			logger.StringField("id", entity.ID),
			logger.ErrorField(err))
		return err
	}

	logger.Info("异步更新ES索引成功", logger.StringField("id", entity.ID))
	return nil
}

func (c *ESConsumer) handleDelete(event map[string]interface{}) error {
	entityId, ok := event["entityId"].(string)
	if !ok {
		logger.Warn("事件缺少entityId字段")
		return nil
	}

	if err := c.esManager.DeleteDocument("entities", entityId); err != nil {
		logger.Error("删除ES索引失败",
			logger.StringField("id", entityId),
			logger.ErrorField(err))
		return err
	}

	logger.Info("异步删除ES索引成功", logger.StringField("id", entityId))
	return nil
}
