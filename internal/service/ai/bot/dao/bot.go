package dao

import (
	"context"
	"errors"
	"time"

	"Logos/internal/service/ai/bot/model"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrBotNotFound          = errors.New("Bot不存在")
	ErrConversationNotFound = errors.New("对话不存在")
	ErrMessageNotFound      = errors.New("消息不存在")
	ErrPromptNotFound       = errors.New("提示词不存在")
)

// BotRepository Bot 数据仓库接口
type BotRepository interface {
	CreateBot(ctx context.Context, bot *model.Bot) error
	UpdateBot(ctx context.Context, bot *model.Bot) error
	DeleteBot(ctx context.Context, id string) error
	GetBot(ctx context.Context, id string) (*model.Bot, error)
	GetBotByName(ctx context.Context, userId, name string) (*model.Bot, error)
	ListBots(ctx context.Context, userId, botType, status string, offset, limit int) ([]*model.Bot, error)
	CountBots(ctx context.Context, userId, botType, status string) (int64, error)

	CreateConversation(ctx context.Context, conversation *model.Conversation) error
	UpdateConversation(ctx context.Context, conversation *model.Conversation) error
	DeleteConversation(ctx context.Context, id string) error
	GetConversation(ctx context.Context, id string) (*model.Conversation, error)
	ListConversations(ctx context.Context, botId, userId string, offset, limit int) ([]*model.Conversation, error)
	CountConversations(ctx context.Context, botId, userId string) (int64, error)

	AddMessage(ctx context.Context, message *model.Message) error
	GetMessages(ctx context.Context, conversationId string, limit int, beforeTime *time.Time) ([]*model.Message, error)

	CreatePrompt(ctx context.Context, prompt *model.Prompt) error
	UpdatePrompt(ctx context.Context, prompt *model.Prompt) error
	DeletePrompt(ctx context.Context, id string) error
	GetPrompt(ctx context.Context, id string) (*model.Prompt, error)
	ListPrompts(ctx context.Context, userId, promptType string, isPreset, isPublic *bool, offset, limit int) ([]*model.Prompt, error)
	CountPrompts(ctx context.Context, userId, promptType string, isPreset, isPublic *bool) (int64, error)

	GetUserMemory(ctx context.Context, userID, botID string) ([]*model.UserMemory, error)
	SetUserMemory(ctx context.Context, memory *model.UserMemory) error
	DeleteUserMemory(ctx context.Context, userID, botID, key string) error

	WithTransaction(ctx context.Context, fn func(txRepo BotRepository) error) error
}

// botRepository Bot 数据仓库实现
type botRepository struct {
	db *gorm.DB
}

// NewBotRepository 创建 Bot 数据仓库
func NewBotRepository(db *gorm.DB) BotRepository {
	return &botRepository{db: db}
}

func (r *botRepository) CreateBot(ctx context.Context, bot *model.Bot) error {
	if bot.ID == "" {
		bot.ID = uuid.NewString()
	}
	return r.db.WithContext(ctx).Create(bot).Error
}

func (r *botRepository) UpdateBot(ctx context.Context, bot *model.Bot) error {
	return r.db.WithContext(ctx).Save(bot).Error
}

func (r *botRepository) DeleteBot(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&model.Bot{}, "id = ?", id).Error
}

func (r *botRepository) GetBot(ctx context.Context, id string) (*model.Bot, error) {
	var bot model.Bot
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&bot).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &bot, nil
}

func (r *botRepository) GetBotByName(ctx context.Context, userId, name string) (*model.Bot, error) {
	var bot model.Bot
	err := r.db.WithContext(ctx).Where("user_id = ? AND name = ?", userId, name).First(&bot).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &bot, nil
}

func (r *botRepository) ListBots(ctx context.Context, userId, botType, status string, offset, limit int) ([]*model.Bot, error) {
	query := r.db.WithContext(ctx).Model(&model.Bot{})

	if userId != "" {
		query = query.Where("user_id = ?", userId)
	}
	if botType != "" {
		query = query.Where("type = ?", botType)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}

	var bots []*model.Bot
	err := query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&bots).Error
	return bots, err
}

func (r *botRepository) CountBots(ctx context.Context, userId, botType, status string) (int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Bot{})

	if userId != "" {
		query = query.Where("user_id = ?", userId)
	}
	if botType != "" {
		query = query.Where("type = ?", botType)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}

	var count int64
	err := query.Count(&count).Error
	return count, err
}

func (r *botRepository) CreateConversation(ctx context.Context, conversation *model.Conversation) error {
	if conversation.ID == "" {
		conversation.ID = uuid.NewString()
	}
	return r.db.WithContext(ctx).Create(conversation).Error
}

func (r *botRepository) UpdateConversation(ctx context.Context, conversation *model.Conversation) error {
	return r.db.WithContext(ctx).Save(conversation).Error
}

func (r *botRepository) DeleteConversation(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&model.Conversation{}, "id = ?", id).Error
}

func (r *botRepository) GetConversation(ctx context.Context, id string) (*model.Conversation, error) {
	var conversation model.Conversation
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&conversation).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &conversation, nil
}

func (r *botRepository) ListConversations(ctx context.Context, botId, userId string, offset, limit int) ([]*model.Conversation, error) {
	query := r.db.WithContext(ctx).Model(&model.Conversation{})

	if botId != "" {
		query = query.Where("bot_id = ?", botId)
	}
	if userId != "" {
		query = query.Where("user_id = ?", userId)
	}

	var conversations []*model.Conversation
	err := query.Offset(offset).Limit(limit).Order("updated_at DESC").Find(&conversations).Error
	return conversations, err
}

func (r *botRepository) CountConversations(ctx context.Context, botId, userId string) (int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Conversation{})

	if botId != "" {
		query = query.Where("bot_id = ?", botId)
	}
	if userId != "" {
		query = query.Where("user_id = ?", userId)
	}

	var count int64
	err := query.Count(&count).Error
	return count, err
}

func (r *botRepository) AddMessage(ctx context.Context, message *model.Message) error {
	if message.ID == "" {
		message.ID = uuid.NewString()
	}
	return r.db.WithContext(ctx).Create(message).Error
}

func (r *botRepository) GetMessages(ctx context.Context, conversationId string, limit int, beforeTime *time.Time) ([]*model.Message, error) {
	query := r.db.WithContext(ctx).Model(&model.Message{}).Where("conversation_id = ?", conversationId)

	if beforeTime != nil {
		query = query.Where("created_at < ?", beforeTime)
	}

	var messages []*model.Message
	err := query.Order("created_at DESC").Limit(limit).Find(&messages).Error
	return messages, err
}

func (r *botRepository) CreatePrompt(ctx context.Context, prompt *model.Prompt) error {
	if prompt.ID == "" {
		prompt.ID = uuid.NewString()
	}
	return r.db.WithContext(ctx).Create(prompt).Error
}

func (r *botRepository) UpdatePrompt(ctx context.Context, prompt *model.Prompt) error {
	return r.db.WithContext(ctx).Save(prompt).Error
}

func (r *botRepository) DeletePrompt(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&model.Prompt{}, "id = ?", id).Error
}

func (r *botRepository) GetPrompt(ctx context.Context, id string) (*model.Prompt, error) {
	var prompt model.Prompt
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&prompt).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &prompt, nil
}

func (r *botRepository) ListPrompts(ctx context.Context, userId, promptType string, isPreset, isPublic *bool, offset, limit int) ([]*model.Prompt, error) {
	query := r.db.WithContext(ctx).Model(&model.Prompt{})

	if userId != "" {
		query = query.Where("user_id = ?", userId)
	}
	if promptType != "" {
		query = query.Where("type = ?", promptType)
	}
	if isPreset != nil {
		query = query.Where("is_preset = ?", *isPreset)
	}
	if isPublic != nil {
		query = query.Where("is_public = ?", *isPublic)
	}

	var prompts []*model.Prompt
	err := query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&prompts).Error
	return prompts, err
}

func (r *botRepository) CountPrompts(ctx context.Context, userId, promptType string, isPreset, isPublic *bool) (int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Prompt{})

	if userId != "" {
		query = query.Where("user_id = ?", userId)
	}
	if promptType != "" {
		query = query.Where("type = ?", promptType)
	}
	if isPreset != nil {
		query = query.Where("is_preset = ?", *isPreset)
	}
	if isPublic != nil {
		query = query.Where("is_public = ?", *isPublic)
	}

	var count int64
	err := query.Count(&count).Error
	return count, err
}

func (r *botRepository) GetUserMemory(ctx context.Context, userID, botID string) ([]*model.UserMemory, error) {
	var memories []*model.UserMemory
	query := r.db.WithContext(ctx).Model(&model.UserMemory{}).Where("user_id = ?", userID)
	if botID != "" {
		query = query.Where("bot_id = ?", botID)
	}
	err := query.Find(&memories).Error
	return memories, err
}

func (r *botRepository) SetUserMemory(ctx context.Context, memory *model.UserMemory) error {
	var existing model.UserMemory
	err := r.db.WithContext(ctx).Where("user_id = ? AND bot_id = ? AND key = ?", memory.UserID, memory.BotID, memory.Key).First(&existing).Error
	if err == nil {
		existing.Value = memory.Value
		return r.db.WithContext(ctx).Save(&existing).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if memory.ID == "" {
		memory.ID = uuid.NewString()
	}
	return r.db.WithContext(ctx).Create(memory).Error
}

func (r *botRepository) DeleteUserMemory(ctx context.Context, userID, botID, key string) error {
	return r.db.WithContext(ctx).Where("user_id = ? AND bot_id = ? AND key = ?", userID, botID, key).Delete(&model.UserMemory{}).Error
}

func (r *botRepository) WithTransaction(ctx context.Context, fn func(txRepo BotRepository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txRepo := NewBotRepository(tx)
		return fn(txRepo)
	})
}
