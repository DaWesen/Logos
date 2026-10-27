package handler

import (
	"net/http"
	"strconv"

	"Logos/internal/service/platform/gateway/model"
	pb "Logos/proto_gen/billing"

	"github.com/gin-gonic/gin"
)

func (h *Handler) Deposit(c *gin.Context) {
	if h.BillingClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}

	var req pb.DepositRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	req.UserId = userID

	resp, err := h.BillingClient.Deposit(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}

	statusCode := 200
	if resp.Code != 0 {
		statusCode = int(resp.Code)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data:    resp.Data,
	})
}

func (h *Handler) GetAccount(c *gin.Context) {
	if h.BillingClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}

	req := &pb.GetAccountRequest{}

	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	req.UserId = userID

	resp, err := h.BillingClient.GetAccount(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}

	statusCode := 200
	if resp.Code != 0 {
		statusCode = int(resp.Code)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data:    resp.Data,
	})
}

func (h *Handler) GetTransactions(c *gin.Context) {
	if h.BillingClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))

	var txType pb.TransactionType = pb.TransactionType_TRANSACTION_TYPE_UNSPECIFIED
	if t := c.Query("type"); t != "" {
		if ti, err := strconv.Atoi(t); err == nil {
			txType = pb.TransactionType(ti)
		}
	}

	req := &pb.GetTransactionsRequest{
		Type:     txType,
		Page:     int32(page),
		PageSize: int32(pageSize),
	}

	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	req.UserId = userID

	resp, err := h.BillingClient.GetTransactions(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}

	statusCode := 200
	if resp.Code != 0 {
		statusCode = int(resp.Code)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data: gin.H{
			"transactions": resp.Transactions,
			"total":        resp.Total,
		},
	})
}

func (h *Handler) GetUsageStats(c *gin.Context) {
	if h.BillingClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}

	var item pb.BillingItem = pb.BillingItem_BILLING_ITEM_UNSPECIFIED
	if i := c.Query("item"); i != "" {
		if itemVal, err := strconv.Atoi(i); err == nil {
			item = pb.BillingItem(itemVal)
		}
	}

	req := &pb.GetUsageStatsRequest{
		Item: item,
	}

	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	req.UserId = userID

	resp, err := h.BillingClient.GetUsageStats(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}

	statusCode := 200
	if resp.Code != 0 {
		statusCode = int(resp.Code)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data: gin.H{
			"stats": resp.Stats,
		},
	})
}

func (h *Handler) ConsumeModelCall(c *gin.Context) {
	if h.BillingClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}

	var req pb.ConsumeModelCallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	req.UserId = userID
	// token 数由调用方传入，标记来源便于与 AI 服务内部上报的真实用量区分
	if req.Metadata == nil {
		req.Metadata = map[string]string{}
	}
	req.Metadata["token_source"] = "client"

	resp, err := h.BillingClient.ConsumeModelCall(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}

	statusCode := 200
	if resp.Code != 0 {
		statusCode = int(resp.Code)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data:    resp.Amount,
	})
}

func (h *Handler) ConsumeEmbedding(c *gin.Context) {
	if h.BillingClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}

	var req pb.ConsumeEmbeddingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	req.UserId = userID

	resp, err := h.BillingClient.ConsumeEmbedding(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}

	statusCode := 200
	if resp.Code != 0 {
		statusCode = int(resp.Code)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data:    resp.Amount,
	})
}

func (h *Handler) ConsumeStorage(c *gin.Context) {
	if h.BillingClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}

	var req pb.ConsumeStorageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	req.UserId = userID

	resp, err := h.BillingClient.ConsumeStorage(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}

	statusCode := 200
	if resp.Code != 0 {
		statusCode = int(resp.Code)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data:    resp.Amount,
	})
}

func (h *Handler) ConsumeBandwidth(c *gin.Context) {
	if h.BillingClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}

	var req pb.ConsumeBandwidthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	req.UserId = userID

	resp, err := h.BillingClient.ConsumeBandwidth(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}

	statusCode := 200
	if resp.Code != 0 {
		statusCode = int(resp.Code)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data:    resp.Amount,
	})
}

func (h *Handler) Withdraw(c *gin.Context) {
	if h.BillingClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}

	var req pb.WithdrawRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	req.UserId = userID

	resp, err := h.BillingClient.Withdraw(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}

	statusCode := 200
	if resp.Code != 0 {
		statusCode = int(resp.Code)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data:    resp.Data,
	})
}

func (h *Handler) Refund(c *gin.Context) {
	if h.BillingClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}

	var req pb.RefundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	req.UserId = userID

	resp, err := h.BillingClient.Refund(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}

	statusCode := 200
	if resp.Code != 0 {
		statusCode = int(resp.Code)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data:    resp.Data,
	})
}
