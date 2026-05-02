package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"Logos/internal/service/ai/moderation/dao"
	"Logos/internal/service/ai/moderation/model"
	"Logos/pkg/eino"
	"Logos/pkg/logger"

	"github.com/google/uuid"
)

type ModerationService interface {
	Translate(ctx context.Context, content string, sourceLang string, targetLang string, contentID string) (*model.TranslationRecord, error)
	ModerateContent(ctx context.Context, content string, contentID string, contentType string) (*model.ModerationRecord, error)
	GetModerationRecords(ctx context.Context, result string, startTime, endTime *time.Time, page, pageSize int) ([]*model.ModerationRecord, int64, error)
}

type moderationServiceImpl struct {
	repo       dao.ModerationRepository
	einoClient *eino.EinoManager
}

func NewModerationService(repo dao.ModerationRepository, einoClient *eino.EinoManager) ModerationService {
	return &moderationServiceImpl{repo: repo, einoClient: einoClient}
}

func (s *moderationServiceImpl) Translate(ctx context.Context, content string, sourceLang string, targetLang string, contentID string) (*model.TranslationRecord, error) {
	logger.Info("翻译内容", logger.StringField("source", sourceLang), logger.StringField("target", targetLang))

	if content == "" {
		return nil, fmt.Errorf("内容不能为空")
	}

	translated, err := s.doTranslate(ctx, content, sourceLang, targetLang)
	if err != nil {
		return nil, fmt.Errorf("翻译失败: %w", err)
	}

	record := &model.TranslationRecord{
		ID:                uuid.New().String(),
		Content:           content,
		TranslatedContent: translated,
		SourceLang:        sourceLang,
		TargetLang:        targetLang,
		ContentID:         contentID,
	}

	if err := s.repo.CreateTranslationRecord(ctx, record); err != nil {
		logger.Error("保存翻译记录失败", logger.ErrorField(err))
	}

	return record, nil
}

func (s *moderationServiceImpl) ModerateContent(ctx context.Context, content string, contentID string, contentType string) (*model.ModerationRecord, error) {
	logger.Info("审核内容", logger.StringField("content_id", contentID))

	if content == "" {
		return nil, fmt.Errorf("内容不能为空")
	}

	result, categories, scores, action, err := s.doModerate(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("审核失败: %w", err)
	}

	categoriesJSON, _ := json.Marshal(categories)
	scoresJSON, _ := json.Marshal(scores)

	record := &model.ModerationRecord{
		ID:          uuid.New().String(),
		Content:     content,
		ContentID:   contentID,
		ContentType: contentType,
		Result:      result,
		Categories:  string(categoriesJSON),
		Scores:      string(scoresJSON),
		ActionTaken: action,
	}

	if err := s.repo.CreateModerationRecord(ctx, record); err != nil {
		logger.Error("保存审核记录失败", logger.ErrorField(err))
	}

	return record, nil
}

func (s *moderationServiceImpl) GetModerationRecords(ctx context.Context, result string, startTime, endTime *time.Time, page, pageSize int) ([]*model.ModerationRecord, int64, error) {
	return s.repo.ListModerationRecords(ctx, result, startTime, endTime, page, pageSize)
}

func (s *moderationServiceImpl) doTranslate(ctx context.Context, content string, sourceLang string, targetLang string) (string, error) {
	if s.einoClient == nil || !s.einoClient.HasChatModel() {
		return "[翻译服务暂不可用]", nil
	}

	prompt := fmt.Sprintf(`请将以下%s文本翻译为%s，只输出翻译结果，不要任何解释或前缀。

文本：
%s`, sourceLang, targetLang, content)

	response, err := s.einoClient.Chat(ctx, []string{
		"你是一个专业的翻译系统。",
		prompt,
	})
	if err != nil {
		return "", fmt.Errorf("LLM翻译失败: %w", err)
	}

	return response, nil
}

func (s *moderationServiceImpl) doModerate(ctx context.Context, content string) (string, []string, map[string]float64, string, error) {
	if s.einoClient == nil || !s.einoClient.HasChatModel() {
		return "passed", []string{}, map[string]float64{}, "none", nil
	}

	prompt := fmt.Sprintf(`请审核以下内容是否包含不当信息。请以JSON格式输出，包含：
- "result": 审核结果（passed/flagged/rejected）
- "categories": 涉及的分类列表（如hate/harassment/sexual/violence/self_harm/spam/illegal）
- "scores": 各分类的置信度（0.0-1.0）
- "action": 建议的操作（none/warn/block）

只输出JSON，不要其他内容。

内容：
%s`, content)

	response, err := s.einoClient.Chat(ctx, []string{
		"你是一个专业的内容审核系统。",
		prompt,
	})
	if err != nil {
		return "", nil, nil, "", fmt.Errorf("LLM审核失败: %w", err)
	}

	type moderationOutput struct {
		Result     string            `json:"result"`
		Categories []string          `json:"categories"`
		Scores     map[string]float64 `json:"scores"`
		Action     string            `json:"action"`
	}

	var output moderationOutput
	if err := json.Unmarshal([]byte(trimJSON(response)), &output); err != nil {
		return "passed", []string{}, map[string]float64{}, "none", nil
	}

	if output.Result == "" {
		output.Result = "passed"
	}
	if output.Action == "" {
		output.Action = "none"
	}

	return output.Result, output.Categories, output.Scores, output.Action, nil
}

func trimJSON(s string) string {
	start := -1
	for i := 0; i < len(s); i++ {
		if s[i] == '{' {
			start = i
			break
		}
	}
	if start < 0 {
		return s
	}
	end := -1
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '}' {
			end = i
			break
		}
	}
	if end < 0 {
		return s[start:]
	}
	return s[start : end+1]
}
