package handler

import (
	"context"
	"strings"

	"Logos/internal/service/messaging/im/service"
	"Logos/pkg/auth"
	"Logos/pkg/logger"
	pb "Logos/proto_gen/im"
)

const sessionIDPrefix = "session_"

// sessionOwner 解析会话 ID 归属的用户；格式不符时返回空串（视为无法归属）。
func sessionOwner(sessionID string) string {
	if !strings.HasPrefix(sessionID, sessionIDPrefix) {
		return ""
	}
	return strings.TrimPrefix(sessionID, sessionIDPrefix)
}

type IMServiceImpl struct {
	pb.UnimplementedIMServiceServer
	imService service.IMService
}

func NewIMServiceImpl(imService service.IMService) *IMServiceImpl {
	return &IMServiceImpl{imService: imService}
}

func (s *IMServiceImpl) Connect(ctx context.Context, req *pb.ConnectRequest) (*pb.ConnectResponse, error) {
	userID, err := auth.GetUserID(ctx)
	if err != nil {
		return &pb.ConnectResponse{Code: 401, Message: "未提供认证信息"}, nil
	}
	// 以认证身份为准，忽略请求体里的 user_id，避免冒充他人建立会话。
	logger.Info("IM连接请求",
		logger.StringField("user_id", userID),
		logger.StringField("device_id", req.DeviceId))

	sessionID := sessionIDPrefix + userID
	if err := s.imService.Connect(ctx, userID, req.DeviceId, sessionID); err != nil {
		logger.Error("IM连接失败", logger.ErrorField(err))
		return &pb.ConnectResponse{
			Code:    500,
			Message: err.Error(),
		}, nil
	}

	return &pb.ConnectResponse{
		Code:      200,
		Message:   "连接成功",
		SessionId: sessionID,
	}, nil
}

func (s *IMServiceImpl) Disconnect(ctx context.Context, req *pb.DisconnectRequest) (*pb.DisconnectResponse, error) {
	userID, err := auth.GetUserID(ctx)
	if err != nil {
		return &pb.DisconnectResponse{Code: 401, Message: "未提供认证信息"}, nil
	}
	// 会话 ID 由服务端按用户生成，只允许操作自己的会话。
	if sessionOwner(req.SessionId) != userID {
		logger.Warn("尝试断开他人会话，已拒绝",
			logger.StringField("user_id", userID),
			logger.StringField("session_id", req.SessionId))
		return &pb.DisconnectResponse{Code: 403, Message: "无权操作该会话"}, nil
	}
	logger.Info("IM断开连接请求", logger.StringField("session_id", req.SessionId))
	if err := s.imService.Disconnect(ctx, req.SessionId); err != nil {
		logger.Error("IM断开连接失败", logger.ErrorField(err))
		return &pb.DisconnectResponse{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	return &pb.DisconnectResponse{
		Code:    200,
		Message: "断开连接成功",
	}, nil
}

func (s *IMServiceImpl) Heartbeat(ctx context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	userID, err := auth.GetUserID(ctx)
	if err != nil {
		return &pb.HeartbeatResponse{Code: 401, Message: "未提供认证信息"}, nil
	}
	if sessionOwner(req.SessionId) != userID {
		logger.Warn("尝试为他人会话发送心跳，已拒绝",
			logger.StringField("user_id", userID),
			logger.StringField("session_id", req.SessionId))
		return &pb.HeartbeatResponse{Code: 403, Message: "无权操作该会话"}, nil
	}
	if err := s.imService.Heartbeat(ctx, req.SessionId); err != nil {
		logger.Warn("心跳失败",
			logger.StringField("session_id", req.SessionId),
			logger.ErrorField(err))
		return &pb.HeartbeatResponse{
			Code:    500,
			Message: "心跳失败",
		}, nil
	}
	return &pb.HeartbeatResponse{
		Code:    200,
		Message: "心跳成功",
	}, nil
}

func (s *IMServiceImpl) GetOnlineStatus(ctx context.Context, req *pb.GetOnlineStatusRequest) (*pb.GetOnlineStatusResponse, error) {
	boolStatus, err := s.imService.GetOnlineStatus(ctx, req.UserIds)
	if err != nil {
		logger.Error("获取在线状态失败", logger.ErrorField(err))
		return &pb.GetOnlineStatusResponse{
			Code:    500,
			Message: err.Error(),
		}, nil
	}

	statusMap := make(map[string]pb.OnlineStatus)
	for userID, online := range boolStatus {
		if online {
			statusMap[userID] = pb.OnlineStatus_ONLINE_STATUS_ONLINE
		} else {
			statusMap[userID] = pb.OnlineStatus_ONLINE_STATUS_OFFLINE
		}
	}
	return &pb.GetOnlineStatusResponse{
		Code:     200,
		Message:  "获取成功",
		Statuses: statusMap,
	}, nil
}

func (s *IMServiceImpl) SetOnlineStatus(ctx context.Context, req *pb.SetOnlineStatusRequest) (*pb.SetOnlineStatusResponse, error) {
	userID, err := auth.GetUserID(ctx)
	if err != nil {
		return &pb.SetOnlineStatusResponse{Code: 401, Message: "未提供认证信息"}, nil
	}
	online := req.Status == pb.OnlineStatus_ONLINE_STATUS_ONLINE
	err = s.imService.SetOnlineStatus(ctx, userID, online)
	if err != nil {
		logger.Error("设置在线状态失败", logger.ErrorField(err))
		return &pb.SetOnlineStatusResponse{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	return &pb.SetOnlineStatusResponse{
		Code:    200,
		Message: "设置成功",
	}, nil
}

func (s *IMServiceImpl) SendTypingStatus(ctx context.Context, req *pb.SendTypingStatusRequest) (*pb.SendTypingStatusResponse, error) {
	userID, err := auth.GetUserID(ctx)
	if err != nil {
		return &pb.SendTypingStatusResponse{Code: 401, Message: "未提供认证信息"}, nil
	}
	typing := req.Status == pb.TypingStatus_TYPING_STATUS_TYPING
	err = s.imService.SendTypingStatus(ctx, userID, req.ChatId, typing)
	if err != nil {
		logger.Error("发送输入状态失败", logger.ErrorField(err))
		return &pb.SendTypingStatusResponse{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	return &pb.SendTypingStatusResponse{
		Code:    200,
		Message: "发送成功",
	}, nil
}

func (s *IMServiceImpl) BroadcastMessage(ctx context.Context, req *pb.BroadcastMessageRequest) (*pb.BroadcastMessageResponse, error) {
	err := s.imService.BroadcastMessage(ctx, req.Content)
	if err != nil {
		logger.Error("广播消息失败", logger.ErrorField(err))
		return &pb.BroadcastMessageResponse{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	return &pb.BroadcastMessageResponse{
		Code:    200,
		Message: "广播成功",
	}, nil
}
