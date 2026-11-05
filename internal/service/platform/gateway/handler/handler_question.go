package handler

import (
	"encoding/json"
	"net/http"

	"Logos/internal/service/platform/gateway/model"
	"Logos/pkg/pagination"

	pbCommon "Logos/proto_gen/common"
	pb "Logos/proto_gen/question"

	"github.com/gin-gonic/gin"
)

func (h *Handler) AskQuestion(c *gin.Context) {
	if h.QuestionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	var req pb.QuestionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.QuestionClient.AskQuestion(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: gin.H{
		"answer":     resp.Answer,
		"confidence": resp.Confidence,
		"sources":    resp.Sources,
		"questionId": resp.QuestionId,
		"timestamp":  resp.Timestamp,
	}})
}

func (h *Handler) BatchAskQuestions(c *gin.Context) {
	if h.QuestionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	var req pb.BatchQuestionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.QuestionClient.BatchAskQuestions(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Answers})
}

func (h *Handler) GetHistory(c *gin.Context) {
	if h.QuestionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	// 问答历史是个人数据：身份只认认证中间件，?user_id= 不再能指定查询对象。
	userID, ok := h.resolveTargetUserID(c)
	if !ok {
		return
	}
	page, pageSize, ok := parsePagination(c, "page", "page_size", pagination.DefaultPageSize)
	if !ok {
		return
	}

	req := &pb.HistoryReq{
		UserId:   userID,
		Page:     int32(page),
		PageSize: int32(pageSize),
	}
	resp, err := h.QuestionClient.GetHistory(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: gin.H{
		"records": resp.Records,
		"total":   resp.Total,
	}})
}

func (h *Handler) SubmitFeedback(c *gin.Context) {
	if h.QuestionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	var req pb.FeedbackReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.QuestionClient.SubmitFeedback(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
}

func (h *Handler) GetRecommendedQuestions(c *gin.Context) {
	if h.QuestionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	// 路由是 /qa/recommended，没有 :userId；旧实现读 c.Param("userId") 恒为空，
	// ParseInt 失败后又吞掉错误，于是所有请求都在为「用户 0」生成推荐。
	userID, ok := h.requireNumericUserID(c)
	if !ok {
		return
	}
	limit, ok := parseLimit(c, "limit", 4, pagination.MaxPageSize)
	if !ok {
		return
	}
	resp, err := h.QuestionClient.GetRecommendedQuestions(c.Request.Context(), &pb.GetRecommendedQuestionsReq{UserId: userID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp)
	if statusCode != http.StatusOK {
		c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
		return
	}

	// 下游把结果放在 BaseResp.StatusMessage 的 JSON 里（rpc 返回类型就是 BaseResp），
	// 这是既有契约。网关此前只透传 message，前端拿到的 data 永远是空的，
	// 面板只能显示「暂无推荐问题」。这里解析后按前端期望的 [{question}] 返回。
	var payload struct {
		Questions []string `json:"questions"`
	}
	if err := json.Unmarshal([]byte(resp.GetStatusMessage()), &payload); err != nil {
		// 契约被破坏时不能装作成功：返回空数组会让「模型没生成」和「解析失败」
		// 看起来一模一样，排查时无从下手。
		c.JSON(http.StatusBadGateway, model.Error(502, "推荐问题响应格式错误"))
		return
	}
	if limit > 0 && len(payload.Questions) > limit {
		payload.Questions = payload.Questions[:limit]
	}
	items := make([]gin.H, 0, len(payload.Questions))
	for _, question := range payload.Questions {
		items = append(items, gin.H{"question": question})
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: "success", Data: items})
}

func _() { _ = (*pbCommon.BaseResp)(nil) }
