package handler

import (
	"context"

	pb "Logos/proto_gen/im"
)

// IMServiceImpl IM 服务 Handler
// TODO: 实现 IMService 接口
type IMServiceImpl struct {
	pb.UnimplementedIMServiceServer
}

// Connect implements the IMServiceImpl interface.
func (s *IMServiceImpl) Connect(ctx context.Context, req *pb.ConnectRequest) (*pb.ConnectResponse, error) {
	return &pb.ConnectResponse{Code: 501, Message: "not implemented"}, nil
}

// Disconnect implements the IMServiceImpl interface.
func (s *IMServiceImpl) Disconnect(ctx context.Context, req *pb.DisconnectRequest) (*pb.DisconnectResponse, error) {
	return &pb.DisconnectResponse{Code: 501, Message: "not implemented"}, nil
}

// GetOnlineStatus implements the IMServiceImpl interface.
func (s *IMServiceImpl) GetOnlineStatus(ctx context.Context, req *pb.GetOnlineStatusRequest) (*pb.GetOnlineStatusResponse, error) {
	return &pb.GetOnlineStatusResponse{Code: 501, Message: "not implemented"}, nil
}

// SetOnlineStatus implements the IMServiceImpl interface.
func (s *IMServiceImpl) SetOnlineStatus(ctx context.Context, req *pb.SetOnlineStatusRequest) (*pb.SetOnlineStatusResponse, error) {
	return &pb.SetOnlineStatusResponse{Code: 501, Message: "not implemented"}, nil
}

// Heartbeat implements the IMServiceImpl interface.
func (s *IMServiceImpl) Heartbeat(ctx context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	return &pb.HeartbeatResponse{Code: 501, Message: "not implemented"}, nil
}

// SendTypingStatus implements the IMServiceImpl interface.
func (s *IMServiceImpl) SendTypingStatus(ctx context.Context, req *pb.SendTypingStatusRequest) (*pb.SendTypingStatusResponse, error) {
	return &pb.SendTypingStatusResponse{Code: 501, Message: "not implemented"}, nil
}

// BroadcastMessage implements the IMServiceImpl interface.
func (s *IMServiceImpl) BroadcastMessage(ctx context.Context, req *pb.BroadcastMessageRequest) (*pb.BroadcastMessageResponse, error) {
	return &pb.BroadcastMessageResponse{Code: 501, Message: "not implemented"}, nil
}

// SyncOfflineMessages implements the IMServiceImpl interface.
func (s *IMServiceImpl) SyncOfflineMessages(ctx context.Context, req *pb.SyncOfflineMessagesRequest) (*pb.SyncOfflineMessagesResponse, error) {
	return &pb.SyncOfflineMessagesResponse{Code: 501, Message: "not implemented"}, nil
}

// StreamMessages implements the IMServiceImpl interface.
func (s *IMServiceImpl) StreamMessages(req *pb.ConnectRequest, stream pb.IMService_StreamMessagesServer) error {
	return nil
}
