package service

import (
	"Logos/internal/service/messaging/contact/dao"
	"Logos/internal/service/messaging/contact/model"
	"Logos/pkg/logger"
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type ContactService interface {
	SendFriendRequest(ctx context.Context, fromUserID, toUserID, remark, message string) (*model.FriendRequest, error)
	AcceptFriendRequest(ctx context.Context, requestID, toUserID string) error
	RejectFriendRequest(ctx context.Context, requestID, toUserID string) error
	GetPendingFriendRequests(ctx context.Context, toUserID string, page, pageSize int) ([]*model.FriendRequest, int64, error)

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
	if fromUserID == toUserID {
		return nil, fmt.Errorf("不能添加自己为好友")
	}
	if existingFriend, _ := s.repo.GetFriendship(ctx, fromUserID, toUserID); existingFriend != nil {
		return nil, fmt.Errorf("已经是好友")
	}
	request := &model.FriendRequest{
		ID:         uuid.NewString(),
		FromUserID: fromUserID,
		ToUserID:   toUserID,
		Remark:     remark,
		Message:    message,
		Status:     model.FriendRequestStatusPending,
	}
	if err := s.repo.CreateFriendRequest(ctx, request); err != nil {
		logger.Error("创建好友申请失败", logger.ErrorField(err))
		return nil, err
	}
	logger.Info("发送好友申请", logger.StringField("from_user", fromUserID), logger.StringField("to_user", toUserID))
	return request, nil
}

func (s *ContactServiceImpl) AcceptFriendRequest(ctx context.Context, requestID, toUserID string) error {
	request, err := s.repo.GetFriendRequestByID(ctx, requestID)
	if err != nil {
		return fmt.Errorf("申请不存在")
	}
	if request.ToUserID != toUserID {
		return fmt.Errorf("无权处理此申请")
	}
	if request.Status != model.FriendRequestStatusPending {
		return fmt.Errorf("申请已处理")
	}
	now := time.Now().UnixMilli()
	friendship1 := &model.Friendship{
		ID:        uuid.NewString(),
		UserID:    toUserID,
		FriendID:  request.FromUserID,
		Status:    model.FriendshipStatusAccepted,
		CreatedAt: now,
		UpdatedAt: now,
	}
	friendship2 := &model.Friendship{
		ID:        uuid.NewString(),
		UserID:    request.FromUserID,
		FriendID:  toUserID,
		Status:    model.FriendshipStatusAccepted,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repo.CreateFriendship(ctx, friendship1); err != nil {
		logger.Error("创建好友关系失败", logger.ErrorField(err))
		return err
	}
	if err := s.repo.CreateFriendship(ctx, friendship2); err != nil {
		logger.Error("创建好友关系失败", logger.ErrorField(err))
		return err
	}
	if err := s.repo.UpdateFriendRequestStatus(ctx, requestID, model.FriendRequestStatusAccepted); err != nil {
		logger.Error("更新申请状态失败", logger.ErrorField(err))
		return err
	}
	logger.Info("接受好友申请", logger.StringField("from_user", request.FromUserID), logger.StringField("to_user", toUserID))
	return nil
}

func (s *ContactServiceImpl) RejectFriendRequest(ctx context.Context, requestID, toUserID string) error {
	request, err := s.repo.GetFriendRequestByID(ctx, requestID)
	if err != nil {
		return fmt.Errorf("申请不存在")
	}
	if request.ToUserID != toUserID {
		return fmt.Errorf("无权处理此申请")
	}
	if err := s.repo.UpdateFriendRequestStatus(ctx, requestID, model.FriendRequestStatusRejected); err != nil {
		logger.Error("拒绝好友申请失败", logger.ErrorField(err))
		return err
	}
	logger.Info("拒绝好友申请", logger.StringField("from_user", request.FromUserID), logger.StringField("to_user", toUserID))
	return nil
}

func (s *ContactServiceImpl) GetPendingFriendRequests(ctx context.Context, toUserID string, page, pageSize int) ([]*model.FriendRequest, int64, error) {
	return s.repo.GetPendingFriendRequests(ctx, toUserID, page, pageSize)
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
		UserID: userID,
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
