package dao

import (
	"context"
	"encoding/json"
	"fmt"

	"Logos/internal/service/messaging/message/model"

	"gorm.io/gorm"
)

type MessageRepository interface {
	SaveMessage(ctx context.Context, msg *model.Message) error
	SaveMessages(ctx context.Context, msgs []*model.Message) error
	GetMessage(ctx context.Context, id string) (*model.Message, error)
	UpdateMessageStatus(ctx context.Context, id string, status string) error
	UpdateMessagesStatus(ctx context.Context, ids []string, status string) error
	ListMessagesByTopic(ctx context.Context, topic string, limit int) ([]*model.Message, error)
	ListPendingMessages(ctx context.Context, limit int) ([]*model.Message, error)
	ListPendingMessagesByTopic(ctx context.Context, topic string, limit int) ([]*model.Message, error)
	DeleteMessagesByTopic(ctx context.Context, topic string) error
	ClearMessagesByTopic(ctx context.Context, topic string) error
	DeleteSubscriptionsByTopic(ctx context.Context, topic string) error
	GetMessageStats(ctx context.Context, topic string) (total, pending, processed, failed int64, err error)

	CreateSubscription(ctx context.Context, sub *model.MessageSubscription) error
	GetSubscription(ctx context.Context, topic, consumerGroup string) (*model.MessageSubscription, error)
	ListSubscriptions(ctx context.Context) ([]*model.MessageSubscription, error)
	DeleteSubscription(ctx context.Context, topic, consumerGroup string) error

	WithTransaction(ctx context.Context, fn func(txRepo MessageRepository) error) error
	DB() *gorm.DB
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

// SaveMessages 用一条多值 INSERT 写入整批消息。
// 批量发送若逐条 Create，既多 N 次往返，也无法和 outbox 事件放进同一个事务。
func (r *messageRepository) SaveMessages(ctx context.Context, msgs []*model.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).CreateInBatches(msgs, 100).Error
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

// UpdateMessagesStatus 用一条 UPDATE 完成批量确认。
//
// 逐条 UpdateMessageStatus 是 N 次数据库往返；调用方还容易「失败 continue、
// 最后返回 nil」，让客户端把部分失败当成全部成功。单条语句要么全部生效，
// 要么返回错误由上游重试。
func (r *messageRepository) UpdateMessagesStatus(ctx context.Context, ids []string, status string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Model(&model.Message{}).
		Where("id IN ?", ids).
		Update("status", status).Error
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

func (r *messageRepository) ListPendingMessagesByTopic(ctx context.Context, topic string, limit int) ([]*model.Message, error) {
	var msgs []*model.Message
	query := r.db.WithContext(ctx).Where("status = ? AND topic = ?", "PENDING", topic).Order("priority desc, timestamp asc")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&msgs).Error
	return msgs, err
}

func (r *messageRepository) DeleteMessagesByTopic(ctx context.Context, topic string) error {
	return r.db.WithContext(ctx).Where("topic = ?", topic).Delete(&model.Message{}).Error
}

func (r *messageRepository) ClearMessagesByTopic(ctx context.Context, topic string) error {
	return r.db.WithContext(ctx).Model(&model.Message{}).
		Where("topic = ?", topic).
		Update("status", "CLEARED").Error
}

func (r *messageRepository) DeleteSubscriptionsByTopic(ctx context.Context, topic string) error {
	return r.db.WithContext(ctx).Where("topic = ?", topic).Delete(&model.MessageSubscription{}).Error
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

func (r *messageRepository) WithTransaction(ctx context.Context, fn func(txRepo MessageRepository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txRepo := &messageRepository{db: tx}
		return fn(txRepo)
	})
}

func (r *messageRepository) DB() *gorm.DB {
	return r.db
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

// TopicString 把 gRPC 的 Topic 枚举值映射为内部 topic 名。
//
// proto 枚举从 0 开始（DATA_COLLECTION = 0），历史 DAO 实现却从 1 开始，
// 于是所有 topic 整体错位一格：DATA_COLLECTION 变成 UNKNOWN(0)，
// SYSTEM_EVENT 永远不可达，消息被投到相邻业务 topic。枚举边界只保留这一份映射。
func TopicString(t int) string {
	switch t {
	case 0:
		return "DATA_COLLECTION"
	case 1:
		return "KNOWLEDGE_EXTRACTION"
	case 2:
		return "VECTOR_PROCESSING"
	case 3:
		return "QA_REQUEST"
	case 4:
		return "RECOMMENDATION"
	case 5:
		return "USER_ACTIVITY"
	case 6:
		return "SYSTEM_EVENT"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", t)
	}
}

// ValidTopic 判断 topic 枚举是否落在已定义范围内。
func ValidTopic(t int) bool {
	return t >= 0 && t <= 6
}
