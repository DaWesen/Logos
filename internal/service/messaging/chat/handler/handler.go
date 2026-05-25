package handler

import (
	"context"
	"strings"
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

func chatTypeToProto(chatType int) pb.ChatType {
	return pb.ChatType(chatType)
}

func messageTypeToProto(msgType int) pb.MessageType {
	return pb.MessageType(msgType)
}

func msgToProto(msg *model.Message) *pb.Message {
	return &pb.Message{
		Id:               msg.ID,
		ChatId:           msg.ChatID,
		ChatType:         chatTypeToProto(msg.ChatType),
		SenderId:         msg.SenderID,
		MessageType:      messageTypeToProto(msg.MessageType),
		Content:          msg.Content,
		MediaUrl:         msg.MediaURL,
		MediaMeta:        string(msg.MediaMeta),
		Metadata:         msg.Metadata,
		Status:           messageStatusToProto(msg.Status),
		CreatedAt:        timestamppb.New(msg.CreatedAt),
		UpdatedAt:        timestamppb.New(msg.UpdatedAt),
		ReplyToMessageId: msg.ReplyToMessage,
	}
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
		Data: msgToProto(msg),
	}, nil
}

func (s *ChatServiceImpl) GetMessageHistory(ctx context.Context, req *pb.GetMessageHistoryRequest) (*pb.GetMessageHistoryResponse, error) {
	var beforeTime time.Time
	if req.BeforeTime != nil {
		beforeTime = req.BeforeTime.AsTime()
	}

	// 群聊鉴权检查
	if model.ChatType(req.ChatType) == model.ChatTypeGroup {
		userID, err := getUserID(ctx)
		if err != nil {
			return &pb.GetMessageHistoryResponse{Code: 401, Message: "未提供认证信息"}, nil
		}
		member, err := s.service.GetGroupMember(req.ChatId, userID)
		if err != nil {
			logger.Error("检查群成员身份失败", logger.ErrorField(err))
			return &pb.GetMessageHistoryResponse{Code: 500, Message: "检查群成员身份失败"}, nil
		}
		if member == nil {
			return &pb.GetMessageHistoryResponse{Code: 403, Message: "您不是该群组成员"}, nil
		}
	}

	messages, hasMore, err := s.service.GetMessageHistory(req.ChatId, model.ChatType(req.ChatType), beforeTime, int(req.Limit))
	if err != nil {
		logger.Error("获取消息历史失败", logger.ErrorField(err))
		return &pb.GetMessageHistoryResponse{Code: 500, Message: err.Error()}, nil
	}

	pbMessages := make([]*pb.Message, 0, len(messages))
	for _, msg := range messages {
		pbMessages = append(pbMessages, msgToProto(msg))
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
		pbMessages = append(pbMessages, msgToProto(msg))
	}

	return &pb.SearchMessagesResponse{Code: 200, Message: "搜索成功", Messages: pbMessages, Total: int32(total)}, nil
}

func (s *ChatServiceImpl) MarkMessagesRead(ctx context.Context, req *pb.MarkMessagesReadRequest) (*pb.MarkMessagesReadResponse, error) {
	userID, _ := getUserID(ctx)
	if err := s.service.MarkMessagesRead(req.MessageIds, userID, req.ChatId); err != nil {
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
		// 业务逻辑错误返回400，系统错误返回500
		errMsg := err.Error()
		if strings.Contains(errMsg, "超过撤回时限") ||
			strings.Contains(errMsg, "只能撤回自己的消息") ||
			strings.Contains(errMsg, "消息不存在") {
			return &pb.WithdrawMessageResponse{Code: 400, Message: errMsg}, nil
		}
		return &pb.WithdrawMessageResponse{Code: 500, Message: "撤回失败"}, nil
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
		Data: msgToProto(msg),
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

	memberIDs, _ := s.service.GetGroupMemberIDs(group.ID)

	return &pb.CreateGroupResponse{
		Code: 200, Message: "创建成功",
		Data: &pb.Group{
			Id: group.ID, Name: group.Name, OwnerId: group.OwnerID,
			MemberIds: memberIDs,
			CreatedAt: timestamppb.New(group.CreatedAt),
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

func (s *ChatServiceImpl) UpdateGroupAvatar(ctx context.Context, req *pb.UpdateGroupAvatarRequest) (*pb.UpdateGroupAvatarResponse, error) {
	operatorID, err := getUserID(ctx)
	if err != nil {
		return &pb.UpdateGroupAvatarResponse{Code: 401, Message: "未提供认证信息"}, nil
	}
	if err := s.service.UpdateGroupAvatar(req.GroupId, operatorID, req.Avatar); err != nil {
		logger.Error("更新群头像失败", logger.ErrorField(err))
		return &pb.UpdateGroupAvatarResponse{Code: 500, Message: err.Error()}, nil
	}
	return &pb.UpdateGroupAvatarResponse{Code: 200, Message: "更新成功"}, nil
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
	// 群聊鉴权检查
	userID, err := getUserID(ctx)
	if err != nil {
		return &pb.GetGroupMembersResponse{Code: 401, Message: "未提供认证信息"}, nil
	}
	member, err := s.service.GetGroupMember(req.GroupId, userID)
	if err != nil {
		logger.Error("检查群成员身份失败", logger.ErrorField(err))
		return &pb.GetGroupMembersResponse{Code: 500, Message: "检查群成员身份失败"}, nil
	}
	if member == nil {
		return &pb.GetGroupMembersResponse{Code: 403, Message: "您不是该群组成员"}, nil
	}

	members, total, err := s.service.GetGroupMembers(req.GroupId, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error("获取群成员失败", logger.ErrorField(err))
		return &pb.GetGroupMembersResponse{Code: 500, Message: err.Error()}, nil
	}

	pbMembers := make([]*pb.GroupMember, 0, len(members))
	for _, member := range members {
		pbMember := &pb.GroupMember{
			UserId: member.UserID, Role: pb.GroupMemberRole(member.Role),
			MuteType: pb.MuteType(member.MuteType),
			JoinedAt: timestamppb.New(member.JoinedAt),
		}
		if member.MuteUntil != nil {
			pbMember.MuteUntil = timestamppb.New(*member.MuteUntil)
		}
		pbMembers = append(pbMembers, pbMember)
	}
	return &pb.GetGroupMembersResponse{Code: 200, Message: "获取成功", Members: pbMembers, Total: int32(total)}, nil
}

func (s *ChatServiceImpl) GetGroup(ctx context.Context, req *pb.GetGroupRequest) (*pb.GetGroupResponse, error) {
	// 群聊鉴权检查
	userID, err := getUserID(ctx)
	if err != nil {
		return &pb.GetGroupResponse{Code: 401, Message: "未提供认证信息"}, nil
	}
	member, err := s.service.GetGroupMember(req.GroupId, userID)
	if err != nil {
		logger.Error("检查群成员身份失败", logger.ErrorField(err))
		return &pb.GetGroupResponse{Code: 500, Message: "检查群成员身份失败"}, nil
	}
	if member == nil {
		return &pb.GetGroupResponse{Code: 403, Message: "您不是该群组成员"}, nil
	}

	group, err := s.service.GetGroup(req.GroupId)
	if err != nil {
		logger.Error("获取群组信息失败", logger.ErrorField(err))
		return &pb.GetGroupResponse{Code: 500, Message: err.Error()}, nil
	}

	memberIDs, _ := s.service.GetGroupMemberIDs(group.ID)

	return &pb.GetGroupResponse{
		Code: 200, Message: "获取成功",
		Data: &pb.Group{
			Id: group.ID, Name: group.Name, OwnerId: group.OwnerID,
			MemberIds: memberIDs,
			CreatedAt: timestamppb.New(group.CreatedAt),
			UpdatedAt: timestamppb.New(group.UpdatedAt), Announcement: group.Announcement,
			MemberCount: int32(group.MemberCount),
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

func (s *ChatServiceImpl) GetConversationList(ctx context.Context, req *pb.GetConversationListRequest) (*pb.GetConversationListResponse, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return &pb.GetConversationListResponse{Code: 401, Message: "未提供认证信息"}, nil
	}

	logger.Info("[DEBUG] Handler GetConversationList called", logger.StringField("user_id", userID))

	page := int(req.Page)
	pageSize := int(req.PageSize)
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	items, total, err := s.service.GetConversationList(userID, page, pageSize)
	if err != nil {
		logger.Error("获取会话列表失败", logger.ErrorField(err))
		return &pb.GetConversationListResponse{Code: 500, Message: err.Error()}, nil
	}
	logger.Info("[DEBUG] Service returned items", logger.IntField("count", len(items)), logger.AnyField("items", items))

	conversations := make([]*pb.ConversationItem, 0, len(items))
	for _, item := range items {
		ci := &pb.ConversationItem{
			ChatId:      item.ChatID,
			ChatType:    pb.ChatType(item.ChatType),
			Name:        item.Name,
			Avatar:      item.Avatar,
			UnreadCount: int32(item.UnreadCount),
			IsPinned:    item.IsPinned,
			IsMuted:     item.IsMuted,
			IsFriend:    item.IsFriend,
			IsBlocked:   item.IsBlocked,
		}
		if item.LastMessage != nil {
			ci.LastMessage = &pb.Message{
				Id:        item.LastMessage.ID,
				ChatId:    item.LastMessage.ChatID,
				ChatType:  pb.ChatType(item.LastMessage.ChatType),
				SenderId:  item.LastMessage.SenderID,
				Content:   item.LastMessage.Content,
				MediaUrl:  item.LastMessage.MediaURL,
				MediaMeta: string(item.LastMessage.MediaMeta),
				Status:    messageStatusToProto(item.LastMessage.Status),
				CreatedAt: timestamppb.New(item.LastMessage.CreatedAt),
				UpdatedAt: timestamppb.New(item.LastMessage.UpdatedAt),
			}
		}
		if !item.UpdatedAt.IsZero() {
			ci.UpdatedAt = timestamppb.New(item.UpdatedAt)
		}
		conversations = append(conversations, ci)
	}

	resp := &pb.GetConversationListResponse{
		Code:          200,
		Message:       "获取成功",
		Conversations: conversations,
		Total:         int32(total),
	}
	logger.Info("[DEBUG] Handler returning resp", logger.StringField("user_id", userID), logger.AnyField("resp", resp))
	return resp, nil
}

func (s *ChatServiceImpl) GetUnreadCount(ctx context.Context, req *pb.GetUnreadCountRequest) (*pb.GetUnreadCountResponse, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return &pb.GetUnreadCountResponse{Code: 401, Message: "未提供认证信息"}, nil
	}

	if len(req.ChatIds) == 0 {
		total, err := s.service.GetTotalUnreadCount(userID)
		if err != nil {
			return &pb.GetUnreadCountResponse{Code: 500, Message: err.Error()}, nil
		}
		return &pb.GetUnreadCountResponse{
			Code:  200,
			Total: int32(total),
		}, nil
	}

	counts, err := s.service.GetUnreadCounts(userID, req.ChatIds)
	if err != nil {
		logger.Error("获取未读数失败", logger.ErrorField(err))
		return &pb.GetUnreadCountResponse{Code: 500, Message: err.Error()}, nil
	}

	pbCounts := make([]*pb.ChatUnreadCount, 0, len(counts))
	var total int64
	for chatID, count := range counts {
		pbCounts = append(pbCounts, &pb.ChatUnreadCount{
			ChatId: chatID,
			Count:  int32(count),
		})
		total += count
	}

	return &pb.GetUnreadCountResponse{
		Code:   200,
		Counts: pbCounts,
		Total:  int32(total),
	}, nil
}

func (s *ChatServiceImpl) ForwardMessage(ctx context.Context, req *pb.ForwardMessageRequest) (*pb.ForwardMessageResponse, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return &pb.ForwardMessageResponse{Code: 401, Message: "未提供认证信息"}, nil
	}

	messages, err := s.service.ForwardMessage(req.MessageId, req.TargetChatIds, userID)
	if err != nil {
		logger.Error("转发消息失败", logger.ErrorField(err))
		return &pb.ForwardMessageResponse{Code: 500, Message: err.Error()}, nil
	}

	pbMessages := make([]*pb.Message, 0, len(messages))
	for _, msg := range messages {
		pbMessages = append(pbMessages, msgToProto(msg))
	}

	return &pb.ForwardMessageResponse{
		Code:              200,
		Message:           "转发成功",
		ForwardedMessages: pbMessages,
	}, nil
}

func (s *ChatServiceImpl) DeleteChat(ctx context.Context, req *pb.DeleteChatRequest) (*pb.DeleteChatResponse, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return &pb.DeleteChatResponse{Code: 401, Message: "未提供认证信息"}, nil
	}

	chatType := model.ChatType(req.ChatType)
	if req.ChatType == pb.ChatType_CHAT_TYPE_UNSPECIFIED {
		chatType = model.ChatTypePrivate
	}

	if err := s.service.DeleteChat(req.ChatId, userID, chatType); err != nil {
		logger.Error("删除聊天失败", logger.ErrorField(err))
		return &pb.DeleteChatResponse{Code: 500, Message: err.Error()}, nil
	}

	return &pb.DeleteChatResponse{
		Code:    200,
		Message: "删除成功",
	}, nil
}

func (s *ChatServiceImpl) DeleteChatHistory(ctx context.Context, req *pb.DeleteChatHistoryRequest) (*pb.DeleteChatHistoryResponse, error) {
	userID, err := getUserID(ctx)
	if err != nil {
		return &pb.DeleteChatHistoryResponse{Code: 401, Message: "未提供认证信息"}, nil
	}

	if err := s.service.DeleteChatHistory(req.ChatId); err != nil {
		logger.Error("删除聊天记录失败", logger.ErrorField(err), logger.StringField("chat_id", req.ChatId), logger.StringField("user_id", userID))
		return &pb.DeleteChatHistoryResponse{Code: 500, Message: err.Error()}, nil
	}

	return &pb.DeleteChatHistoryResponse{
		Code:    200,
		Message: "删除成功",
	}, nil
}

func messageStatusToProto(status string) pb.MessageStatus {
	switch status {
	case "sent":
		return pb.MessageStatus_MESSAGE_STATUS_SENT
	case "delivered":
		return pb.MessageStatus_MESSAGE_STATUS_DELIVERED
	case "read":
		return pb.MessageStatus_MESSAGE_STATUS_READ
	case "withdrawn":
		return pb.MessageStatus_MESSAGE_STATUS_WITHDRAWN
	case "edited":
		return pb.MessageStatus_MESSAGE_STATUS_EDITED
	default:
		return pb.MessageStatus_MESSAGE_STATUS_UNSPECIFIED
	}
}
