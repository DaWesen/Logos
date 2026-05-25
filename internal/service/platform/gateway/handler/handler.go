package handler

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"Logos/config"
	"Logos/internal/service/platform/gateway/websocket"
	"Logos/pkg/governance"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/storage"
	pbBilling "Logos/proto_gen/billing"
	pbBot "Logos/proto_gen/bot"
	pbChat "Logos/proto_gen/chat"
	pbCollection "Logos/proto_gen/collection"
	pbCommon "Logos/proto_gen/common"
	pbContact "Logos/proto_gen/contact"
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

	"google.golang.org/grpc"
)

func buildGovernanceConfig(cfg *config.Config) *governance.Config {
	clientTimeout, err := cfg.GetGRPCClientTimeout()
	if err != nil {
		clientTimeout = 30 * time.Second
	}

	govCfg := governance.DefaultConfig()
	govCfg.Timeout.ClientDefault = clientTimeout
	return govCfg
}

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
	ContactClient     pbContact.ContactServiceClient
	SummaryClient     pbSummary.SummaryServiceClient
	MCPClient         pbMCP.MCPServiceClient
	ModerationClient  pbModeration.ModerationServiceClient
	WebSocketHandler  *websocket.Handler
	ProcessServiceURL string
	MinioManager      *storage.MinioManager
	Cfg               *config.Config
}

type clientDef struct {
	host    string
	port    int
	svcName string
}

func directDial(host string, port int) (*grpc.ClientConn, error) {
	return grpcserver.NewDirectClientConn(host, port)
}

func etcdDial(endpoints []string, svcName string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	return grpcserver.NewGRPCClientConnWithGovernance(endpoints, svcName, buildGovernanceConfig(config.GetConfig()), opts...)
}

func tryDial(cfg *config.Config, host string, port int, svcName string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	logger.Info("Trying to dial service",
		logger.StringField("host", host),
		logger.IntField("port", port),
		logger.StringField("service", svcName),
	)
	conn, err := directDial(host, port)
	if err == nil {
		logger.Info("Direct dial successful",
			logger.StringField("service", svcName),
		)
		return conn, nil
	}
	logger.Warn("Direct dial failed, trying etcd",
		logger.StringField("service", svcName),
		logger.ErrorField(err),
	)
	return etcdDial(cfg.Etcd.Endpoints, svcName, opts...)
}

func InitUserClient(cfg *config.Config) (pbUser.UserServiceClient, error) {
	conn, err := tryDial(cfg, "localhost", cfg.Ports.User, "logos.user")
	if err != nil {
		return nil, err
	}
	return pbUser.NewUserServiceClient(conn), nil
}

func InitKnowledgeClient(cfg *config.Config) (pbKnowledge.KnowledgeServiceClient, error) {
	conn, err := tryDial(cfg, "localhost", cfg.Ports.Knowledge, "logos.knowledge")
	if err != nil {
		return nil, err
	}
	return pbKnowledge.NewKnowledgeServiceClient(conn), nil
}

func InitSearchClient(cfg *config.Config) (pbSearch.SearchServiceClient, error) {
	conn, err := tryDial(cfg, "localhost", cfg.Ports.Search, "logos.search")
	if err != nil {
		return nil, err
	}
	return pbSearch.NewSearchServiceClient(conn), nil
}

func InitVectorClient(cfg *config.Config) (pbVector.VectorServiceClient, error) {
	conn, err := tryDial(cfg, "localhost", cfg.Ports.Vector, "logos.vector")
	if err != nil {
		return nil, err
	}
	return pbVector.NewVectorServiceClient(conn), nil
}

func InitQuestionClient(cfg *config.Config) (pbQuestion.QAServiceClient, error) {
	conn, err := tryDial(cfg, "localhost", cfg.Ports.Question, "logos.question")
	if err != nil {
		return nil, err
	}
	return pbQuestion.NewQAServiceClient(conn), nil
}

func InitRecommendClient(cfg *config.Config) (pbRecommend.RecommendationServiceClient, error) {
	conn, err := tryDial(cfg, "localhost", cfg.Ports.Recommend, "logos.recommend")
	if err != nil {
		return nil, err
	}
	return pbRecommend.NewRecommendationServiceClient(conn), nil
}

func InitExtractionClient(cfg *config.Config) (pbExtraction.KnowledgeExtractionServiceClient, error) {
	conn, err := tryDial(cfg, "localhost", cfg.Ports.Extraction, "logos.extraction")
	if err != nil {
		return nil, err
	}
	return pbExtraction.NewKnowledgeExtractionServiceClient(conn), nil
}

func InitCollectionClient(cfg *config.Config) (pbCollection.DataCollectionServiceClient, error) {
	conn, err := tryDial(cfg, "localhost", cfg.Ports.Collection, "logos.collection")
	if err != nil {
		return nil, err
	}
	return pbCollection.NewDataCollectionServiceClient(conn), nil
}

func InitMessageClient(cfg *config.Config) (pbMessage.MessageServiceClient, error) {
	conn, err := tryDial(cfg, "localhost", cfg.Ports.Message, "logos.message")
	if err != nil {
		return nil, err
	}
	return pbMessage.NewMessageServiceClient(conn), nil
}

func InitMonitoringClient(cfg *config.Config) (pbMonitoring.MonitoringServiceClient, error) {
	conn, err := tryDial(cfg, "localhost", cfg.Ports.Monitoring, "logos.monitoring")
	if err != nil {
		return nil, err
	}
	return pbMonitoring.NewMonitoringServiceClient(conn), nil
}

func InitBotClient(cfg *config.Config) (pbBot.BotServiceClient, error) {
	conn, err := tryDial(cfg, "localhost", cfg.Ports.Bot, "logos.bot")
	if err != nil {
		return nil, err
	}
	return pbBot.NewBotServiceClient(conn), nil
}

func InitBillingClient(cfg *config.Config) (pbBilling.BillingServiceClient, error) {
	conn, err := tryDial(cfg, "localhost", cfg.Ports.Billing, "logos.billing")
	if err != nil {
		return nil, err
	}
	return pbBilling.NewBillingServiceClient(conn), nil
}

func InitIMClient(cfg *config.Config) (pbIM.IMServiceClient, error) {
	conn, err := tryDial(cfg, "localhost", cfg.Ports.IM, "logos.im")
	if err != nil {
		return nil, err
	}
	return pbIM.NewIMServiceClient(conn), nil
}

func InitChatClient(cfg *config.Config) (pbChat.ChatServiceClient, error) {
	conn, err := tryDial(cfg, "localhost", cfg.Ports.Chat, "logos.chat")
	if err != nil {
		return nil, err
	}
	return pbChat.NewChatServiceClient(conn), nil
}

func InitContactClient(cfg *config.Config) (pbContact.ContactServiceClient, error) {
	conn, err := tryDial(cfg, "localhost", cfg.Ports.Contact, "logos.contact")
	if err != nil {
		return nil, err
	}
	return pbContact.NewContactServiceClient(conn), nil
}

func InitSummaryClient(cfg *config.Config) (pbSummary.SummaryServiceClient, error) {
	conn, err := tryDial(cfg, "localhost", cfg.Ports.Summary, "logos.summary")
	if err != nil {
		return nil, err
	}
	return pbSummary.NewSummaryServiceClient(conn), nil
}

func InitMCPClient(cfg *config.Config) (pbMCP.MCPServiceClient, error) {
	conn, err := tryDial(cfg, "localhost", cfg.Ports.MCP, "logos.mcp")
	if err != nil {
		return nil, err
	}
	return pbMCP.NewMCPServiceClient(conn), nil
}

func InitModerationClient(cfg *config.Config) (pbModeration.ModerationServiceClient, error) {
	conn, err := tryDial(cfg, "localhost", cfg.Ports.Moderation, "logos.moderation")
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

func mapStatusCode(code int32) int {
	if code == 0 {
		return 200
	}
	// 如果是业务错误码（1），映射到 500
	if code == 1 {
		return 500
	}
	// 如果是有效的 HTTP 状态码，直接返回
	if code >= 100 && code < 600 {
		return int(code)
	}
	// 默认返回 500
	return 500
}

func mapBaseRespStatusCode(resp *pbCommon.BaseResp) int {
	if resp == nil {
		return 200
	}
	return mapStatusCode(resp.StatusCode)
}

func (h *Handler) ProxyProcessService(c *gin.Context) {
	target, err := url.Parse(h.ProcessServiceURL)
	if err != nil {
		logger.Error("Parse process service URL failed", logger.ErrorField(err))
		c.JSON(500, gin.H{"error": "服务配置错误"})
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host

		origPath := c.Request.URL.Path
		origPath = strings.TrimPrefix(origPath, "/api/v1/process")
		req.URL.Path = "/api/ai/process" + origPath

		req.Header.Set("X-Forwarded-Host", c.Request.Host)
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Error("Process service proxy failed", logger.ErrorField(err))
		c.JSON(502, gin.H{"error": "处理服务不可用"})
	}

	proxy.ServeHTTP(c.Writer, c.Request)
}
