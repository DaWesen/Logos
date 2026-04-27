package handler

import (
	"context"

	pb "Logos/proto_gen/contact"
	pbCommon "Logos/proto_gen/common"
)

// ContactServiceImpl Contact 服务 Handler
// TODO: 实现 ContactService 接口
type ContactServiceImpl struct {
	pb.UnimplementedContactServiceServer
}

// AddFriend implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) AddFriend(ctx context.Context, req *pb.AddFriendReq) (*pb.AddFriendResp, error) {
	return &pb.AddFriendResp{BaseResp: &pbCommon.BaseResp{StatusCode: 501, StatusMessage: "not implemented"}}, nil
}

// HandleFriendRequest implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) HandleFriendRequest(ctx context.Context, req *pb.HandleFriendReq) (*pbCommon.BaseResp, error) {
	return &pbCommon.BaseResp{StatusCode: 501, StatusMessage: "not implemented"}, nil
}

// RemoveFriend implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) RemoveFriend(ctx context.Context, req *pb.RemoveFriendReq) (*pbCommon.BaseResp, error) {
	return &pbCommon.BaseResp{StatusCode: 501, StatusMessage: "not implemented"}, nil
}

// GetFriendList implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) GetFriendList(ctx context.Context, req *pb.GetByUserIdReq) (*pb.FriendListResp, error) {
	return &pb.FriendListResp{BaseResp: &pbCommon.BaseResp{StatusCode: 501, StatusMessage: "not implemented"}}, nil
}

// GetPendingRequests implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) GetPendingRequests(ctx context.Context, req *pb.GetByUserIdReq) (*pb.FriendListResp, error) {
	return &pb.FriendListResp{BaseResp: &pbCommon.BaseResp{StatusCode: 501, StatusMessage: "not implemented"}}, nil
}

// BlockUser implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) BlockUser(ctx context.Context, req *pb.BlockUserReq) (*pbCommon.BaseResp, error) {
	return &pbCommon.BaseResp{StatusCode: 501, StatusMessage: "not implemented"}, nil
}

// UnblockUser implements the ContactServiceImpl interface.
func (s *ContactServiceImpl) UnblockUser(ctx context.Context, req *pb.BlockUserReq) (*pbCommon.BaseResp, error) {
	return &pbCommon.BaseResp{StatusCode: 501, StatusMessage: "not implemented"}, nil
}
