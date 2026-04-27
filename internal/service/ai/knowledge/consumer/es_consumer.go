
package consumer

import (
	"context"
	"encoding/json"

	"Logos/internal/service/ai/knowledge/model"
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
		logger.Warn("ES not initialized, skipping ES sync")
		return nil
	}

	if c.consumer == nil {
		logger.Warn("Kafka consumer not initialized, skipping ES sync")
		return nil
	}

	logger.Info("Starting ES consumer")

	err := c.consumer.Subscribe(ctx, c.handleMessage)
	if err != nil {
		logger.Error("Failed to subscribe to knowledge_events", logger.ErrorField(err))
		return err
	}

	return nil
}

func (c *ESConsumer) handleMessage(msg *mq.Message) error {
	logger.Info("Received knowledge event",
		logger.StringField("topic", msg.Topic),
		logger.Int64Field("offset", msg.Offset))

	var event map[string]interface{}
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		logger.Error("Failed to parse event", logger.ErrorField(err))
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
		logger.Warn("Unknown event type", logger.StringField("action", action))
		return nil
	}
}

func (c *ESConsumer) handleCreate(event map[string]interface{}) error {
	entityData, ok := event["entity"]
	if !ok {
		logger.Warn("Event missing entity field")
		return nil
	}

	entityJSON, err := json.Marshal(entityData)
	if err != nil {
		logger.Error("Failed to marshal entity", logger.ErrorField(err))
		return err
	}

	var entity model.Entity
	if err := json.Unmarshal(entityJSON, &entity); err != nil {
		logger.Error("Failed to unmarshal entity", logger.ErrorField(err))
		return err
	}

	if err := c.esManager.AddDocument("entities", entity.ID, entity); err != nil {
		logger.Error("Failed to add entity to ES",
			logger.StringField("id", entity.ID),
			logger.ErrorField(err))
		return err
	}

	logger.Info("Successfully added entity to ES", logger.StringField("id", entity.ID))
	return nil
}

func (c *ESConsumer) handleUpdate(event map[string]interface{}) error {
	entityData, ok := event["entity"]
	if !ok {
		logger.Warn("Event missing entity field")
		return nil
	}

	entityJSON, err := json.Marshal(entityData)
	if err != nil {
		logger.Error("Failed to marshal entity", logger.ErrorField(err))
		return err
	}

	var entity model.Entity
	if err := json.Unmarshal(entityJSON, &entity); err != nil {
		logger.Error("Failed to unmarshal entity", logger.ErrorField(err))
		return err
	}

	if err := c.esManager.UpdateDocument("entities", entity.ID, entity); err != nil {
		logger.Error("Failed to update ES document",
			logger.StringField("id", entity.ID),
			logger.ErrorField(err))
		return err
	}

	logger.Info("Successfully updated ES document", logger.StringField("id", entity.ID))
	return nil
}

func (c *ESConsumer) handleDelete(event map[string]interface{}) error {
	entityId, ok := event["entityId"].(string)
	if !ok {
		logger.Warn("Event missing entityId field")
		return nil
	}

	if err := c.esManager.DeleteDocument("entities", entityId); err != nil {
		logger.Error("Failed to delete ES document",
			logger.StringField("id", entityId),
			logger.ErrorField(err))
		return err
	}

	logger.Info("Successfully deleted ES document", logger.StringField("id", entityId))
	return nil
}
