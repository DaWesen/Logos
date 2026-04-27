package handler

import (
	"context"

	pb "Logos/proto_gen/im"
	pbCommon "Logos/proto_gen/common"
)

// IMServiceImpl IM 服务 Handler
// TODO: 实现 IMService 接口
type IMServiceImpl struct {
	pb.UnimplementedIMServiceServer
}

// Connect implements the IMServiceImpl interface.
func (s *IMServiceImpl) Connect(ctx context.Context, req *pb.ConnectReq) (*pbCommon.BaseResp, error) {
	return &pbCommon.BaseResp{StatusCode: 501, StatusMessage: "not implemented"}, nil
}

// Disconnect implements the IMServiceImpl interface.
func (s *IMServiceImpl) Disconnect(ctx context.Context, req *pb.ConnectReq) (*pbCommon.BaseResp, error) {
	return &pbCommon.BaseResp{StatusCode: 501, StatusMessage: "not implemented"}, nil
}

// GetOnlineStatus implements the IMServiceImpl interface.
func (s *IMServiceImpl) GetOnlineStatus(ctx context.Context, req *pb.GetUserStatusReq) (*pb.OnlineStatusResp, error) {
	return &pb.OnlineStatusResp{BaseResp: &pbCommon.BaseResp{StatusCode: 501, StatusMessage: "not implemented"}}, nil
}

// Heartbeat implements the IMServiceImpl interface.
func (s *IMServiceImpl) Heartbeat(ctx context.Context, req *pb.HeartbeatReq) (*pbCommon.BaseResp, error) {
	return &pbCommon.BaseResp{StatusCode: 501, StatusMessage: "not implemented"}, nil
}
