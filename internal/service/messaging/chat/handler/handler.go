package handler

import (
	"context"
	"time"

	"Logos/internal/service/messaging/chat/model"
	"Logos/internal/service/messaging/chat/service"
	"Logos/pkg/auth"
	"Logos/pkg/logger"
	pb "Logos/proto_gen/chat"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func getUserID(ctx context.Context) (string, error) {
	return auth.GetUserID(ctx)
}

type ChatServiceImpl struct {
	pb.UnimplementedChatServiceServer
	service service.ChatService
}

func NewChatServiceImpl(service service.ChatService) *ChatServiceImpl {
	return &ChatServiceImpl{service: service}
}

func (s *ChatServiceImpl) SendMessage(ctx context.Context, req *pb.SendMessageRequest) (*pb.SendMessageResponse, error) {
	msgType := model.MessageType(req.MessageType)
	chatType := model.ChatType(req.ChatType)

	senderID, err := getUserID(ctx)
	if err != nil {
		return &pb.SendMessageResponse{Code: 401, Message: "未提供认证信息"}, nil
	}

	msg, _, err := s.service.SendMessage(
		senderID, req.ChatId, chatType, msgType, req.Content, req.Metadata, req.ReplyToMessageId, req.MentionUserIds,
	)
	if err != nil {
		logger.Error("发送消息失败", logger.ErrorField(err))
		return &pb.SendMessageResponse{Code: 500, Message: err.Error()}, nil
	}

	return &pb.SendMessageResponse{
		Code: 200, Message: "发送成功",
		Data: &pb.Message{
			Id: msg.ID, ChatId: msg.ChatID, ChatType: pb.ChatType(msg.ChatType),
			SenderId: msg.SenderID, MessageType: pb.MessageType(msg.MessageType),
			Content: msg.Content, Metadata: msg.Metadata, Status: pb.MessageStatus(msg.Status),
			CreatedAt: timestamppb.New(msg.CreatedAt), UpdatedAt: timestamppb.New(msg.UpdatedAt),
			ReplyToMessageId: msg.ReplyToMessage,
		},
	}, nil
}

func (s *ChatServiceImpl) GetMessageHistory(ctx context.Context, req *pb.GetMessageHistoryRequest) (*pb.GetMessageHistoryResponse, error) {
	var beforeTime time.Time
	if req.BeforeTime != nil {
		beforeTime = req.BeforeTime.AsTime()
	}

	messages, hasMore, err := s.service.GetMessageHistory(req.ChatId, model.ChatType(req.ChatType), beforeTime, int(req.Limit))
	if err != nil {
		logger.Error("获取消息历史失败", logger.ErrorField(err))
		return &pb.GetMessageHistoryResponse{Code: 500, Message: err.Error()}, nil
	}

	pbMessages := make([]*pb.Message, 0, len(messages))
	for _, msg := range messages {
		pbMessages = append(pbMessages, &pb.Message{
			Id: msg.ID, ChatId: msg.ChatID, ChatType: pb.ChatType(msg.ChatType),
			SenderId: msg.SenderID, MessageType: pb.MessageType(msg.MessageType),
			Content: msg.Content, Metadata: msg.Metadata, Status: pb.MessageStatus(msg.Status),
			CreatedAt: timestamppb.New(msg.CreatedAt), UpdatedAt: timestamppb.New(msg.UpdatedAt),
			ReplyToMessageId: msg.ReplyToMessage,
		})
	}

	return &pb.GetMessageHistoryResponse{Code: 200, Message: "获取成功", Messages: pbMessages, HasMore: hasMore}, nil
}

func (s *ChatServiceImpl) SearchMessages(ctx context.Context, req *pb.SearchMessagesRequest) (*pb.SearchMessagesResponse, error) {
	var startTime, endTime time.Time
	if req.StartTime != nil {
		startTime = req.StartTime.AsTime()
	}
	if req.EndTime != nil {
		endTime = req.EndTime.AsTime()
	}

	messages, total, err := s.service.SearchMessages(req.ChatId, model.ChatType(req.ChatType), req.Keyword, startTime, endTime, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error("搜索消息失败", logger.ErrorField(err))
		return &pb.SearchMessagesResponse{Code: 500, Message: err.Error()}, nil
	}

	pbMessages := make([]*pb.Message, 0, len(messages))
	for _, msg := range messages {
		pbMessages = append(pbMessages, &pb.Message{
			Id: msg.ID, ChatId: msg.ChatID, ChatType: pb.ChatType(msg.ChatType),
			SenderId: msg.SenderID, MessageType: pb.MessageType(msg.MessageType),
			Content: msg.Content, Metadata: msg.Metadata, Status: pb.MessageStatus(msg.Status),
			CreatedAt: timestamppb.New(msg.CreatedAt), UpdatedAt: timestamppb.New(msg.UpdatedAt),
			ReplyToMessageId: msg.ReplyToMessage,
		})
	}

	return &pb.SearchMessagesResponse{Code: 200, Message: "搜索成功", Messages: pbMessages, Total: int32(total)}, nil
}

func (s *ChatServiceImpl) MarkMessagesRead(ctx context.Context, req *pb.MarkMessagesReadRequest) (*pb.MarkMessagesReadResponse, error) {
	if err := s.service.MarkMessagesRead(req.MessageIds); err != nil {
		logger.Error("标记消息已读失败", logger.ErrorField(err))
		return &pb.MarkMessagesReadResponse{Code: 500, Message: err.Error()}, nil
	}
	return &pb.MarkMessagesReadResponse{Code: 200, Message: "标记成功"}, nil
}

func (s *ChatServiceImpl) WithdrawMessage(ctx context.Context, req *pb.WithdrawMessageRequest) (*pb.WithdrawMessageResponse, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return &pb.WithdrawMessageResponse{Code: 401, Message: "未提供认证信息"}, nil
	}
	if err := s.service.WithdrawMessage(req.MessageId, userID); err != nil {
		logger.Error("撤回消息失败", logger.ErrorField(err))
		return &pb.WithdrawMessageResponse{Code: 500, Message: err.Error()}, nil
	}
	return &pb.WithdrawMessageResponse{Code: 200, Message: "撤回成功"}, nil
}

func (s *ChatServiceImpl) EditMessage(ctx context.Context, req *pb.EditMessageRequest) (*pb.EditMessageResponse, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return &pb.EditMessageResponse{Code: 401, Message: "未提供认证信息"}, nil
	}
	msg, err := s.service.EditMessage(req.MessageId, userID, req.Content)
	if err != nil {
		logger.Error("编辑消息失败", logger.ErrorField(err))
		return &pb.EditMessageResponse{Code: 500, Message: err.Error()}, nil
	}
	return &pb.EditMessageResponse{
		Code: 200, Message: "编辑成功",
		Data: &pb.Message{
			Id: msg.ID, ChatId: msg.ChatID, ChatType: pb.ChatType(msg.ChatType),
			SenderId: msg.SenderID, MessageType: pb.MessageType(msg.MessageType),
			Content: msg.Content, Metadata: msg.Metadata, Status: pb.MessageStatus(msg.Status),
			CreatedAt: timestamppb.New(msg.CreatedAt), UpdatedAt: timestamppb.New(msg.UpdatedAt),
			ReplyToMessageId: msg.ReplyToMessage,
		},
	}, nil
}

func (s *ChatServiceImpl) CreateGroup(ctx context.Context, req *pb.CreateGroupRequest) (*pb.CreateGroupResponse, error) {
	ownerID, err := getUserID(ctx)
	if err != nil {
		return &pb.CreateGroupResponse{Code: 401, Message: "未提供认证信息"}, nil
	}
	group, err := s.service.CreateGroup(req.Name, ownerID, req.MemberIds, req.Metadata)
	if err != nil {
		logger.Error("创建群组失败", logger.ErrorField(err))
		return &pb.CreateGroupResponse{Code: 500, Message: err.Error()}, nil
	}
	return &pb.CreateGroupResponse{
		Code: 200, Message: "创建成功",
		Data: &pb.Group{
			Id: group.ID, Name: group.Name, OwnerId: group.OwnerID,
			Metadata: group.Metadata, CreatedAt: timestamppb.New(group.CreatedAt),
			UpdatedAt: timestamppb.New(group.UpdatedAt), Announcement: group.Announcement,
		},
	}, nil
}

func (s *ChatServiceImpl) InviteGroupMember(ctx context.Context, req *pb.InviteGroupMemberRequest) (*pb.InviteGroupMemberResponse, error) {
	operatorID, err := getUserID(ctx)
	if err != nil {
		return &pb.InviteGroupMemberResponse{Code: 401, Message: "未提供认证信息"}, nil
	}
	if err := s.service.InviteGroupMember(req.GroupId, operatorID, req.UserIds); err != nil {
		logger.Error("邀请群成员失败", logger.ErrorField(err))
		return &pb.InviteGroupMemberResponse{Code: 500, Message: err.Error()}, nil
	}
	return &pb.InviteGroupMemberResponse{Code: 200, Message: "邀请成功"}, nil
}

func (s *ChatServiceImpl) KickGroupMember(ctx context.Context, req *pb.KickGroupMemberRequest) (*pb.KickGroupMemberResponse, error) {
	operatorID, err := getUserID(ctx)
	if err != nil {
		return &pb.KickGroupMemberResponse{Code: 401, Message: "未提供认证信息"}, nil
	}
	if err := s.service.KickGroupMember(req.GroupId, operatorID, req.UserId); err != nil {
		logger.Error("踢出群成员失败", logger.ErrorField(err))
		return &pb.KickGroupMemberResponse{Code: 500, Message: err.Error()}, nil
	}
	return &pb.KickGroupMemberResponse{Code: 200, Message: "踢出成功"}, nil
}

func (s *ChatServiceImpl) MuteGroupMember(ctx context.Context, req *pb.MuteGroupMemberRequest) (*pb.MuteGroupMemberResponse, error) {
	operatorID, err := getUserID(ctx)
	if err != nil {
		return &pb.MuteGroupMemberResponse{Code: 401, Message: "未提供认证信息"}, nil
	}
	var muteUntil time.Time
	if req.MuteUntil != nil {
		muteUntil = req.MuteUntil.AsTime()
	}
	if err := s.service.MuteGroupMember(req.GroupId, operatorID, req.UserId, model.MuteType(req.MuteType), muteUntil); err != nil {
		logger.Error("设置群成员禁言失败", logger.ErrorField(err))
		return &pb.MuteGroupMemberResponse{Code: 500, Message: err.Error()}, nil
	}
	return &pb.MuteGroupMemberResponse{Code: 200, Message: "设置成功"}, nil
}

func (s *ChatServiceImpl) TransferGroupOwner(ctx context.Context, req *pb.TransferGroupOwnerRequest) (*pb.TransferGroupOwnerResponse, error) {
	operatorID, err := getUserID(ctx)
	if err != nil {
		return &pb.TransferGroupOwnerResponse{Code: 401, Message: "未提供认证信息"}, nil
	}
	if err := s.service.TransferGroupOwner(req.GroupId, operatorID, req.NewOwnerId); err != nil {
		logger.Error("转让群主失败", logger.ErrorField(err))
		return &pb.TransferGroupOwnerResponse{Code: 500, Message: err.Error()}, nil
	}
	return &pb.TransferGroupOwnerResponse{Code: 200, Message: "转让成功"}, nil
}

func (s *ChatServiceImpl) UpdateGroupAnnouncement(ctx context.Context, req *pb.UpdateGroupAnnouncementRequest) (*pb.UpdateGroupAnnouncementResponse, error) {
	operatorID, err := getUserID(ctx)
	if err != nil {
		return &pb.UpdateGroupAnnouncementResponse{Code: 401, Message: "未提供认证信息"}, nil
	}
	if err := s.service.UpdateGroupAnnouncement(req.GroupId, operatorID, req.Announcement); err != nil {
		logger.Error("更新群公告失败", logger.ErrorField(err))
		return &pb.UpdateGroupAnnouncementResponse{Code: 500, Message: err.Error()}, nil
	}
	return &pb.UpdateGroupAnnouncementResponse{Code: 200, Message: "更新成功"}, nil
}

func (s *ChatServiceImpl) SetGroupAdmin(ctx context.Context, req *pb.SetGroupAdminRequest) (*pb.SetGroupAdminResponse, error) {
	operatorID, err := getUserID(ctx)
	if err != nil {
		return &pb.SetGroupAdminResponse{Code: 401, Message: "未提供认证信息"}, nil
	}
	if err := s.service.SetGroupAdmin(req.GroupId, operatorID, req.UserId, req.IsAdmin); err != nil {
		logger.Error("设置管理员失败", logger.ErrorField(err))
		return &pb.SetGroupAdminResponse{Code: 500, Message: err.Error()}, nil
	}
	return &pb.SetGroupAdminResponse{Code: 200, Message: "设置成功"}, nil
}

func (s *ChatServiceImpl) GetGroupMembers(ctx context.Context, req *pb.GetGroupMembersRequest) (*pb.GetGroupMembersResponse, error) {
	members, total, err := s.service.GetGroupMembers(req.GroupId, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error("获取群成员失败", logger.ErrorField(err))
		return &pb.GetGroupMembersResponse{Code: 500, Message: err.Error()}, nil
	}

	pbMembers := make([]*pb.GroupMember, 0, len(members))
	for _, member := range members {
		pbMembers = append(pbMembers, &pb.GroupMember{
			UserId: member.UserID, Role: pb.GroupMemberRole(member.Role),
			MuteType: pb.MuteType(member.MuteType), MuteUntil: timestamppb.New(member.MuteUntil),
			JoinedAt: timestamppb.New(member.JoinedAt),
		})
	}
	return &pb.GetGroupMembersResponse{Code: 200, Message: "获取成功", Members: pbMembers, Total: int32(total)}, nil
}

func (s *ChatServiceImpl) GetGroup(ctx context.Context, req *pb.GetGroupRequest) (*pb.GetGroupResponse, error) {
	group, err := s.service.GetGroup(req.GroupId)
	if err != nil {
		logger.Error("获取群组信息失败", logger.ErrorField(err))
		return &pb.GetGroupResponse{Code: 500, Message: err.Error()}, nil
	}
	return &pb.GetGroupResponse{
		Code: 200, Message: "获取成功",
		Data: &pb.Group{
			Id: group.ID, Name: group.Name, OwnerId: group.OwnerID,
			Metadata: group.Metadata, CreatedAt: timestamppb.New(group.CreatedAt),
			UpdatedAt: timestamppb.New(group.UpdatedAt), Announcement: group.Announcement,
		},
	}, nil
}

func (s *ChatServiceImpl) JoinGroup(ctx context.Context, req *pb.JoinGroupRequest) (*pb.JoinGroupResponse, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return &pb.JoinGroupResponse{Code: 401, Message: "未提供认证信息"}, nil
	}
	if err := s.service.JoinGroup(req.GroupId, userID); err != nil {
		logger.Error("加入群组失败", logger.ErrorField(err))
		return &pb.JoinGroupResponse{Code: 500, Message: err.Error()}, nil
	}
	return &pb.JoinGroupResponse{Code: 200, Message: "加入成功"}, nil
}

func (s *ChatServiceImpl) LeaveGroup(ctx context.Context, req *pb.LeaveGroupRequest) (*pb.LeaveGroupResponse, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return &pb.LeaveGroupResponse{Code: 401, Message: "未提供认证信息"}, nil
	}
	if err := s.service.LeaveGroup(req.GroupId, userID); err != nil {
		logger.Error("退出群组失败", logger.ErrorField(err))
		return &pb.LeaveGroupResponse{Code: 500, Message: err.Error()}, nil
	}
	return &pb.LeaveGroupResponse{Code: 200, Message: "退出成功"}, nil
}
