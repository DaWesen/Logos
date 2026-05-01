package handler

import (
	"context"

	"Logos/internal/service/messaging/im/service"
	"Logos/pkg/auth"
	"Logos/pkg/logger"
	pb "Logos/proto_gen/im"
)

type IMServiceImpl struct {
	pb.UnimplementedIMServiceServer
	imService service.IMService
}

func NewIMServiceImpl(imService service.IMService) *IMServiceImpl {
	return &IMServiceImpl{imService: imService}
}

func (s *IMServiceImpl) Connect(ctx context.Context, req *pb.ConnectRequest) (*pb.ConnectResponse, error) {
	logger.Info("IM连接请求",
		logger.StringField("user_id", req.UserId),
		logger.StringField("device_id", req.DeviceId))

	sessionID := "session_" + req.UserId
	err := s.imService.Connect(ctx, req.UserId, req.DeviceId, sessionID)
	if err != nil {
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
	logger.Info("IM断开连接请求", logger.StringField("session_id", req.SessionId))
	err := s.imService.Disconnect(ctx, req.SessionId)
	if err != nil {
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
	err := s.imService.Heartbeat(ctx, req.SessionId)
	if err != nil {
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
	userID := auth.MustGetUserID(ctx)
	online := req.Status == pb.OnlineStatus_ONLINE_STATUS_ONLINE
	err := s.imService.SetOnlineStatus(ctx, userID, online)
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
	userID := auth.MustGetUserID(ctx)
	typing := req.Status == pb.TypingStatus_TYPING_STATUS_TYPING
	err := s.imService.SendTypingStatus(ctx, userID, req.ChatId, typing)
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
