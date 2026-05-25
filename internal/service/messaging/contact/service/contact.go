package service

import (
	"Logos/internal/service/messaging/contact/dao"
	"Logos/internal/service/messaging/contact/model"
	"Logos/pkg/logger"
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"
)

func toInt64(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

type ContactService interface {
	SendFriendRequest(ctx context.Context, fromUserID, toUserID, remark, message string) (*model.FriendRequest, error)
	AcceptFriendRequest(ctx context.Context, requestID, toUserID string) error
	RejectFriendRequest(ctx context.Context, requestID, toUserID string) error
	GetPendingFriendRequests(ctx context.Context, toUserID string, page, pageSize int) ([]*model.FriendRequest, int64, error)

	GetFriendship(ctx context.Context, userID, friendID string) (*model.Friendship, error)
	GetFriends(ctx context.Context, userID, groupID string, page, pageSize int) ([]*model.Friendship, int64, error)
	DeleteFriend(ctx context.Context, userID, friendID string) error
	UpdateFriendRemark(ctx context.Context, userID, friendID, remark string) error

	BlockUser(ctx context.Context, userID, blockUserID string) error
	UnblockUser(ctx context.Context, userID, unblockUserID string) error
	GetBlacklist(ctx context.Context, userID string, page, pageSize int) ([]*model.Friendship, int64, error)

	CreateFriendGroup(ctx context.Context, userID, name string) (*model.FriendGroup, error)
	GetFriendGroups(ctx context.Context, userID string) ([]*model.FriendGroup, error)
	UpdateFriendGroup(ctx context.Context, userID, groupID, name string, sort int) error
	DeleteFriendGroup(ctx context.Context, userID, groupID string) error
	MoveFriendToGroup(ctx context.Context, userID, friendID, groupID string) error
}

type ContactServiceImpl struct {
	repo dao.ContactRepository
	ctx  context.Context
}

func NewContactService(repo dao.ContactRepository, ctx context.Context) ContactService {
	return &ContactServiceImpl{repo: repo, ctx: ctx}
}

func (s *ContactServiceImpl) SendFriendRequest(ctx context.Context, fromUserID, toUserID, remark, message string) (*model.FriendRequest, error) {
	logger.Info("\x1b[36m🔵 [Contact] 1️⃣ 开始处理好友申请\x1b[0m", logger.StringField("from_user", fromUserID), logger.StringField("to_user", toUserID))
	if fromUserID == toUserID {
		logger.Warn("\x1b[31m🔴 [Contact] 拒绝添加自己\x1b[0m", logger.StringField("user", fromUserID))
		return nil, fmt.Errorf("不能添加自己为好友")
	}
	if existingFriend, _ := s.repo.GetFriendship(ctx, fromUserID, toUserID); existingFriend != nil {
		logger.Warn("\x1b[33m🟡 [Contact] 已经是好友\x1b[0m", logger.StringField("from_user", fromUserID), logger.StringField("to_user", toUserID))
		return nil, fmt.Errorf("已经是好友")
	}
	request := &model.FriendRequest{
		ID:         uuid.NewString(),
		FromUserID: toInt64(fromUserID),
		ToUserID:   toInt64(toUserID),
		Remark:     remark,
		Message:    message,
		Status:     model.FriendRequestStatusPending,
	}
	if err := s.repo.CreateFriendRequest(ctx, request); err != nil {
		logger.Error("\x1b[31m🔴 [Contact] 创建好友申请失败\x1b[0m", logger.ErrorField(err))
		return nil, err
	}
	logger.Info("\x1b[32m🟢 [Contact] 2️⃣ 好友申请发送成功\x1b[0m", logger.StringField("from_user", fromUserID), logger.StringField("to_user", toUserID))
	return request, nil
}

func (s *ContactServiceImpl) AcceptFriendRequest(ctx context.Context, requestID, toUserID string) error {
	logger.Info("\x1b[36m🔵 [Contact] 1️⃣ 开始接受好友申请\x1b[0m", logger.StringField("request_id", requestID), logger.StringField("to_user", toUserID))
	request, err := s.repo.GetFriendRequestByID(ctx, requestID)
	if err != nil {
		logger.Error("\x1b[31m🔴 [Contact] 申请不存在\x1b[0m", logger.StringField("request_id", requestID))
		return fmt.Errorf("申请不存在")
	}
	if request.ToUserID != toInt64(toUserID) {
		logger.Warn("\x1b[31m🔴 [Contact] 无权处理此申请\x1b[0m", logger.StringField("request_id", requestID))
		return fmt.Errorf("无权处理此申请")
	}
	if request.Status != model.FriendRequestStatusPending {
		logger.Warn("\x1b[33m🟡 [Contact] 申请已处理\x1b[0m", logger.StringField("request_id", requestID))
		return fmt.Errorf("申请已处理")
	}
	logger.Info("\x1b[36m🔵 [Contact] 2️⃣ 开始创建好友关系\x1b[0m", logger.StringField("user1", toUserID), logger.StringField("user2", strconv.FormatInt(request.FromUserID, 10)))
	friendship1 := &model.Friendship{
		ID:       uuid.NewString(),
		UserID:   toInt64(toUserID),
		FriendID: request.FromUserID,
		Status:   model.FriendshipStatusAccepted,
	}
	friendship2 := &model.Friendship{
		ID:       uuid.NewString(),
		UserID:   request.FromUserID,
		FriendID: toInt64(toUserID),
		Status:   model.FriendshipStatusAccepted,
	}
	if err := s.repo.CreateFriendship(ctx, friendship1); err != nil {
		logger.Error("\x1b[31m🔴 [Contact] 创建好友关系1失败\x1b[0m", logger.ErrorField(err))
		return err
	}
	if err := s.repo.CreateFriendship(ctx, friendship2); err != nil {
		logger.Error("\x1b[31m🔴 [Contact] 创建好友关系2失败\x1b[0m", logger.ErrorField(err))
		return err
	}
	logger.Info("\x1b[36m🔵 [Contact] 3️⃣ 更新申请状态为accepted\x1b[0m")
	if err := s.repo.UpdateFriendRequestStatus(ctx, requestID, model.FriendRequestStatusAccepted); err != nil {
		logger.Error("\x1b[31m🔴 [Contact] 更新申请状态失败\x1b[0m", logger.ErrorField(err))
		return err
	}
	logger.Info("\x1b[32m🟢 [Contact] ✅ 好友关系建立成功\x1b[0m", logger.StringField("user1", toUserID), logger.StringField("user2", strconv.FormatInt(request.FromUserID, 10)))
	return nil
}

func (s *ContactServiceImpl) RejectFriendRequest(ctx context.Context, requestID, toUserID string) error {
	request, err := s.repo.GetFriendRequestByID(ctx, requestID)
	if err != nil {
		return fmt.Errorf("申请不存在")
	}
	if request.ToUserID != toInt64(toUserID) {
		return fmt.Errorf("无权处理此申请")
	}
	if err := s.repo.UpdateFriendRequestStatus(ctx, requestID, model.FriendRequestStatusRejected); err != nil {
		logger.Error("拒绝好友申请失败", logger.ErrorField(err))
		return err
	}
	logger.Info("拒绝好友申请", logger.StringField("from_user", strconv.FormatInt(request.FromUserID, 10)), logger.StringField("to_user", toUserID))
	return nil
}

func (s *ContactServiceImpl) GetPendingFriendRequests(ctx context.Context, toUserID string, page, pageSize int) ([]*model.FriendRequest, int64, error) {
	return s.repo.GetPendingFriendRequests(ctx, toUserID, page, pageSize)
}

func (s *ContactServiceImpl) GetFriendship(ctx context.Context, userID, friendID string) (*model.Friendship, error) {
	return s.repo.GetFriendship(ctx, userID, friendID)
}

func (s *ContactServiceImpl) GetFriends(ctx context.Context, userID, groupID string, page, pageSize int) ([]*model.Friendship, int64, error) {
	return s.repo.GetFriends(ctx, userID, groupID, page, pageSize)
}

func (s *ContactServiceImpl) DeleteFriend(ctx context.Context, userID, friendID string) error {
	if err := s.repo.DeleteFriendship(ctx, userID, friendID); err != nil {
		logger.Error("删除好友失败", logger.ErrorField(err))
		return err
	}
	if err := s.repo.DeleteFriendship(ctx, friendID, userID); err != nil {
		logger.Warn("删除反向好友失败", logger.ErrorField(err))
	}
	logger.Info("删除好友", logger.StringField("user_id", userID), logger.StringField("friend_id", friendID))
	return nil
}

func (s *ContactServiceImpl) UpdateFriendRemark(ctx context.Context, userID, friendID, remark string) error {
	if err := s.repo.UpdateFriendRemark(ctx, userID, friendID, remark); err != nil {
		logger.Error("更新备注失败", logger.ErrorField(err))
		return err
	}
	return nil
}

func (s *ContactServiceImpl) BlockUser(ctx context.Context, userID, blockUserID string) error {
	if userID == blockUserID {
		return fmt.Errorf("不能拉黑自己")
	}
	if err := s.repo.BlockUser(ctx, userID, blockUserID); err != nil {
		logger.Error("拉黑失败", logger.ErrorField(err))
		return err
	}
	logger.Info("拉黑用户", logger.StringField("user_id", userID), logger.StringField("block_user_id", blockUserID))
	return nil
}

func (s *ContactServiceImpl) UnblockUser(ctx context.Context, userID, unblockUserID string) error {
	if err := s.repo.UnblockUser(ctx, userID, unblockUserID); err != nil {
		logger.Error("取消拉黑失败", logger.ErrorField(err))
		return err
	}
	logger.Info("取消拉黑用户", logger.StringField("user_id", userID), logger.StringField("unblock_user_id", unblockUserID))
	return nil
}

func (s *ContactServiceImpl) GetBlacklist(ctx context.Context, userID string, page, pageSize int) ([]*model.Friendship, int64, error) {
	return s.repo.GetBlacklist(ctx, userID, page, pageSize)
}

func (s *ContactServiceImpl) CreateFriendGroup(ctx context.Context, userID, name string) (*model.FriendGroup, error) {
	group := &model.FriendGroup{
		ID:     uuid.NewString(),
		UserID: toInt64(userID),
		Name:   name,
	}
	if err := s.repo.CreateFriendGroup(ctx, group); err != nil {
		logger.Error("创建分组失败", logger.ErrorField(err))
		return nil, err
	}
	logger.Info("创建分组", logger.StringField("user_id", userID), logger.StringField("name", name))
	return group, nil
}

func (s *ContactServiceImpl) GetFriendGroups(ctx context.Context, userID string) ([]*model.FriendGroup, error) {
	return s.repo.GetFriendGroups(ctx, userID)
}

func (s *ContactServiceImpl) UpdateFriendGroup(ctx context.Context, userID, groupID, name string, sort int) error {
	if err := s.repo.UpdateFriendGroup(ctx, userID, groupID, name, sort); err != nil {
		logger.Error("更新分组失败", logger.ErrorField(err))
		return err
	}
	return nil
}

func (s *ContactServiceImpl) DeleteFriendGroup(ctx context.Context, userID, groupID string) error {
	if err := s.repo.DeleteFriendGroup(ctx, userID, groupID); err != nil {
		logger.Error("删除分组失败", logger.ErrorField(err))
		return err
	}
	return nil
}

func (s *ContactServiceImpl) MoveFriendToGroup(ctx context.Context, userID, friendID, groupID string) error {
	if err := s.repo.MoveFriendToGroup(ctx, userID, friendID, groupID); err != nil {
		logger.Error("移动分组失败", logger.ErrorField(err))
		return err
	}
	return nil
}
