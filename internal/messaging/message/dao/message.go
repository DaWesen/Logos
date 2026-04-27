package dao

import (
	"context"
	"encoding/json"
	"fmt"

	"Logos/internal/messaging/message/model"

	"gorm.io/gorm"
)

type MessageRepository interface {
	SaveMessage(ctx context.Context, msg *model.Message) error
	GetMessage(ctx context.Context, id string) (*model.Message, error)
	UpdateMessageStatus(ctx context.Context, id string, status string) error
	ListMessagesByTopic(ctx context.Context, topic string, limit int) ([]*model.Message, error)
	ListPendingMessages(ctx context.Context, limit int) ([]*model.Message, error)
	GetMessageStats(ctx context.Context, topic string) (total, pending, processed, failed int64, err error)

	CreateSubscription(ctx context.Context, sub *model.MessageSubscription) error
	GetSubscription(ctx context.Context, topic, consumerGroup string) (*model.MessageSubscription, error)
	ListSubscriptions(ctx context.Context) ([]*model.MessageSubscription, error)
	DeleteSubscription(ctx context.Context, topic, consumerGroup string) error
}

type messageRepository struct {
	db *gorm.DB
}

func NewMessageRepository(db *gorm.DB) MessageRepository {
	return &messageRepository{db: db}
}

func (r *messageRepository) SaveMessage(ctx context.Context, msg *model.Message) error {
	return r.db.WithContext(ctx).Create(msg).Error
}

func (r *messageRepository) GetMessage(ctx context.Context, id string) (*model.Message, error) {
	var m model.Message
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *messageRepository) UpdateMessageStatus(ctx context.Context, id string, status string) error {
	return r.db.WithContext(ctx).Model(&model.Message{}).Where("id = ?", id).Update("status", status).Error
}

func (r *messageRepository) ListMessagesByTopic(ctx context.Context, topic string, limit int) ([]*model.Message, error) {
	var msgs []*model.Message
	query := r.db.WithContext(ctx).Where("topic = ?", topic).Order("timestamp desc")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&msgs).Error
	return msgs, err
}

func (r *messageRepository) ListPendingMessages(ctx context.Context, limit int) ([]*model.Message, error) {
	var msgs []*model.Message
	query := r.db.WithContext(ctx).Where("status = ?", "PENDING").Order("priority desc, timestamp asc")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&msgs).Error
	return msgs, err
}

func (r *messageRepository) GetMessageStats(ctx context.Context, topic string) (total, pending, processed, failed int64, err error) {
	var stats struct {
		Total     int64
		Pending   int64
		Processed int64
		Failed    int64
	}

	query := r.db.WithContext(ctx).Model(&model.Message{})
	if topic != "" {
		query = query.Where("topic = ?", topic)
	}

	query.Select(
		"COUNT(*) as total",
		"SUM(CASE WHEN status='PENDING' THEN 1 ELSE 0 END) as pending",
		"SUM(CASE WHEN status='PROCESSED' OR status='ACKED' THEN 1 ELSE 0 END) as processed",
		"SUM(CASE WHEN status='FAILED' THEN 1 ELSE 0 END) as failed",
	).Scan(&stats)

	return stats.Total, stats.Pending, stats.Processed, stats.Failed, query.Error
}

func (r *messageRepository) CreateSubscription(ctx context.Context, sub *model.MessageSubscription) error {
	return r.db.WithContext(ctx).Create(sub).Error
}

func (r *messageRepository) GetSubscription(ctx context.Context, topic, consumerGroup string) (*model.MessageSubscription, error) {
	var sub model.MessageSubscription
	err := r.db.WithContext(ctx).Where("topic = ? AND consumer_group = ?", topic, consumerGroup).First(&sub).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

func (r *messageRepository) ListSubscriptions(ctx context.Context) ([]*model.MessageSubscription, error) {
	var subs []*model.MessageSubscription
	err := r.db.WithContext(ctx).Find(&subs).Error
	return subs, err
}

func (r *messageRepository) DeleteSubscription(ctx context.Context, topic, consumerGroup string) error {
	return r.db.WithContext(ctx).Where("topic = ? AND consumer_group = ?", topic, consumerGroup).Delete(&model.MessageSubscription{}).Error
}

func MarshalHeaders(m map[string]string) string {
	if m == nil {
		return "{}"
	}
	data, _ := json.Marshal(m)
	return string(data)
}

func UnmarshalHeaders(s string) map[string]string {
	if s == "" || s == "{}" {
		return make(map[string]string)
	}
	var m map[string]string
	json.Unmarshal([]byte(s), &m)
	return m
}

func TopicString(t int) string {
	switch t {
	case 1:
		return "DATA_COLLECTION"
	case 2:
		return "KNOWLEDGE_EXTRACTION"
	case 3:
		return "VECTOR_PROCESSING"
	case 4:
		return "QA_REQUEST"
	case 5:
		return "RECOMMENDATION"
	case 6:
		return "USER_ACTIVITY"
	case 7:
		return "SYSTEM_EVENT"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", t)
	}
}
