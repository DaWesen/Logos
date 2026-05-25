package handler

import (
	"context"
	"strconv"

	"Logos/internal/service/messaging/contact/model"
	"Logos/internal/service/messaging/contact/service"
	"Logos/pkg/auth"
	"Logos/pkg/logger"
	pb "Logos/proto_gen/contact"

	"google.golang.org/protobuf/types/known/timestamppb"
)

type ContactServiceImpl struct {
	pb.UnimplementedContactServiceServer
	service service.ContactService
}

func NewContactServiceImpl(service service.ContactService) *ContactServiceImpl {
	return &ContactServiceImpl{service: service}
}

func (s *ContactServiceImpl) AddFriend(ctx context.Context, req *pb.AddFriendRequest) (*pb.AddFriendResponse, error) {
	userID := auth.MustGetUserID(ctx)
	_, err := s.service.SendFriendRequest(ctx, userID, req.UserId, req.Remark, req.Message)
	if err != nil {
		logger.Error("发送好友申请失败", logger.ErrorField(err))
		return &pb.AddFriendResponse{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	return &pb.AddFriendResponse{
		Code:    200,
		Message: "发送成功",
	}, nil
}

func (s *ContactServiceImpl) HandleFriendRequest(ctx context.Context, req *pb.HandleFriendRequestRequest) (*pb.HandleFriendRequestResponse, error) {
	userID := auth.MustGetUserID(ctx)

	switch req.Status {
	case pb.FriendRequestStatus_FRIEND_REQUEST_STATUS_ACCEPTED:
		if err := s.service.AcceptFriendRequest(ctx, req.RequestId, userID); err != nil {
			logger.Error("接受好友申请失败", logger.ErrorField(err))
			return &pb.HandleFriendRequestResponse{
				Code:    500,
				Message: err.Error(),
			}, nil
		}
	case pb.FriendRequestStatus_FRIEND_REQUEST_STATUS_REJECTED:
		if err := s.service.RejectFriendRequest(ctx, req.RequestId, userID); err != nil {
			logger.Error("拒绝好友申请失败", logger.ErrorField(err))
			return &pb.HandleFriendRequestResponse{
				Code:    500,
				Message: err.Error(),
			}, nil
		}
	}
	return &pb.HandleFriendRequestResponse{
		Code:    200,
		Message: "处理成功",
	}, nil
}

func (s *ContactServiceImpl) GetFriendRequests(ctx context.Context, req *pb.GetFriendRequestsRequest) (*pb.GetFriendRequestsResponse, error) {
	userID := auth.MustGetUserID(ctx)
	page := int(req.Page)
	if page <= 0 {
		page = 1
	}
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = 20
	}

	requests, total, err := s.service.GetPendingFriendRequests(ctx, userID, page, pageSize)
	if err != nil {
		logger.Error("获取好友申请失败", logger.ErrorField(err))
		return &pb.GetFriendRequestsResponse{
			Code:    500,
			Message: err.Error(),
		}, nil
	}

	respRequests := make([]*pb.FriendRequest, 0, len(requests))
	for _, reqItem := range requests {
		respRequests = append(respRequests, &pb.FriendRequest{
			Id:         reqItem.ID,
			FromUserId: strconv.FormatInt(reqItem.FromUserID, 10),
			ToUserId:   strconv.FormatInt(reqItem.ToUserID, 10),
			Remark:     reqItem.Remark,
			Message:    reqItem.Message,
			Status:     friendRequestStatusToProto(reqItem.Status),
			CreatedAt:  timestamppb.New(reqItem.CreatedAt),
			UpdatedAt:  timestamppb.New(reqItem.UpdatedAt),
		})
	}
	return &pb.GetFriendRequestsResponse{
		Code:     200,
		Message:  "获取成功",
		Requests: respRequests,
		Total:    int32(total),
	}, nil
}

func (s *ContactServiceImpl) DeleteFriend(ctx context.Context, req *pb.DeleteFriendRequest) (*pb.DeleteFriendResponse, error) {
	userID := auth.MustGetUserID(ctx)
	if err := s.service.DeleteFriend(ctx, userID, req.FriendId); err != nil {
		logger.Error("删除好友失败", logger.ErrorField(err))
		return &pb.DeleteFriendResponse{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	return &pb.DeleteFriendResponse{
		Code:    200,
		Message: "删除成功",
	}, nil
}

func (s *ContactServiceImpl) UpdateFriendRemark(ctx context.Context, req *pb.UpdateFriendRemarkRequest) (*pb.UpdateFriendRemarkResponse, error) {
	userID := auth.MustGetUserID(ctx)
	if err := s.service.UpdateFriendRemark(ctx, userID, req.FriendId, req.Remark); err != nil {
		logger.Error("更新备注失败", logger.ErrorField(err))
		return &pb.UpdateFriendRemarkResponse{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	return &pb.UpdateFriendRemarkResponse{
		Code:    200,
		Message: "更新成功",
	}, nil
}

func (s *ContactServiceImpl) GetFriendList(ctx context.Context, req *pb.GetFriendListRequest) (*pb.GetFriendListResponse, error) {
	userID := auth.MustGetUserID(ctx)
	page := int(req.Page)
	if page <= 0 {
		page = 1
	}
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = 50
	}

	friends, total, err := s.service.GetFriends(ctx, userID, req.GroupId, page, pageSize)
	if err != nil {
		logger.Error("获取好友列表失败", logger.ErrorField(err))
		return &pb.GetFriendListResponse{
			Code:    500,
			Message: err.Error(),
		}, nil
	}

	respFriends := make([]*pb.Friendship, 0, len(friends))
	for _, friend := range friends {
		respFriends = append(respFriends, &pb.Friendship{
			Id:        friend.ID,
			UserId:    strconv.FormatInt(friend.UserID, 10),
			FriendId:  strconv.FormatInt(friend.FriendID, 10),
			Remark:    friend.Remark,
			GroupId:   friend.GroupID,
			CreatedAt: timestamppb.New(friend.CreatedAt),
			UpdatedAt: timestamppb.New(friend.UpdatedAt),
		})
	}
	return &pb.GetFriendListResponse{
		Code:    200,
		Message: "获取成功",
		Friends: respFriends,
		Total:   int32(total),
	}, nil
}

func (s *ContactServiceImpl) CreateFriendGroup(ctx context.Context, req *pb.CreateFriendGroupRequest) (*pb.CreateFriendGroupResponse, error) {
	userID := auth.MustGetUserID(ctx)
	group, err := s.service.CreateFriendGroup(ctx, userID, req.Name)
	if err != nil {
		logger.Error("创建分组失败", logger.ErrorField(err))
		return &pb.CreateFriendGroupResponse{
			Code:    500,
			Message: err.Error(),
		}, nil
	}

	respGroup := &pb.FriendGroup{
		Id:        group.ID,
		UserId:    strconv.FormatInt(group.UserID, 10),
		Name:      group.Name,
		SortOrder: int32(group.Sort),
		CreatedAt: timestamppb.New(group.CreatedAt),
		UpdatedAt: timestamppb.New(group.UpdatedAt),
	}

	return &pb.CreateFriendGroupResponse{
		Code:    200,
		Message: "创建成功",
		Data:    respGroup,
	}, nil
}

func (s *ContactServiceImpl) DeleteFriendGroup(ctx context.Context, req *pb.DeleteFriendGroupRequest) (*pb.DeleteFriendGroupResponse, error) {
	userID := auth.MustGetUserID(ctx)
	if err := s.service.DeleteFriendGroup(ctx, userID, req.GroupId); err != nil {
		logger.Error("删除分组失败", logger.ErrorField(err))
		return &pb.DeleteFriendGroupResponse{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	return &pb.DeleteFriendGroupResponse{
		Code:    200,
		Message: "删除成功",
	}, nil
}

func (s *ContactServiceImpl) UpdateFriendGroup(ctx context.Context, req *pb.UpdateFriendGroupRequest) (*pb.UpdateFriendGroupResponse, error) {
	userID := auth.MustGetUserID(ctx)
	if err := s.service.UpdateFriendGroup(ctx, userID, req.GroupId, req.Name, int(req.SortOrder)); err != nil {
		logger.Error("更新分组失败", logger.ErrorField(err))
		return &pb.UpdateFriendGroupResponse{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	return &pb.UpdateFriendGroupResponse{
		Code:    200,
		Message: "更新成功",
	}, nil
}

func (s *ContactServiceImpl) GetFriendGroups(ctx context.Context, req *pb.GetFriendGroupsRequest) (*pb.GetFriendGroupsResponse, error) {
	userID := auth.MustGetUserID(ctx)
	groups, err := s.service.GetFriendGroups(ctx, userID)
	if err != nil {
		logger.Error("获取分组失败", logger.ErrorField(err))
		return &pb.GetFriendGroupsResponse{
			Code:    500,
			Message: err.Error(),
		}, nil
	}

	respGroups := make([]*pb.FriendGroup, 0, len(groups))
	for _, group := range groups {
		respGroups = append(respGroups, &pb.FriendGroup{
			Id:        group.ID,
			UserId:    strconv.FormatInt(group.UserID, 10),
			Name:      group.Name,
			SortOrder: int32(group.Sort),
			CreatedAt: timestamppb.New(group.CreatedAt),
			UpdatedAt: timestamppb.New(group.UpdatedAt),
		})
	}
	return &pb.GetFriendGroupsResponse{
		Code:    200,
		Message: "获取成功",
		Groups:  respGroups,
	}, nil
}

func (s *ContactServiceImpl) MoveFriendToGroup(ctx context.Context, req *pb.MoveFriendToGroupRequest) (*pb.MoveFriendToGroupResponse, error) {
	userID := auth.MustGetUserID(ctx)
	if err := s.service.MoveFriendToGroup(ctx, userID, req.FriendId, req.GroupId); err != nil {
		logger.Error("移动分组失败", logger.ErrorField(err))
		return &pb.MoveFriendToGroupResponse{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	return &pb.MoveFriendToGroupResponse{
		Code:    200,
		Message: "移动成功",
	}, nil
}

func (s *ContactServiceImpl) BlockUser(ctx context.Context, req *pb.BlockUserRequest) (*pb.BlockUserResponse, error) {
	userID := auth.MustGetUserID(ctx)
	if err := s.service.BlockUser(ctx, userID, req.UserId); err != nil {
		logger.Error("拉黑失败", logger.ErrorField(err))
		return &pb.BlockUserResponse{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	return &pb.BlockUserResponse{
		Code:    200,
		Message: "拉黑成功",
	}, nil
}

func (s *ContactServiceImpl) UnblockUser(ctx context.Context, req *pb.UnblockUserRequest) (*pb.UnblockUserResponse, error) {
	userID := auth.MustGetUserID(ctx)
	if err := s.service.UnblockUser(ctx, userID, req.UserId); err != nil {
		logger.Error("取消拉黑失败", logger.ErrorField(err))
		return &pb.UnblockUserResponse{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	return &pb.UnblockUserResponse{
		Code:    200,
		Message: "取消拉黑成功",
	}, nil
}

func (s *ContactServiceImpl) GetBlacklist(ctx context.Context, req *pb.GetBlacklistRequest) (*pb.GetBlacklistResponse, error) {
	userID := auth.MustGetUserID(ctx)
	records, total, err := s.service.GetBlacklist(ctx, userID, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error("获取黑名单失败", logger.ErrorField(err))
		return &pb.GetBlacklistResponse{
			Code:    500,
			Message: err.Error(),
		}, nil
	}

	pbRecords := make([]*pb.BlacklistRecord, 0, len(records))
	for _, r := range records {
		pbRecords = append(pbRecords, &pb.BlacklistRecord{
			Id:            r.ID,
			UserId:        strconv.FormatInt(r.UserID, 10),
			BlockedUserId: strconv.FormatInt(r.FriendID, 10),
			CreatedAt:     timestamppb.New(r.CreatedAt),
		})
	}

	return &pb.GetBlacklistResponse{
		Code:    200,
		Message: "success",
		Records: pbRecords,
		Total:   int32(total),
	}, nil
}

func (s *ContactServiceImpl) CheckFriendship(ctx context.Context, req *pb.CheckFriendshipRequest) (*pb.CheckFriendshipResponse, error) {
	logger.Info("\x1b[36m🔵 [Contact] 检查好友关系\x1b[0m",
		logger.StringField("user_id", req.UserId),
		logger.StringField("friend_id", req.FriendId))

	friendship, err := s.service.GetFriendship(ctx, req.UserId, req.FriendId)
	if err != nil {
		logger.Warn("\x1b[33m🟡 [Contact] 好友关系记录不存在\x1b[0m", logger.ErrorField(err))
		return &pb.CheckFriendshipResponse{
			Code:      200,
			Message:   "查询成功",
			IsFriend:  false,
			IsBlocked: false,
		}, nil
	}

	isFriend := friendship.Status == model.FriendshipStatusAccepted
	isBlocked := friendship.Status == model.FriendshipStatusBlocked

	logger.Info("\x1b[32m🟢 [Contact] 检查好友关系完成\x1b[0m",
		logger.BoolField("is_friend", isFriend),
		logger.BoolField("is_blocked", isBlocked),
		logger.StringField("status", string(friendship.Status)))

	return &pb.CheckFriendshipResponse{
		Code:      200,
		Message:   "查询成功",
		IsFriend:  isFriend,
		IsBlocked: isBlocked,
	}, nil
}

func friendRequestStatusToProto(status model.FriendRequestStatus) pb.FriendRequestStatus {
	switch status {
	case model.FriendRequestStatusPending:
		return pb.FriendRequestStatus_FRIEND_REQUEST_STATUS_PENDING
	case model.FriendRequestStatusAccepted:
		return pb.FriendRequestStatus_FRIEND_REQUEST_STATUS_ACCEPTED
	case model.FriendRequestStatusRejected:
		return pb.FriendRequestStatus_FRIEND_REQUEST_STATUS_REJECTED
	default:
		return pb.FriendRequestStatus_FRIEND_REQUEST_STATUS_UNSPECIFIED
	}
}
