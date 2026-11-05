package handler

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"Logos/config"
	"Logos/internal/service/platform/gateway/model"
	"Logos/internal/service/platform/gateway/websocket"
	"Logos/pkg/auth"
	"Logos/pkg/governance"
	"Logos/pkg/grpcserver"
	pkgJwt "Logos/pkg/jwt"
	"Logos/pkg/logger"
	"Logos/pkg/pagination"
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
	// JWTManager / Revoker 供登出使用：解析当前令牌拿到 jti，再写入撤销黑名单
	JWTManager *pkgJwt.JWTManager
	Revoker    *auth.Revoker
}

func directDial(host string, port int) (*grpc.ClientConn, error) {
	return grpcserver.NewDirectClientConn(host, port)
}

// requireUserID 强制要求经过认证的 user_id，缺失时直接返回 401。
// 涉及资金/个人数据的接口必须以它为准，禁止沿用请求体里的 user_id，避免冒充他人操作。
func (h *Handler) requireUserID(c *gin.Context) (string, bool) {
	if value, exists := c.Get("user_id"); exists {
		if uid, ok := value.(string); ok && uid != "" {
			return uid, true
		}
	}
	c.JSON(http.StatusUnauthorized, model.Error(401, "unauthenticated"))
	return "", false
}

// requireNumericUserID 在 requireUserID 之上把字符串身份转成数据库主键。
// 认证中间件写入的 user_id 来自 JWT claims，正常情况下一定是数字；
// 转换失败说明令牌内容与用户表主键约定不一致，属于身份不可信，必须 401，
// 不能像旧代码那样 `userID, _ := strconv.ParseInt(...)` 静默变成 0。
func (h *Handler) requireNumericUserID(c *gin.Context) (int64, bool) {
	raw, ok := h.requireUserID(c)
	if !ok {
		return 0, false
	}
	userID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || userID <= 0 {
		c.JSON(http.StatusUnauthorized, model.Error(401, "用户身份无效"))
		return 0, false
	}
	return userID, true
}

// resolveTargetUserID 决定「这次请求查谁的数据」。
//
// 身份只能来自认证中间件。查询参数 user_id 历史上被当成身份使用，任何登录
// 用户改个数字就能读他人的问答历史、推荐记录和统计（水平越权 / IDOR）。
// 这里保留该参数只是为了兼容老客户端，但只允许它等于当前登录用户：
// 不相等直接 403，而不是悄悄按自己的身份查询，否则调用方会以为读到了别人。
func (h *Handler) resolveTargetUserID(c *gin.Context) (int64, bool) {
	authUserID, ok := h.requireNumericUserID(c)
	if !ok {
		return 0, false
	}
	raw := c.Query("user_id")
	if raw == "" {
		return authUserID, true
	}
	requested, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || requested <= 0 {
		c.JSON(http.StatusBadRequest, model.BadRequest("user_id 必须是正整数"))
		return 0, false
	}
	if requested != authUserID {
		c.JSON(http.StatusForbidden, model.Error(403, "无权访问其他用户的数据"))
		return 0, false
	}
	return authUserID, true
}

// withUserID 从 gin.Context 提取 user_id 并附加到 gRPC metadata，用于下游服务识别用户。
// 基座必须用请求上下文：认证中间件已把身份写进 c.Request.Context()，
// 这样客户端断开/超时会随链路取消下游调用，trace 也不会断链。
func (h *Handler) withUserID(c *gin.Context) context.Context {
	ctx := c.Request.Context()
	if userID, exists := c.Get("user_id"); exists {
		if uid, ok := userID.(string); ok {
			ctx = auth.AttachUserToContext(ctx, uid, "")
		}
	}
	return ctx
}

func etcdDial(endpoints []string, svcName string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	return grpcserver.NewGRPCClientConnWithGovernance(endpoints, svcName, buildGovernanceConfig(config.GetConfig()), opts...)
}

func resolveHost(svcName, defaultHost string) string {
	envKey := strings.ToUpper(strings.ReplaceAll(svcName, ".", "_")) + "_HOST"
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return defaultHost
}

func tryDial(cfg *config.Config, host string, port int, svcName string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	host = resolveHost(svcName, host)
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

// respondProtoCode 是所有「下游返回 int32 code」的 handler 的唯一出口：
// 业务码先经 mapStatusCode 变成合法 HTTP 状态码，响应体的 code 再复用同一个值。
// 这样响应体不会出现 0（成功）与 HTTP 200 不一致，也不会把 7 之类的业务码写进状态行。
func respondProtoCode(c *gin.Context, code int32, message string, data interface{}) {
	statusCode := mapStatusCode(code)
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: message,
		Data:    data,
	})
}

// 分页是外部输入，不能假设调用方一定传 page>=1、pageSize 在合理区间：
// pageSize=0 可能让数据库省略 LIMIT，负 page 会生成负 OFFSET。
//
// 之前的写法是 `page, _ := strconv.Atoi(...)`：`?page=abc` 静默变成 0，
// `?pageSize=1000000000` 原样透传给数据库/检索后端，一次请求就能把
// 全表拉进内存。这里对非法值直接返回 400（而不是猜一个默认值），
// 合法值再交给 pkg/pagination 统一收敛默认值与上限。
func parsePagination(c *gin.Context, pageKey, pageSizeKey string, defaultPageSize int) (page, pageSize int, ok bool) {
	return parsePaginationAlias(c, []string{pageKey}, []string{pageSizeKey}, defaultPageSize)
}

// parsePaginationAlias 与 parsePagination 相同，但允许页大小有多个参数名。
// 历史上 knowledge 接口同时接受 pageSize（前端）和 page_size（旧客户端），
// 两个名字都必须走同一套校验，不能只修其中一条路径。
func parsePaginationAlias(c *gin.Context, pageKeys, pageSizeKeys []string, defaultPageSize int) (page, pageSize int, ok bool) {
	page = pagination.DefaultPage
	pageSize = defaultPageSize

	if key, raw, found := firstQuery(c, pageKeys...); found {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < pagination.DefaultPage {
			c.JSON(http.StatusBadRequest, model.BadRequest(key+" 必须是 >= 1 的整数"))
			return 0, 0, false
		}
		page = parsed
	}
	if key, raw, found := firstQuery(c, pageSizeKeys...); found {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			c.JSON(http.StatusBadRequest, model.BadRequest(key+" 必须是 >= 1 的整数"))
			return 0, 0, false
		}
		pageSize = parsed
	}

	page, pageSize = pagination.NormalizeWithDefault(page, pageSize, defaultPageSize)
	return page, pageSize, true
}

// parseLimit 处理「只要条数不要页码」的接口（推荐、历史记录等）。
// 非法值返回 400；超过 max 的值被收敛到 max，避免一次请求拖垮下游。
func parseLimit(c *gin.Context, key string, defaultLimit, maxLimit int) (int, bool) {
	raw := c.Query(key)
	if raw == "" {
		return defaultLimit, true
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 1 {
		c.JSON(http.StatusBadRequest, model.BadRequest(key+" 必须是 >= 1 的整数"))
		return 0, false
	}
	if maxLimit > 0 && parsed > maxLimit {
		parsed = maxLimit
	}
	return parsed, true
}

// firstQuery 返回第一个非空参数，用于兼容同一语义的多个参数名。
// 空字符串视作「没传」，避免 `?pageSize=` 被当成非法值而拒绝。
func firstQuery(c *gin.Context, keys ...string) (key, value string, found bool) {
	for _, k := range keys {
		if raw := c.Query(k); raw != "" {
			return k, raw, true
		}
	}
	return "", "", false
}

func (h *Handler) ProxyProcessService(c *gin.Context) {
	target, err := url.Parse(h.ProcessServiceURL)
	if err != nil {
		logger.Error("Parse process service URL failed", logger.ErrorField(err))
		c.JSON(500, gin.H{"error": "服务配置错误"})
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = processAPIClient.Transport
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
