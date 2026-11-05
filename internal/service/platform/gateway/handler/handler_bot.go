package handler

import (
	"Logos/internal/service/platform/gateway/model"
	"Logos/pkg/logger"
	"Logos/pkg/pagination"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	pb "Logos/proto_gen/bot"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (h *Handler) CreateBot(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	var req pb.CreateBotRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	// 身份只能来自认证中间件。旧写法 `req.UserId, _ = userID.(string)` 在
	// key 缺失或类型不符时把身份静默变成空串，下游 Bot 服务会以「空用户」
	// 建 Bot（归属列写成 ""），后续任何按用户过滤的查询都查不到它。
	uid, ok := h.requireUserID(c)
	if !ok {
		return
	}
	req.UserId = uid

	resp, err := h.BotClient.CreateBot(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapStatusCode(resp.Code)
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data:    resp.Data,
	})
}

func (h *Handler) UpdateBot(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	var req pb.UpdateBotRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	req.BotId = c.Param("id")

	uid, ok := h.requireUserID(c)
	if !ok {
		return
	}
	req.UserId = uid

	resp, err := h.BotClient.UpdateBot(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapStatusCode(resp.Code)
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data:    resp.Data,
	})
}

func (h *Handler) DeleteBot(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	id := c.Param("id")
	req := &pb.DeleteBotRequest{BotId: id}
	// 透传 user_id，Bot 服务据此做归属校验
	resp, err := h.BotClient.DeleteBot(h.withUserID(c), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapStatusCode(resp.Code)
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
	})
}

func (h *Handler) GetBot(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	id := c.Param("id")
	req := &pb.GetBotRequest{BotId: id}
	// 透传 user_id，Bot 服务据此做归属校验（非所有者按不存在返回）
	resp, err := h.BotClient.GetBot(h.withUserID(c), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapStatusCode(resp.Code)
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data:    resp.Data,
	})
}

func (h *Handler) ListBots(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	page, pageSize, ok := parsePagination(c, "page", "pageSize", pagination.DefaultPageSize)
	if !ok {
		return
	}

	// 字符串枚举必须命中白名单：拼写错误/大小写不一致/误传数字都会 400，
	// 而不是掉进 else 被当成「不过滤」返回全部 Bot（过滤静默失效）。
	botTypeValue, ok := parseEnumQuery(c, "type", int32(pb.BotType_BOT_TYPE_UNSPECIFIED), map[string]int32{
		"builtin": int32(pb.BotType_BOT_TYPE_BUILTIN),
		"custom":  int32(pb.BotType_BOT_TYPE_CUSTOM),
	})
	if !ok {
		return
	}

	statusValue, ok := parseEnumQuery(c, "status", int32(pb.BotStatus_BOT_STATUS_UNSPECIFIED), map[string]int32{
		"active":   int32(pb.BotStatus_BOT_STATUS_ACTIVE),
		"inactive": int32(pb.BotStatus_BOT_STATUS_INACTIVE),
		"deleted":  int32(pb.BotStatus_BOT_STATUS_DELETED),
	})
	if !ok {
		return
	}

	req := &pb.ListBotsRequest{
		Type:     pb.BotType(botTypeValue),
		Status:   pb.BotStatus(statusValue),
		Page:     int32(page),
		PageSize: int32(pageSize),
	}

	// 列表接口的身份缺失后果比写接口更严重：DAO 里 `if userId != ""`
	// 才加过滤，空身份等于「不加任何 user_id 条件」，会把全库用户的
	// Bot 一起返回（水平越权）。这里必须先 401，不能继续往下查。
	uid, ok := h.requireUserID(c)
	if !ok {
		return
	}
	req.UserId = uid

	resp, err := h.BotClient.ListBots(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapStatusCode(resp.Code)
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data: gin.H{
			"bots":  resp.Bots,
			"total": resp.Total,
		},
	})
}

func (h *Handler) SendBotMessage(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	var req pb.SendBotMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	uid, ok := h.requireUserID(c)
	if !ok {
		return
	}
	req.UserId = uid

	var header metadata.MD
	resp, err := h.BotClient.SendBotMessage(c.Request.Context(), &req, grpc.Header(&header))
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}

	data := gin.H{
		"content": resp.Content,
		"done":    resp.Done,
		"chat_id": resp.ChatId,
	}

	if costVals := header.Get("x-cost"); len(costVals) > 0 {
		if cost, parseErr := strconv.ParseFloat(costVals[0], 64); parseErr == nil {
			data["cost"] = cost
		}
	}
	if tokenVals := header.Get("x-tokens"); len(tokenVals) > 0 {
		if tokens, parseErr := strconv.ParseInt(tokenVals[0], 10, 64); parseErr == nil {
			data["tokens"] = tokens
		}
	}

	c.JSON(http.StatusOK, model.Response{
		Code:    200,
		Message: "success",
		Data:    data,
	})
}

// StreamBotMessage 以 SSE 逐 token 转发 Bot 的流式回复。
// 客户端断开时 c.Request.Context() 取消，gRPC 流随之终止，
// Bot 侧的 LLM 生成也会通过 ctx 级联取消。
func (h *Handler) StreamBotMessage(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	var req pb.SendBotMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	uid, ok := h.requireUserID(c)
	if !ok {
		return
	}
	req.UserId = uid
	req.Stream = true

	stream, err := h.BotClient.StreamBotMessage(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	// 禁用 Nginx 等反向代理的缓冲，保证 token 实时到达
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()

	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			return
		}
		if err != nil {
			// 错误事件写失败也不必再重试：客户端已经断了，直接结束
			_ = writeSSEEvent(c, map[string]interface{}{"error": err.Error(), "done": true})
			return
		}

		event := map[string]interface{}{
			"content": resp.GetContent(),
			"done":    resp.GetDone(),
		}
		if resp.GetChatId() != "" {
			event["chat_id"] = resp.GetChatId()
		}
		if resp.GetCost() > 0 {
			event["cost"] = resp.GetCost()
		}
		if resp.GetTokens() > 0 {
			event["tokens"] = resp.GetTokens()
		}
		if err := writeSSEEvent(c, event); err != nil {
			// 客户端已断开：结束 handler 会取消请求上下文，进而取消上游 gRPC 流，
			// 停止继续生成，避免为一个已经离开的客户端持续消耗 token。
			logger.Warn("SSE 写入失败，关闭流", logger.ErrorField(err))
			return
		}

		if resp.GetDone() {
			return
		}
	}
}

// sseWriteTimeout 单次 SSE 写超时。慢客户端读不动 socket 时 Write 会一直阻塞，
// 拖住整个 gRPC 流与生成槽；超时后写返回错误，由调用方结束流并取消上游生成。
const sseWriteTimeout = 10 * time.Second

// writeSSEEvent 把事件写成 SSE 帧并立即刷出。返回错误表示客户端已不可写
// （断开、超时或代理断开），调用方必须结束流，不能继续读取上游。
func writeSSEEvent(c *gin.Context, data map[string]interface{}) error {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("序列化 SSE 事件失败: %w", err)
	}
	// ResponseController 在底层支持时设置写截止时间；不支持时返回 ErrNotSupported，
	// 此时退化为依赖写错误返回，行为仍然正确。
	_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(sseWriteTimeout))
	if _, err := c.Writer.WriteString("data: " + string(jsonBytes) + "\n\n"); err != nil {
		return err
	}
	c.Writer.Flush()
	return nil
}

func (h *Handler) GetBotHistory(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	botID := c.Query("bot_id")
	chatID := c.Query("chat_id")
	limit, ok := parseLimit(c, "limit", 50, pagination.MaxPageSize)
	if !ok {
		return
	}

	req := &pb.GetBotHistoryRequest{
		BotId:  botID,
		ChatId: chatID,
		Limit:  int32(limit),
	}

	// before_time 是「取该时间点之前的历史」的硬过滤条件。旧实现解析失败
	// 就跳过赋值，接口照样 200，但返回的是全部历史——分页游标类参数尤其
	// 不能静默失效，否则客户端会重复拉取。
	beforeTime, ok := parseInt64Query(c, "before_time", 0)
	if !ok {
		return
	}
	if beforeTime > 0 {
		req.BeforeTime = &timestamppb.Timestamp{Seconds: beforeTime}
	}

	// 透传 user_id，Bot 服务据此校验会话归属
	resp, err := h.BotClient.GetBotHistory(h.withUserID(c), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapStatusCode(resp.Code)
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data: gin.H{
			"messages": resp.Messages,
			"hasMore":  resp.HasMore,
		},
	})
}

func (h *Handler) GetUserMemory(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	botID := c.Query("bot_id")
	// 旧写法 `userID.(string)` 没有 ok 断言：中间件没写入 key 或类型不是
	// string 时直接 panic，一个缺身份头的请求就能把 handler 打挂。
	uid, ok := h.requireUserID(c)
	if !ok {
		return
	}

	req := &pb.GetUserMemoryRequest{
		UserId: uid,
		BotId:  botID,
	}

	resp, err := h.BotClient.GetUserMemory(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapStatusCode(resp.Code)
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data:    resp.Memories,
	})
}

func (h *Handler) SetUserMemory(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	var req pb.SetUserMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	uid, ok := h.requireUserID(c)
	if !ok {
		return
	}
	req.UserId = uid

	resp, err := h.BotClient.SetUserMemory(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapStatusCode(resp.Code)
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
	})
}

func (h *Handler) DeleteUserMemory(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	botID := c.Query("bot_id")
	key := c.Query("key")
	uid, ok := h.requireUserID(c)
	if !ok {
		return
	}

	logger.Info("收到删除记忆请求",
		logger.StringField("user_id", uid),
		logger.StringField("bot_id", botID),
		logger.StringField("key", key),
	)

	req := &pb.DeleteUserMemoryRequest{
		UserId: uid,
		BotId:  botID,
		Key:    key,
	}

	resp, err := h.BotClient.DeleteUserMemory(c.Request.Context(), req)
	if err != nil {
		logger.Error("删除记忆gRPC调用失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}

	logger.Info("删除记忆响应",
		logger.IntField("code", int(resp.Code)),
		logger.StringField("message", resp.Message),
	)

	statusCode := mapStatusCode(resp.Code)
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
	})
}
