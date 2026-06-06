package handler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"Logos/internal/service/ai/bot/model"
	"Logos/internal/service/ai/bot/service"
	"Logos/pkg/logger"
	"Logos/pkg/strutil"
	pb "Logos/proto_gen/bot"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type BotServiceImpl struct {
	pb.UnimplementedBotServiceServer
	BotService service.BotService
}

func convertModelBotToProtoBot(mb *model.Bot) *pb.Bot {
	if mb == nil {
		return nil
	}

	var botType pb.BotType
	switch mb.Type {
	case "builtin":
		botType = pb.BotType_BOT_TYPE_BUILTIN
	case "custom":
		botType = pb.BotType_BOT_TYPE_CUSTOM
	default:
		botType = pb.BotType_BOT_TYPE_UNSPECIFIED
	}

	var provider pb.ModelProvider
	switch mb.Provider {
	case "openai":
		provider = pb.ModelProvider_MODEL_PROVIDER_OPENAI
	case "claude":
		provider = pb.ModelProvider_MODEL_PROVIDER_CLAUDE
	case "qianfan":
		provider = pb.ModelProvider_MODEL_PROVIDER_QIANFAN
	case "platform":
		provider = pb.ModelProvider_MODEL_PROVIDER_PLATFORM
	default:
		provider = pb.ModelProvider_MODEL_PROVIDER_UNSPECIFIED
	}

	var status pb.BotStatus
	switch mb.Status {
	case "active":
		status = pb.BotStatus_BOT_STATUS_ACTIVE
	case "inactive":
		status = pb.BotStatus_BOT_STATUS_INACTIVE
	case "deleted":
		status = pb.BotStatus_BOT_STATUS_DELETED
	default:
		status = pb.BotStatus_BOT_STATUS_UNSPECIFIED
	}

	return &pb.Bot{
		Id:             mb.ID,
		UserId:         mb.UserID,
		Name:           mb.Name,
		Description:    mb.Description,
		Avatar:         mb.Avatar,
		Type:           botType,
		Provider:       provider,
		Model:          mb.Model,
		ApiKey:         mb.APIKey,
		BaseUrl:        mb.BaseURL,
		EmbeddingModel: mb.EmbeddingModel,
		SystemPrompt:   mb.SystemPrompt,
		Status:         status,
		Config:         map[string]string(mb.Config),
		CreatedAt:      timestamppb.New(mb.CreatedAt),
		UpdatedAt:      timestamppb.New(mb.UpdatedAt),
	}
}

func convertModelMessageToProtoMessage(mm *model.Message) *pb.BotMessage {
	if mm == nil {
		return nil
	}
	return &pb.BotMessage{
		Id:        mm.ID,
		BotId:     mm.BotID,
		ChatId:    mm.ConversationID,
		Role:      mm.Role,
		Content:   mm.Content,
		Metadata:  map[string]string(mm.Metadata),
		CreatedAt: timestamppb.New(mm.CreatedAt),
	}
}

func convertModelPromptToProtoPrompt(mp *model.Prompt) *pb.Prompt {
	if mp == nil {
		return nil
	}
	return &pb.Prompt{
		Id:          mp.ID,
		UserId:      mp.UserID,
		Name:        mp.Name,
		Description: mp.Description,
		Content:     mp.Content,
		Type:        mp.Type,
		IsPreset:    mp.IsPreset,
		IsPublic:    mp.IsPublic,
		Config:      map[string]string(mp.Config),
		CreatedAt:   timestamppb.New(mp.CreatedAt),
		UpdatedAt:   timestamppb.New(mp.UpdatedAt),
	}
}

func (s *BotServiceImpl) CreateBot(ctx context.Context, req *pb.CreateBotRequest) (*pb.CreateBotResponse, error) {
	resp := &pb.CreateBotResponse{}

	var botType string
	switch req.Type {
	case pb.BotType_BOT_TYPE_BUILTIN:
		botType = "builtin"
	case pb.BotType_BOT_TYPE_CUSTOM:
		botType = "custom"
	}

	var provider string
	switch req.Provider {
	case pb.ModelProvider_MODEL_PROVIDER_OPENAI:
		provider = "openai"
	case pb.ModelProvider_MODEL_PROVIDER_CLAUDE:
		provider = "claude"
	case pb.ModelProvider_MODEL_PROVIDER_QIANFAN:
		provider = "qianfan"
	case pb.ModelProvider_MODEL_PROVIDER_PLATFORM:
		provider = "platform"
	default:
		if req.Provider != pb.ModelProvider_MODEL_PROVIDER_UNSPECIFIED {
			provider = req.Provider.String()
		}
	}

	if provider == "" {
		provider = "platform"
	}

	if provider == "openai" && req.Model != "" {
		if strings.HasPrefix(req.Model, "deepseek") {
			provider = "deepseek"
		} else if strings.HasPrefix(req.Model, "claude") {
			provider = "claude"
		} else if strings.HasPrefix(req.Model, "ernie") || strings.HasPrefix(req.Model, "wenxin") {
			provider = "qianfan"
		}
	}

	bot, err := s.BotService.CreateBot(ctx, req.UserId, req.Name, req.Description, req.Avatar, botType, provider, req.Model, req.ApiKey, req.BaseUrl, req.EmbeddingModel, req.SystemPrompt, req.Config)
	if err != nil {
		logger.Error("创建 Bot 失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Data = convertModelBotToProtoBot(bot)
	return resp, nil
}

func convertModelConversationToProtoConversation(mc *model.Conversation) *pb.Conversation {
	if mc == nil {
		return nil
	}
	return &pb.Conversation{
		Id:        mc.ID,
		BotId:     mc.BotID,
		UserId:    mc.UserID,
		Title:     mc.Title,
		Status:    mc.Status,
		CreatedAt: timestamppb.New(mc.CreatedAt),
		UpdatedAt: timestamppb.New(mc.UpdatedAt),
	}
}

func convertModelUserMemoryToProtoUserMemory(um *model.UserMemory) *pb.UserMemory {
	if um == nil {
		return nil
	}
	return &pb.UserMemory{
		Id:         um.ID,
		UserId:     um.UserID,
		BotId:      um.BotID,
		Key:        um.Key,
		Value:      um.Value,
		Category:   um.Category,
		Source:     um.Source,
		Confidence: um.Confidence,
		CreatedAt:  timestamppb.New(um.CreatedAt),
		UpdatedAt:  timestamppb.New(um.UpdatedAt),
	}
}

func (s *BotServiceImpl) CreateConversation(ctx context.Context, req *pb.CreateConversationRequest) (*pb.CreateConversationResponse, error) {
	resp := &pb.CreateConversationResponse{}

	conversation, err := s.BotService.CreateConversation(ctx, req.BotId, req.UserId, req.Title)
	if err != nil {
		logger.Error("创建对话失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Data = convertModelConversationToProtoConversation(conversation)
	return resp, nil
}

func (s *BotServiceImpl) UpdateConversation(ctx context.Context, req *pb.UpdateConversationRequest) (*pb.UpdateConversationResponse, error) {
	resp := &pb.UpdateConversationResponse{}

	conversation, err := s.BotService.UpdateConversation(ctx, req.ConversationId, req.Title)
	if err != nil {
		logger.Error("更新对话失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Data = convertModelConversationToProtoConversation(conversation)
	return resp, nil
}

func (s *BotServiceImpl) DeleteConversation(ctx context.Context, req *pb.DeleteConversationRequest) (*pb.DeleteConversationResponse, error) {
	resp := &pb.DeleteConversationResponse{}

	if err := s.BotService.DeleteConversation(ctx, req.ConversationId); err != nil {
		logger.Error("删除对话失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	return resp, nil
}

func (s *BotServiceImpl) ListConversations(ctx context.Context, req *pb.ListConversationsRequest) (*pb.ListConversationsResponse, error) {
	resp := &pb.ListConversationsResponse{}

	conversations, total, err := s.BotService.ListConversations(ctx, req.BotId, req.UserId, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error("获取对话列表失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Total = int32(total)
	for _, conv := range conversations {
		resp.Conversations = append(resp.Conversations, convertModelConversationToProtoConversation(conv))
	}
	return resp, nil
}

func (s *BotServiceImpl) StreamBotMessage(req *pb.SendBotMessageRequest, stream pb.BotService_StreamBotMessageServer) error {
	ctx := stream.Context()
	userID := req.UserId

	chatID, err := s.BotService.SendMessageStream(ctx, userID, req.BotId, req.ChatId, req.Content, req.Metadata, func(chunk string) error {
		return stream.Send(&pb.StreamBotResponse{
			Content: strutil.CleanInvalidUTF8(chunk),
			Done:    false,
		})
	})
	if err != nil {
		logger.Error("流式发送消息失败", logger.ErrorField(err))
		return stream.Send(&pb.StreamBotResponse{
			Content: err.Error(),
			Done:    true,
		})
	}

	return stream.Send(&pb.StreamBotResponse{
		Content: "",
		Done:    true,
		ChatId:  chatID,
	})
}

func (s *BotServiceImpl) GetUserMemory(ctx context.Context, req *pb.GetUserMemoryRequest) (*pb.GetUserMemoryResponse, error) {
	resp := &pb.GetUserMemoryResponse{}

	memories, err := s.BotService.GetUserMemory(ctx, req.UserId, req.BotId)
	if err != nil {
		logger.Error("获取用户记忆失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	for _, m := range memories {
		resp.Memories = append(resp.Memories, convertModelUserMemoryToProtoUserMemory(m))
	}
	return resp, nil
}

func (s *BotServiceImpl) SetUserMemory(ctx context.Context, req *pb.SetUserMemoryRequest) (*pb.SetUserMemoryResponse, error) {
	resp := &pb.SetUserMemoryResponse{}

	category := req.Category
	if category == "" {
		category = "fact"
	}
	if err := s.BotService.SetUserMemoryWithCategory(ctx, req.UserId, req.BotId, req.Key, req.Value, category); err != nil {
		logger.Error("设置用户记忆失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	return resp, nil
}

func (s *BotServiceImpl) DeleteUserMemory(ctx context.Context, req *pb.DeleteUserMemoryRequest) (*pb.DeleteUserMemoryResponse, error) {
	resp := &pb.DeleteUserMemoryResponse{}

	logger.Info("Bot服务收到删除记忆请求",
		logger.StringField("user_id", req.UserId),
		logger.StringField("bot_id", req.BotId),
		logger.StringField("key", req.Key),
	)

	if err := s.BotService.DeleteUserMemory(ctx, req.UserId, req.BotId, req.Key); err != nil {
		logger.Error("删除用户记忆失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	logger.Info("删除用户记忆成功",
		logger.StringField("user_id", req.UserId),
		logger.StringField("bot_id", req.BotId),
		logger.StringField("key", req.Key),
	)

	resp.Code = 0
	resp.Message = "success"
	return resp, nil
}

func (s *BotServiceImpl) UpdateBot(ctx context.Context, req *pb.UpdateBotRequest) (*pb.UpdateBotResponse, error) {
	resp := &pb.UpdateBotResponse{}

	var status string
	switch req.Status {
	case pb.BotStatus_BOT_STATUS_ACTIVE:
		status = "active"
	case pb.BotStatus_BOT_STATUS_INACTIVE:
		status = "inactive"
	case pb.BotStatus_BOT_STATUS_DELETED:
		status = "deleted"
	}

	bot, err := s.BotService.UpdateBot(ctx, req.UserId, req.BotId, req.Name, req.Description, req.Avatar, req.Model, req.ApiKey, req.BaseUrl, req.EmbeddingModel, req.SystemPrompt, req.Config, status)
	if err != nil {
		logger.Error("更新 Bot 失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Data = convertModelBotToProtoBot(bot)
	return resp, nil
}

func (s *BotServiceImpl) DeleteBot(ctx context.Context, req *pb.DeleteBotRequest) (*pb.DeleteBotResponse, error) {
	resp := &pb.DeleteBotResponse{}

	if err := s.BotService.DeleteBot(ctx, req.BotId); err != nil {
		logger.Error("删除 Bot 失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	return resp, nil
}

func (s *BotServiceImpl) GetBot(ctx context.Context, req *pb.GetBotRequest) (*pb.GetBotResponse, error) {
	resp := &pb.GetBotResponse{}

	bot, err := s.BotService.GetBot(ctx, req.BotId)
	if err != nil {
		logger.Error("获取 Bot 失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}
	if bot == nil {
		resp.Code = 1
		resp.Message = "Bot不存在"
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Data = convertModelBotToProtoBot(bot)
	return resp, nil
}

func (s *BotServiceImpl) ListBots(ctx context.Context, req *pb.ListBotsRequest) (*pb.ListBotsResponse, error) {
	resp := &pb.ListBotsResponse{}

	var botType string
	switch req.Type {
	case pb.BotType_BOT_TYPE_BUILTIN:
		botType = "builtin"
	case pb.BotType_BOT_TYPE_CUSTOM:
		botType = "custom"
	}

	var status string
	switch req.Status {
	case pb.BotStatus_BOT_STATUS_ACTIVE:
		status = "active"
	case pb.BotStatus_BOT_STATUS_INACTIVE:
		status = "inactive"
	case pb.BotStatus_BOT_STATUS_DELETED:
		status = "deleted"
	}

	bots, total, err := s.BotService.ListBots(ctx, req.UserId, botType, status, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error("获取 Bot 列表失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Total = int32(total)
	for _, bot := range bots {
		resp.Bots = append(resp.Bots, convertModelBotToProtoBot(bot))
	}
	return resp, nil
}

func (s *BotServiceImpl) SendBotMessage(ctx context.Context, req *pb.SendBotMessageRequest) (*pb.StreamBotResponse, error) {
	userID := req.UserId

	if req.Stream {
		content, chatID, cost, tokens, err := s.BotService.SendMessage(ctx, userID, req.BotId, req.ChatId, req.Content, true, req.Metadata)
		if err != nil {
			logger.Error("发送消息失败", logger.ErrorField(err))
			return &pb.StreamBotResponse{
				Content: err.Error(),
				Done:    true,
			}, nil
		}
		grpc.SetHeader(ctx, metadata.Pairs("x-cost", fmt.Sprintf("%.10f", cost), "x-tokens", fmt.Sprintf("%d", tokens)))
		return &pb.StreamBotResponse{
			Content: content,
			Done:    true,
			ChatId:  chatID,
		}, nil
	}

	content, chatID, cost, tokens, err := s.BotService.SendMessage(ctx, userID, req.BotId, req.ChatId, req.Content, false, req.Metadata)
	if err != nil {
		logger.Error("发送消息失败", logger.ErrorField(err))
		return &pb.StreamBotResponse{
			Content: err.Error(),
			Done:    true,
		}, nil
	}

	grpc.SetHeader(ctx, metadata.Pairs("x-cost", fmt.Sprintf("%.10f", cost), "x-tokens", fmt.Sprintf("%d", tokens)))
	return &pb.StreamBotResponse{
		Content: content,
		Done:    true,
		ChatId:  chatID,
	}, nil
}

func (s *BotServiceImpl) GetBotHistory(ctx context.Context, req *pb.GetBotHistoryRequest) (*pb.GetBotHistoryResponse, error) {
	resp := &pb.GetBotHistoryResponse{}

	var beforeTime *time.Time
	if req.BeforeTime != nil {
		t := req.BeforeTime.AsTime()
		beforeTime = &t
	}

	messages, hasMore, err := s.BotService.GetHistory(ctx, req.BotId, req.ChatId, int(req.Limit), beforeTime)
	if err != nil {
		logger.Error("获取对话历史失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.HasMore = hasMore
	for _, msg := range messages {
		resp.Messages = append(resp.Messages, convertModelMessageToProtoMessage(msg))
	}
	return resp, nil
}

func (s *BotServiceImpl) CreatePrompt(ctx context.Context, req *pb.CreatePromptRequest) (*pb.CreatePromptResponse, error) {
	resp := &pb.CreatePromptResponse{}

	prompt, err := s.BotService.CreatePrompt(ctx, req.UserId, req.Name, req.Description, req.Content, req.Type, req.IsPreset, req.IsPublic, req.Config)
	if err != nil {
		logger.Error("创建提示词失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Data = convertModelPromptToProtoPrompt(prompt)
	return resp, nil
}

func (s *BotServiceImpl) UpdatePrompt(ctx context.Context, req *pb.UpdatePromptRequest) (*pb.UpdatePromptResponse, error) {
	resp := &pb.UpdatePromptResponse{}

	var isPublic *bool
	if req.IsPublic != nil {
		val := req.GetIsPublic()
		isPublic = &val
	}

	prompt, err := s.BotService.UpdatePrompt(ctx, req.PromptId, req.Name, req.Description, req.Content, isPublic, req.Config)
	if err != nil {
		logger.Error("更新提示词失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Data = convertModelPromptToProtoPrompt(prompt)
	return resp, nil
}

func (s *BotServiceImpl) DeletePrompt(ctx context.Context, req *pb.DeletePromptRequest) (*pb.DeletePromptResponse, error) {
	resp := &pb.DeletePromptResponse{}

	if err := s.BotService.DeletePrompt(ctx, req.PromptId); err != nil {
		logger.Error("删除提示词失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	return resp, nil
}

func (s *BotServiceImpl) GetPrompt(ctx context.Context, req *pb.GetPromptRequest) (*pb.GetPromptResponse, error) {
	resp := &pb.GetPromptResponse{}

	prompt, err := s.BotService.GetPrompt(ctx, req.PromptId)
	if err != nil {
		logger.Error("获取提示词失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}
	if prompt == nil {
		resp.Code = 1
		resp.Message = "提示词不存在"
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Data = convertModelPromptToProtoPrompt(prompt)
	return resp, nil
}

func (s *BotServiceImpl) ListPrompts(ctx context.Context, req *pb.ListPromptsRequest) (*pb.ListPromptsResponse, error) {
	resp := &pb.ListPromptsResponse{}

	prompts, total, err := s.BotService.ListPrompts(ctx, req.UserId, req.Type, req.IsPreset, req.IsPublic, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error("获取提示词列表失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Total = int32(total)
	for _, prompt := range prompts {
		resp.Prompts = append(resp.Prompts, convertModelPromptToProtoPrompt(prompt))
	}
	return resp, nil
}
