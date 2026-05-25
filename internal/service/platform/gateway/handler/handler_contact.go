package handler

import (
	"net/http"

	pbContact "Logos/proto_gen/contact"
	"Logos/pkg/logger"

	"github.com/gin-gonic/gin"
)

func getUserIDFromContext(c *gin.Context) string {
	userID, _ := c.Get("user_id")
	if id, ok := userID.(string); ok {
		return id
	}
	return ""
}

func (h *Handler) AddFriend(c *gin.Context) {
	var req struct {
		UserID  string `json:"user_id"`
		Remark  string `json:"remark"`
		Message string `json:"message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	resp, err := h.ContactClient.AddFriend(c.Request.Context(), &pbContact.AddFriendRequest{
		UserId:  req.UserID,
		Remark:  req.Remark,
		Message: req.Message,
	})
	if err != nil {
		logger.Error("gRPC AddFriend 调用失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if resp.Code != 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": resp.Message})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": resp.Message})
}

func (h *Handler) HandleFriendRequest(c *gin.Context) {
	var req struct {
		RequestID string `json:"request_id"`
		Status    string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	var status pbContact.FriendRequestStatus
	switch req.Status {
	case "accepted":
		status = pbContact.FriendRequestStatus_FRIEND_REQUEST_STATUS_ACCEPTED
	case "rejected":
		status = pbContact.FriendRequestStatus_FRIEND_REQUEST_STATUS_REJECTED
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的状态"})
		return
	}

	resp, err := h.ContactClient.HandleFriendRequest(c.Request.Context(), &pbContact.HandleFriendRequestRequest{
		RequestId: req.RequestID,
		Status:    status,
	})
	if err != nil {
		logger.Error("gRPC HandleFriendRequest 调用失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if resp.Code != 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": resp.Message})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": resp.Message})
}

func (h *Handler) GetFriendRequests(c *gin.Context) {
	resp, err := h.ContactClient.GetFriendRequests(c.Request.Context(), &pbContact.GetFriendRequestsRequest{
		Page:     1,
		PageSize: 50,
	})
	if err != nil {
		logger.Error("gRPC GetFriendRequests 调用失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if resp.Code != 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": resp.Message})
		return
	}
	c.JSON(http.StatusOK, gin.H{"requests": resp.Requests, "total": resp.Total})
}

func (h *Handler) GetFriendList(c *gin.Context) {
	resp, err := h.ContactClient.GetFriendList(c.Request.Context(), &pbContact.GetFriendListRequest{
		Page:     1,
		PageSize: 200,
	})
	if err != nil {
		logger.Error("gRPC GetFriendList 调用失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if resp.Code != 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": resp.Message})
		return
	}
	c.JSON(http.StatusOK, gin.H{"friends": resp.Friends, "total": resp.Total})
}

func (h *Handler) DeleteFriend(c *gin.Context) {
	var req struct {
		FriendID string `json:"friend_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	resp, err := h.ContactClient.DeleteFriend(c.Request.Context(), &pbContact.DeleteFriendRequest{
		FriendId: req.FriendID,
	})
	if err != nil {
		logger.Error("gRPC DeleteFriend 调用失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if resp.Code != 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": resp.Message})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": resp.Message})
}

func (h *Handler) CheckFriendship(c *gin.Context) {
	userID := getUserIDFromContext(c)
	friendID := c.Query("friend_id")
	if friendID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 friend_id"})
		return
	}

	resp, err := h.ContactClient.CheckFriendship(c.Request.Context(), &pbContact.CheckFriendshipRequest{
		UserId:   userID,
		FriendId: friendID,
	})
	if err != nil {
		logger.Error("gRPC CheckFriendship 调用失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"is_friend": resp.IsFriend, "is_blocked": resp.IsBlocked})
}

func (h *Handler) UpdateFriendRemark(c *gin.Context) {
	var req struct {
		FriendID string `json:"friend_id"`
		Remark   string `json:"remark"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	resp, err := h.ContactClient.UpdateFriendRemark(c.Request.Context(), &pbContact.UpdateFriendRemarkRequest{
		FriendId: req.FriendID,
		Remark:   req.Remark,
	})
	if err != nil {
		logger.Error("gRPC UpdateFriendRemark 调用失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if resp.Code != 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": resp.Message})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": resp.Message})
}

func (h *Handler) CreateFriendGroup(c *gin.Context) {
	var req struct {
		Name      string `json:"name"`
		SortOrder int32  `json:"sort_order"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	resp, err := h.ContactClient.CreateFriendGroup(c.Request.Context(), &pbContact.CreateFriendGroupRequest{
		Name:      req.Name,
		SortOrder: req.SortOrder,
	})
	if err != nil {
		logger.Error("gRPC CreateFriendGroup 调用失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if resp.Code != 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": resp.Message})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": resp.Message, "data": resp.Data})
}

func (h *Handler) DeleteFriendGroup(c *gin.Context) {
	var req struct {
		GroupID string `json:"group_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	resp, err := h.ContactClient.DeleteFriendGroup(c.Request.Context(), &pbContact.DeleteFriendGroupRequest{
		GroupId: req.GroupID,
	})
	if err != nil {
		logger.Error("gRPC DeleteFriendGroup 调用失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if resp.Code != 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": resp.Message})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": resp.Message})
}

func (h *Handler) UpdateFriendGroup(c *gin.Context) {
	var req struct {
		GroupID   string `json:"group_id"`
		Name      string `json:"name"`
		SortOrder int32  `json:"sort_order"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	resp, err := h.ContactClient.UpdateFriendGroup(c.Request.Context(), &pbContact.UpdateFriendGroupRequest{
		GroupId:   req.GroupID,
		Name:      req.Name,
		SortOrder: req.SortOrder,
	})
	if err != nil {
		logger.Error("gRPC UpdateFriendGroup 调用失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if resp.Code != 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": resp.Message})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": resp.Message})
}

func (h *Handler) GetFriendGroups(c *gin.Context) {
	resp, err := h.ContactClient.GetFriendGroups(c.Request.Context(), &pbContact.GetFriendGroupsRequest{})
	if err != nil {
		logger.Error("gRPC GetFriendGroups 调用失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if resp.Code != 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": resp.Message})
		return
	}
	c.JSON(http.StatusOK, gin.H{"groups": resp.Groups})
}

func (h *Handler) MoveFriendToGroup(c *gin.Context) {
	var req struct {
		FriendID string `json:"friend_id"`
		GroupID  string `json:"group_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	resp, err := h.ContactClient.MoveFriendToGroup(c.Request.Context(), &pbContact.MoveFriendToGroupRequest{
		FriendId: req.FriendID,
		GroupId:  req.GroupID,
	})
	if err != nil {
		logger.Error("gRPC MoveFriendToGroup 调用失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if resp.Code != 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": resp.Message})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": resp.Message})
}

func (h *Handler) BlockUser(c *gin.Context) {
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	resp, err := h.ContactClient.BlockUser(c.Request.Context(), &pbContact.BlockUserRequest{
		UserId: req.UserID,
	})
	if err != nil {
		logger.Error("gRPC BlockUser 调用失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if resp.Code != 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": resp.Message})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": resp.Message})
}

func (h *Handler) UnblockUser(c *gin.Context) {
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	resp, err := h.ContactClient.UnblockUser(c.Request.Context(), &pbContact.UnblockUserRequest{
		UserId: req.UserID,
	})
	if err != nil {
		logger.Error("gRPC UnblockUser 调用失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if resp.Code != 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": resp.Message})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": resp.Message})
}

func (h *Handler) GetBlacklist(c *gin.Context) {
	resp, err := h.ContactClient.GetBlacklist(c.Request.Context(), &pbContact.GetBlacklistRequest{
		Page:     1,
		PageSize: 200,
	})
	if err != nil {
		logger.Error("gRPC GetBlacklist 调用失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if resp.Code != 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": resp.Message})
		return
	}
	c.JSON(http.StatusOK, gin.H{"records": resp.Records, "total": resp.Total})
}
