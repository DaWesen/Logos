package handler

import (
	"context"

	pb "Logos/proto_gen/chat"
)

// ChatServiceImpl Chat 服务 Handler
// TODO: 实现 ChatService 接口
type ChatServiceImpl struct {
	pb.UnimplementedChatServiceServer
}

// SendMessage implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) SendMessage(ctx context.Context, req *pb.SendMessageRequest) (*pb.SendMessageResponse, error) {
	return &pb.SendMessageResponse{Code: 501, Message: "not implemented"}, nil
}

// SearchMessages implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) SearchMessages(ctx context.Context, req *pb.SearchMessagesRequest) (*pb.SearchMessagesResponse, error) {
	return &pb.SearchMessagesResponse{Code: 501, Message: "not implemented"}, nil
}

// GetMessageHistory implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) GetMessageHistory(ctx context.Context, req *pb.GetMessageHistoryRequest) (*pb.GetMessageHistoryResponse, error) {
	return &pb.GetMessageHistoryResponse{Code: 501, Message: "not implemented"}, nil
}

// MarkMessagesRead implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) MarkMessagesRead(ctx context.Context, req *pb.MarkMessagesReadRequest) (*pb.MarkMessagesReadResponse, error) {
	return &pb.MarkMessagesReadResponse{Code: 501, Message: "not implemented"}, nil
}

// WithdrawMessage implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) WithdrawMessage(ctx context.Context, req *pb.WithdrawMessageRequest) (*pb.WithdrawMessageResponse, error) {
	return &pb.WithdrawMessageResponse{Code: 501, Message: "not implemented"}, nil
}

// EditMessage implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) EditMessage(ctx context.Context, req *pb.EditMessageRequest) (*pb.EditMessageResponse, error) {
	return &pb.EditMessageResponse{Code: 501, Message: "not implemented"}, nil
}

// CreateGroup implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) CreateGroup(ctx context.Context, req *pb.CreateGroupRequest) (*pb.CreateGroupResponse, error) {
	return &pb.CreateGroupResponse{Code: 501, Message: "not implemented"}, nil
}

// InviteGroupMember implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) InviteGroupMember(ctx context.Context, req *pb.InviteGroupMemberRequest) (*pb.InviteGroupMemberResponse, error) {
	return &pb.InviteGroupMemberResponse{Code: 501, Message: "not implemented"}, nil
}

// KickGroupMember implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) KickGroupMember(ctx context.Context, req *pb.KickGroupMemberRequest) (*pb.KickGroupMemberResponse, error) {
	return &pb.KickGroupMemberResponse{Code: 501, Message: "not implemented"}, nil
}

// MuteGroupMember implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) MuteGroupMember(ctx context.Context, req *pb.MuteGroupMemberRequest) (*pb.MuteGroupMemberResponse, error) {
	return &pb.MuteGroupMemberResponse{Code: 501, Message: "not implemented"}, nil
}

// TransferGroupOwner implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) TransferGroupOwner(ctx context.Context, req *pb.TransferGroupOwnerRequest) (*pb.TransferGroupOwnerResponse, error) {
	return &pb.TransferGroupOwnerResponse{Code: 501, Message: "not implemented"}, nil
}

// UpdateGroupAnnouncement implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) UpdateGroupAnnouncement(ctx context.Context, req *pb.UpdateGroupAnnouncementRequest) (*pb.UpdateGroupAnnouncementResponse, error) {
	return &pb.UpdateGroupAnnouncementResponse{Code: 501, Message: "not implemented"}, nil
}

// SetGroupAdmin implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) SetGroupAdmin(ctx context.Context, req *pb.SetGroupAdminRequest) (*pb.SetGroupAdminResponse, error) {
	return &pb.SetGroupAdminResponse{Code: 501, Message: "not implemented"}, nil
}

// GetGroupMembers implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) GetGroupMembers(ctx context.Context, req *pb.GetGroupMembersRequest) (*pb.GetGroupMembersResponse, error) {
	return &pb.GetGroupMembersResponse{Code: 501, Message: "not implemented"}, nil
}

// JoinGroup implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) JoinGroup(ctx context.Context, req *pb.JoinGroupRequest) (*pb.JoinGroupResponse, error) {
	return &pb.JoinGroupResponse{Code: 501, Message: "not implemented"}, nil
}

// LeaveGroup implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) LeaveGroup(ctx context.Context, req *pb.LeaveGroupRequest) (*pb.LeaveGroupResponse, error) {
	return &pb.LeaveGroupResponse{Code: 501, Message: "not implemented"}, nil
}

// GetGroup implements the ChatServiceImpl interface.
func (s *ChatServiceImpl) GetGroup(ctx context.Context, req *pb.GetGroupRequest) (*pb.GetGroupResponse, error) {
	return &pb.GetGroupResponse{Code: 501, Message: "not implemented"}, nil
}
