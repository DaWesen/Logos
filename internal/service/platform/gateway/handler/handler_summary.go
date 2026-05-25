package handler

import (
	"context"
	"net/http"

	"Logos/internal/service/platform/gateway/model"
	pb "Logos/proto_gen/summary"
	chatpb "Logos/proto_gen/chat"

	"github.com/gin-gonic/gin"
)

type summaryRequest struct {
	ChatID            string              `json:"chat_id"`
	ChatType          int32               `json:"chat_type"`
	MessageIDs        []string            `json:"message_ids"`
	IncludeTodos      bool                `json:"include_todos"`
	IncludeCandidates bool                `json:"include_candidates"`
	MessageCount      int32               `json:"message_count"`
	ModelConfig       *summaryModelConfig `json:"model_config"`
}

type replyCandidatesRequest struct {
	ChatID            string              `json:"chat_id"`
	ChatType          int32               `json:"chat_type"`
	ContextMessageIDs []string            `json:"context_message_ids"`
	CandidateCount    int32               `json:"candidate_count"`
	Tone              string              `json:"tone"`
	ModelConfig       *summaryModelConfig `json:"model_config"`
}

type extractTodosRequest struct {
	ChatID       string              `json:"chat_id"`
	ChatType     int32               `json:"chat_type"`
	MessageIDs   []string            `json:"message_ids"`
	MessageCount int32               `json:"message_count"`
	ModelConfig  *summaryModelConfig `json:"model_config"`
}

type summaryModelConfig struct {
	Provider    string  `json:"provider"`
	Model       string  `json:"model"`
	ApiKey      string  `json:"apiKey"`
	BaseUrl     string  `json:"baseUrl"`
	Temperature float64 `json:"temperature"`
}

func toProtoModelConfig(cfg *summaryModelConfig) *pb.ModelConfig {
	if cfg == nil {
		return nil
	}
	return &pb.ModelConfig{
		Provider:    cfg.Provider,
		Model:       cfg.Model,
		ApiKey:      cfg.ApiKey,
		BaseUrl:     cfg.BaseUrl,
		Temperature: cfg.Temperature,
	}
}

func toChatType(t int32) chatpb.ChatType {
	return chatpb.ChatType(t)
}

func (h *Handler) SummarizeMessages(c *gin.Context) {
	if h.SummaryClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "摘要服务不可用"))
		return
	}
	var req summaryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	pbReq := &pb.SummarizeMessagesRequest{
		ChatId:            req.ChatID,
		ChatType:          toChatType(req.ChatType),
		MessageIds:        req.MessageIDs,
		IncludeTodos:      req.IncludeTodos,
		IncludeCandidates: req.IncludeCandidates,
		MessageCount:      req.MessageCount,
		ModelConfig:       toProtoModelConfig(req.ModelConfig),
	}

	resp, err := h.SummaryClient.SummarizeMessages(context.Background(), pbReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: gin.H{
		"summary":          resp.Summary,
		"todos":            resp.Todos,
		"reply_candidates": resp.ReplyCandidates,
		"key_points":       resp.KeyPoints,
		"participants":     resp.Participants,
	}})
}

func (h *Handler) GenerateReplyCandidates(c *gin.Context) {
	if h.SummaryClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "摘要服务不可用"))
		return
	}
	var req replyCandidatesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	pbReq := &pb.GenerateReplyCandidatesRequest{
		ChatId:            req.ChatID,
		ChatType:          toChatType(req.ChatType),
		ContextMessageIds: req.ContextMessageIDs,
		CandidateCount:    req.CandidateCount,
		Tone:              req.Tone,
		ModelConfig:       toProtoModelConfig(req.ModelConfig),
	}

	resp, err := h.SummaryClient.GenerateReplyCandidates(context.Background(), pbReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: resp.Candidates})
}

func (h *Handler) ExtractTodos(c *gin.Context) {
	if h.SummaryClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "摘要服务不可用"))
		return
	}
	var req extractTodosRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	pbReq := &pb.ExtractTodosRequest{
		ChatId:       req.ChatID,
		ChatType:     toChatType(req.ChatType),
		MessageIds:   req.MessageIDs,
		MessageCount: req.MessageCount,
		ModelConfig:  toProtoModelConfig(req.ModelConfig),
	}

	resp, err := h.SummaryClient.ExtractTodos(context.Background(), pbReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: resp.Todos})
}

func toHTTPCode(code int32) int {
	if code == 0 {
		return http.StatusOK
	}
	return int(code)
}
