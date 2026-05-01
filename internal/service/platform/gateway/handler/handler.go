package handler

import (
	"Logos/config"
	"Logos/internal/service/platform/gateway/websocket"

	"Logos/pkg/grpcserver"
	"Logos/pkg/obs"
	pbBilling "Logos/proto_gen/billing"
	pbBot "Logos/proto_gen/bot"
	pbChat "Logos/proto_gen/chat"
	pbCollection "Logos/proto_gen/collection"
	pbCommon "Logos/proto_gen/common"
	pbExtraction "Logos/proto_gen/extraction"
	pbIM "Logos/proto_gen/im"
	pbKnowledge "Logos/proto_gen/knowledge"
	pbMCP "Logos/proto_gen/mcp"
	pbMessage "Logos/proto_gen/message"
	pbModeration "Logos/proto_gen/moderation"
	pbMonitoring "Logos/proto_gen/monitoring"
	pbQuestion "Logos/proto_gen/question"
	pbRecommend "Logos/proto_gen/recommend"
	pbSearch "Logos/proto_gen/search"
	pbSummary "Logos/proto_gen/summary"
	pbUser "Logos/proto_gen/user"
	pbVector "Logos/proto_gen/vector"
)

type Handler struct {
	UserClient        pbUser.UserServiceClient
	KnowledgeClient   pbKnowledge.KnowledgeServiceClient
	SearchClient      pbSearch.SearchServiceClient
	VectorClient      pbVector.VectorServiceClient
	QuestionClient    pbQuestion.QAServiceClient
	RecommendClient   pbRecommend.RecommendationServiceClient
	ExtractionClient  pbExtraction.KnowledgeExtractionServiceClient
	CollectionClient  pbCollection.DataCollectionServiceClient
	MessageClient     pbMessage.MessageServiceClient
	MonitoringClient  pbMonitoring.MonitoringServiceClient
	BotClient         pbBot.BotServiceClient
	BillingClient     pbBilling.BillingServiceClient
	IMClient          pbIM.IMServiceClient
	ChatClient        pbChat.ChatServiceClient
	SummaryClient     pbSummary.SummaryServiceClient
	MCPClient         pbMCP.MCPServiceClient
	ModerationClient  pbModeration.ModerationServiceClient
	WebSocketHandler  *websocket.Handler
	ProcessServiceURL string
}

func InitUserClient(cfg *config.Config) (pbUser.UserServiceClient, error) {
	_, _, clientOpt := obs.InitGRPCProvider("gateway-user-client")
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.user", clientOpt)
	if err != nil {
		return nil, err
	}
	return pbUser.NewUserServiceClient(conn), nil
}

func InitKnowledgeClient(cfg *config.Config) (pbKnowledge.KnowledgeServiceClient, error) {
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.knowledge")
	if err != nil {
		return nil, err
	}
	return pbKnowledge.NewKnowledgeServiceClient(conn), nil
}

func InitSearchClient(cfg *config.Config) (pbSearch.SearchServiceClient, error) {
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.search")
	if err != nil {
		return nil, err
	}
	return pbSearch.NewSearchServiceClient(conn), nil
}

func InitVectorClient(cfg *config.Config) (pbVector.VectorServiceClient, error) {
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.vector")
	if err != nil {
		return nil, err
	}
	return pbVector.NewVectorServiceClient(conn), nil
}

func InitQuestionClient(cfg *config.Config) (pbQuestion.QAServiceClient, error) {
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.question")
	if err != nil {
		return nil, err
	}
	return pbQuestion.NewQAServiceClient(conn), nil
}

func InitRecommendClient(cfg *config.Config) (pbRecommend.RecommendationServiceClient, error) {
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.recommend")
	if err != nil {
		return nil, err
	}
	return pbRecommend.NewRecommendationServiceClient(conn), nil
}

func InitExtractionClient(cfg *config.Config) (pbExtraction.KnowledgeExtractionServiceClient, error) {
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.extraction")
	if err != nil {
		return nil, err
	}
	return pbExtraction.NewKnowledgeExtractionServiceClient(conn), nil
}

func InitCollectionClient(cfg *config.Config) (pbCollection.DataCollectionServiceClient, error) {
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.collection")
	if err != nil {
		return nil, err
	}
	return pbCollection.NewDataCollectionServiceClient(conn), nil
}

func InitMessageClient(cfg *config.Config) (pbMessage.MessageServiceClient, error) {
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.message")
	if err != nil {
		return nil, err
	}
	return pbMessage.NewMessageServiceClient(conn), nil
}

func InitMonitoringClient(cfg *config.Config) (pbMonitoring.MonitoringServiceClient, error) {
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.monitoring")
	if err != nil {
		return nil, err
	}
	return pbMonitoring.NewMonitoringServiceClient(conn), nil
}

func InitBotClient(cfg *config.Config) (pbBot.BotServiceClient, error) {
	_, _, clientOpt := obs.InitGRPCProvider("gateway-bot-client")
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.bot", clientOpt)
	if err != nil {
		return nil, err
	}
	return pbBot.NewBotServiceClient(conn), nil
}

func InitBillingClient(cfg *config.Config) (pbBilling.BillingServiceClient, error) {
	_, _, clientOpt := obs.InitGRPCProvider("gateway-billing-client")
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.billing", clientOpt)
	if err != nil {
		return nil, err
	}
	return pbBilling.NewBillingServiceClient(conn), nil
}

func InitIMClient(cfg *config.Config) (pbIM.IMServiceClient, error) {
	_, _, clientOpt := obs.InitGRPCProvider("gateway-im-client")
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.im", clientOpt)
	if err != nil {
		return nil, err
	}
	return pbIM.NewIMServiceClient(conn), nil
}

func InitChatClient(cfg *config.Config) (pbChat.ChatServiceClient, error) {
	_, _, clientOpt := obs.InitGRPCProvider("gateway-chat-client")
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.chat", clientOpt)
	if err != nil {
		return nil, err
	}
	return pbChat.NewChatServiceClient(conn), nil
}

func InitSummaryClient(cfg *config.Config) (pbSummary.SummaryServiceClient, error) {
	_, _, clientOpt := obs.InitGRPCProvider("gateway-summary-client")
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.summary", clientOpt)
	if err != nil {
		return nil, err
	}
	return pbSummary.NewSummaryServiceClient(conn), nil
}

func InitMCPClient(cfg *config.Config) (pbMCP.MCPServiceClient, error) {
	_, _, clientOpt := obs.InitGRPCProvider("gateway-mcp-client")
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.mcp", clientOpt)
	if err != nil {
		return nil, err
	}
	return pbMCP.NewMCPServiceClient(conn), nil
}

func InitModerationClient(cfg *config.Config) (pbModeration.ModerationServiceClient, error) {
	_, _, clientOpt := obs.InitGRPCProvider("gateway-moderation-client")
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.moderation", clientOpt)
	if err != nil {
		return nil, err
	}
	return pbModeration.NewModerationServiceClient(conn), nil
}

func getBaseRespMessage(resp *pbCommon.BaseResp) string {
	if resp == nil {
		return "success"
	}
	return resp.StatusMessage
}
