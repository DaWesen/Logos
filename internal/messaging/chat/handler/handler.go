package handler

import (
	"context"

	pb "Logos/proto_gen/chat"
	pbCommon "Logos/proto_gen/common"
)

// ChatServiceImpl Chat 服务 Handler
// TODO: 实现 ChatService 接口
type ChatServiceImpl struct {
	pb.UnimplementedChatServiceServer
}

// SendMessage implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) SendMessage(ctx context.Context, req *pb.SendMessageReq) (*pb.SendMessageResp, error) {
	return &pb.SendMessageResp{BaseResp: &pbCommon.BaseResp{StatusCode: 501, StatusMessage: "not implemented"}}, nil
}

// GetMessageHistory implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) GetMessageHistory(ctx context.Context, req *pb.GetHistoryReq) (*pb.HistoryResp, error) {
	return &pb.HistoryResp{BaseResp: &pbCommon.BaseResp{StatusCode: 501, StatusMessage: "not implemented"}}, nil
}

// MarkMessagesRead implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) MarkMessagesRead(ctx context.Context, req *pb.MarkReadReq) (*pbCommon.BaseResp, error) {
	return &pbCommon.BaseResp{StatusCode: 501, StatusMessage: "not implemented"}, nil
}

// CreateGroup implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) CreateGroup(ctx context.Context, req *pb.CreateGroupReq) (*pb.GroupResp, error) {
	return &pb.GroupResp{BaseResp: &pbCommon.BaseResp{StatusCode: 501, StatusMessage: "not implemented"}}, nil
}

// JoinGroup implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) JoinGroup(ctx context.Context, req *pb.GroupMemberReq) (*pbCommon.BaseResp, error) {
	return &pbCommon.BaseResp{StatusCode: 501, StatusMessage: "not implemented"}, nil
}

// LeaveGroup implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) LeaveGroup(ctx context.Context, req *pb.GroupMemberReq) (*pbCommon.BaseResp, error) {
	return &pbCommon.BaseResp{StatusCode: 501, StatusMessage: "not implemented"}, nil
}
