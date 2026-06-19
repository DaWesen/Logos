package handler

import (
	"context"
	"fmt"
	"net/http"

	"Logos/internal/service/platform/gateway/model"
	"Logos/pkg/cache"
	"Logos/pkg/logger"

	"github.com/gin-gonic/gin"
)

// BindQQRequest 绑定QQ号请求
type BindQQRequest struct {
	QQNumber string `json:"qq_number" binding:"required"`
}

// BindQQ 将当前用户的 QQ 号绑定到 Logos 账号
// 绑定后，从该QQ号发送给Bot的消息会以该用户身份出现在聊天页面
func (h *Handler) BindQQ(c *gin.Context) {
	var req BindQQRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, model.Error(401, "未授权"))
		return
	}

	uid, ok := userID.(string)
	if !ok {
		c.JSON(http.StatusInternalServerError, model.Error(500, "用户ID格式错误"))
		return
	}

	// 检查该QQ号是否已被其他用户绑定
	redis := cache.NewRedisCache()
	ctx := context.Background()
	existingBindKey := fmt.Sprintf("qq:user_bind:%s", req.QQNumber)
	existingUID, err := redis.Get(ctx, existingBindKey)
	if err == nil && existingUID != "" && existingUID != uid {
		c.JSON(http.StatusConflict, model.Error(409, "该QQ号已被其他用户绑定"))
		return
	}

	// 写入双向映射
	// QQ号 → Logos UserID
	if err := redis.Set(ctx, existingBindKey, uid, 0); err != nil {
		logger.Error("绑定QQ号失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, model.Error(500, "绑定失败"))
		return
	}

	// Logos UserID → QQ号（反向映射）
	reverseKey := fmt.Sprintf("qq:user_reverse:%s", uid)
	if err := redis.Set(ctx, reverseKey, req.QQNumber, 0); err != nil {
		logger.Warn("写入QQ反向映射失败", logger.ErrorField(err))
	}

	logger.Info("QQ号绑定成功",
		logger.StringField("user_id", uid),
		logger.StringField("qq_number", req.QQNumber))

	c.JSON(http.StatusOK, model.Success(map[string]interface{}{
		"user_id":   uid,
		"qq_number": req.QQNumber,
	}))
}

// UnbindQQ 解绑QQ号
func (h *Handler) UnbindQQ(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, model.Error(401, "未授权"))
		return
	}

	uid, ok := userID.(string)
	if !ok {
		c.JSON(http.StatusInternalServerError, model.Error(500, "用户ID格式错误"))
		return
	}

	redis := cache.NewRedisCache()
	ctx := context.Background()

	// 查找该用户绑定的QQ号
	reverseKey := fmt.Sprintf("qq:user_reverse:%s", uid)
	qqNumber, err := redis.Get(ctx, reverseKey)
	if err != nil || qqNumber == "" {
		c.JSON(http.StatusNotFound, model.Error(404, "未找到QQ绑定"))
		return
	}

	// 删除双向映射
	bindKey := fmt.Sprintf("qq:user_bind:%s", qqNumber)
	redis.Delete(ctx, bindKey)
	redis.Delete(ctx, reverseKey)

	logger.Info("QQ号解绑成功",
		logger.StringField("user_id", uid),
		logger.StringField("qq_number", qqNumber))

	c.JSON(http.StatusOK, model.Success(map[string]interface{}{
		"user_id":   uid,
		"qq_number": qqNumber,
	}))
}

// GetQQBind 查询当前用户的QQ绑定状态
func (h *Handler) GetQQBind(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, model.Error(401, "未授权"))
		return
	}

	uid, ok := userID.(string)
	if !ok {
		c.JSON(http.StatusInternalServerError, model.Error(500, "用户ID格式错误"))
		return
	}

	redis := cache.NewRedisCache()
	ctx := context.Background()

	reverseKey := fmt.Sprintf("qq:user_reverse:%s", uid)
	qqNumber, err := redis.Get(ctx, reverseKey)

	result := map[string]interface{}{
		"user_id":   uid,
		"bound":     false,
		"qq_number": "",
	}

	if err == nil && qqNumber != "" {
		result["bound"] = true
		result["qq_number"] = qqNumber
	}

	c.JSON(http.StatusOK, model.Success(result))
}
