package eino

import (
	"context"
	"fmt"
	"sync"

	openaiembed "github.com/cloudwego/eino-ext/components/embedding/openai"
	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"Logos/config"
	"Logos/pkg/logger"
)

type EinoManager struct {
	chatModel   model.BaseChatModel
	embedder    embedding.Embedder
	initialized bool
}

var (
	einoInstance *EinoManager
	einoOnce     sync.Once
)

func InitEino() (*EinoManager, error) {
	var err error
	einoOnce.Do(func() {
		cfg := config.GetConfig()

		einoInstance, err = NewEinoManager(
			cfg.Eino.APIKey,
			cfg.Eino.Model,
			cfg.Eino.BaseURL,
			cfg.Eino.EmbeddingModel,
		)
	})
	return einoInstance, err
}

func NewEinoManager(apiKey, modelName, baseURL, embeddingModel string) (*EinoManager, error) {
	logger.Info("初始化Eino管理器",
		logger.StringField("model", modelName),
		logger.StringField("base_url", baseURL),
		logger.StringField("embedding_model", embeddingModel))

	manager := &EinoManager{}

	ctx := context.Background()

	if apiKey != "" && modelName != "" {
		logger.Info("初始化OpenAI兼容ChatModel",
			logger.StringField("model", modelName),
			logger.StringField("base_url", baseURL))

		chatModel, err := openaimodel.NewChatModel(ctx, &openaimodel.ChatModelConfig{
			APIKey:  apiKey,
			Model:   modelName,
			BaseURL: baseURL,
		})
		if err != nil {
			logger.Warn("初始化ChatModel失败，将降级使用", logger.ErrorField(err))
		} else {
			manager.chatModel = chatModel
			manager.initialized = true
			logger.Info("ChatModel初始化成功")
		}
	}

	if apiKey != "" && embeddingModel != "" {
		logger.Info("初始化OpenAI兼容Embedding",
			logger.StringField("model", embeddingModel),
			logger.StringField("base_url", baseURL))

		embedder, err := openaiembed.NewEmbedder(ctx, &openaiembed.EmbeddingConfig{
			APIKey:  apiKey,
			Model:   embeddingModel,
			BaseURL: baseURL,
		})
		if err != nil {
			logger.Warn("初始化Embedding失败，将降级使用", logger.ErrorField(err))
		} else {
			manager.embedder = embedder
			manager.initialized = true
			logger.Info("Embedding初始化成功")
		}
	}

	logger.Info("导入 cloudwego/eino 框架成功",
		logger.StringField("eino_version", "v0.8.8"),
		logger.BoolField("initialized", manager.initialized))

	return manager, nil
}

func (e *EinoManager) Chat(ctx context.Context, messages []string) (string, error) {
	logger.Info("Eino聊天请求",
		logger.IntField("message_count", len(messages)))

	if e.chatModel == nil {
		logger.Warn("ChatModel未初始化")
		return "", fmt.Errorf("ChatModel未初始化，请检查配置")
	}

	var schemaMessages []*schema.Message
	for i, msg := range messages {
		if i == 0 {
			schemaMessages = append(schemaMessages, schema.SystemMessage(msg))
		} else if i%2 == 1 {
			schemaMessages = append(schemaMessages, schema.UserMessage(msg))
		} else {
			schemaMessages = append(schemaMessages, schema.AssistantMessage(msg, nil))
		}
	}

	response, err := e.chatModel.Generate(ctx, schemaMessages)
	if err != nil {
		logger.Error("ChatModel生成失败", logger.ErrorField(err))
		return "", fmt.Errorf("生成失败: %w", err)
	}

	logger.Info("ChatModel生成成功",
		logger.StringField("content", response.Content))

	return response.Content, nil
}

func (e *EinoManager) EmbedText(ctx context.Context, text string) ([]float64, error) {
	logger.Info("Eino文本向量化请求",
		logger.StringField("text_length", fmt.Sprintf("%d", len(text))))

	if e.embedder == nil {
		logger.Warn("Embedder未初始化")
		return []float64{}, fmt.Errorf("Embedder未初始化，请检查配置")
	}

	vectors, err := e.embedder.EmbedStrings(ctx, []string{text})
	if err != nil {
		logger.Error("Embedder向量化失败", logger.ErrorField(err))
		return []float64{}, fmt.Errorf("向量化失败: %w", err)
	}

	if len(vectors) > 0 {
		logger.Info("Embedder向量化成功",
			logger.IntField("dimension", len(vectors[0])))
		return vectors[0], nil
	}

	return []float64{}, fmt.Errorf("未获得向量结果")
}

func (e *EinoManager) BatchEmbedText(ctx context.Context, texts []string) ([][]float64, error) {
	logger.Info("Eino批量文本向量化请求",
		logger.IntField("count", len(texts)))

	if e.embedder == nil {
		logger.Warn("Embedder未初始化")
		embeddings := make([][]float64, 0, len(texts))
		for range texts {
			embeddings = append(embeddings, []float64{})
		}
		return embeddings, fmt.Errorf("Embedder未初始化")
	}

	vectors, err := e.embedder.EmbedStrings(ctx, texts)
	if err != nil {
		logger.Error("Embedder批量向量化失败", logger.ErrorField(err))
		embeddings := make([][]float64, 0, len(texts))
		for range texts {
			embeddings = append(embeddings, []float64{})
		}
		return embeddings, fmt.Errorf("批量向量化失败: %w", err)
	}

	logger.Info("Embedder批量向量化成功",
		logger.IntField("count", len(vectors)))

	return vectors, nil
}

func (e *EinoManager) GetComponents() []string {
	return []string{
		"ChatModel",
		"Embedding",
		"Tool",
		"Retriever",
		"ChatTemplate",
	}
}

func (e *EinoManager) ValidateMessage(msg string) error {
	if msg == "" {
		return fmt.Errorf("消息不能为空")
	}
	return nil
}

func (e *EinoManager) IsInitialized() bool {
	return e.initialized
}

func (e *EinoManager) HasChatModel() bool {
	return e.chatModel != nil
}

func (e *EinoManager) HasEmbedder() bool {
	return e.embedder != nil
}
