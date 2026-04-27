package handler

import (
	"context"

	pb "Logos/proto_gen/contact"
)

// ContactServiceImpl Contact 服务 Handler
// TODO: 实现 ContactService 接口
type ContactServiceImpl struct {
	pb.UnimplementedContactServiceServer
}

// AddFriend implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) AddFriend(ctx context.Context, req *pb.AddFriendRequest) (*pb.AddFriendResponse, error) {
	return &pb.AddFriendResponse{Code: 501, Message: "not implemented"}, nil
}

// HandleFriendRequest implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) HandleFriendRequest(ctx context.Context, req *pb.HandleFriendRequestRequest) (*pb.HandleFriendRequestResponse, error) {
	return &pb.HandleFriendRequestResponse{Code: 501, Message: "not implemented"}, nil
}

// GetFriendRequests implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) GetFriendRequests(ctx context.Context, req *pb.GetFriendRequestsRequest) (*pb.GetFriendRequestsResponse, error) {
	return &pb.GetFriendRequestsResponse{Code: 501, Message: "not implemented"}, nil
}

// DeleteFriend implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) DeleteFriend(ctx context.Context, req *pb.DeleteFriendRequest) (*pb.DeleteFriendResponse, error) {
	return &pb.DeleteFriendResponse{Code: 501, Message: "not implemented"}, nil
}

// UpdateFriendRemark implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) UpdateFriendRemark(ctx context.Context, req *pb.UpdateFriendRemarkRequest) (*pb.UpdateFriendRemarkResponse, error) {
	return &pb.UpdateFriendRemarkResponse{Code: 501, Message: "not implemented"}, nil
}

// GetFriendList implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) GetFriendList(ctx context.Context, req *pb.GetFriendListRequest) (*pb.GetFriendListResponse, error) {
	return &pb.GetFriendListResponse{Code: 501, Message: "not implemented"}, nil
}

// CreateFriendGroup implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) CreateFriendGroup(ctx context.Context, req *pb.CreateFriendGroupRequest) (*pb.CreateFriendGroupResponse, error) {
	return &pb.CreateFriendGroupResponse{Code: 501, Message: "not implemented"}, nil
}

// DeleteFriendGroup implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) DeleteFriendGroup(ctx context.Context, req *pb.DeleteFriendGroupRequest) (*pb.DeleteFriendGroupResponse, error) {
	return &pb.DeleteFriendGroupResponse{Code: 501, Message: "not implemented"}, nil
}

// UpdateFriendGroup implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) UpdateFriendGroup(ctx context.Context, req *pb.UpdateFriendGroupRequest) (*pb.UpdateFriendGroupResponse, error) {
	return &pb.UpdateFriendGroupResponse{Code: 501, Message: "not implemented"}, nil
}

// GetFriendGroups implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) GetFriendGroups(ctx context.Context, req *pb.GetFriendGroupsRequest) (*pb.GetFriendGroupsResponse, error) {
	return &pb.GetFriendGroupsResponse{Code: 501, Message: "not implemented"}, nil
}

// MoveFriendToGroup implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) MoveFriendToGroup(ctx context.Context, req *pb.MoveFriendToGroupRequest) (*pb.MoveFriendToGroupResponse, error) {
	return &pb.MoveFriendToGroupResponse{Code: 501, Message: "not implemented"}, nil
}

// BlockUser implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) BlockUser(ctx context.Context, req *pb.BlockUserRequest) (*pb.BlockUserResponse, error) {
	return &pb.BlockUserResponse{Code: 501, Message: "not implemented"}, nil
}

// UnblockUser implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) UnblockUser(ctx context.Context, req *pb.UnblockUserRequest) (*pb.UnblockUserResponse, error) {
	return &pb.UnblockUserResponse{Code: 501, Message: "not implemented"}, nil
}

// GetBlacklist implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) GetBlacklist(ctx context.Context, req *pb.GetBlacklistRequest) (*pb.GetBlacklistResponse, error) {
	return &pb.GetBlacklistResponse{Code: 501, Message: "not implemented"}, nil
}
