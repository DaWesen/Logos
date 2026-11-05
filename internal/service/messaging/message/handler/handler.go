package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"Logos/internal/service/messaging/message/service"
	"Logos/pkg/logger"
	pbCommon "Logos/proto_gen/common"
	pb "Logos/proto_gen/message"
)

type MessageServiceImpl struct {
	pb.UnimplementedMessageServiceServer
	MessageService service.MessageService
}

func priorityToInt32(p *pb.Priority) *int32 {
	if p == nil {
		return nil
	}
	// proto Priority 从 0 开始（LOW=0），内部模型约定是 1..4。
	// 直接透传会把 LOW 存成 0、URGENT 存成 HIGH，消费端读回来整体偏移一格。
	v := int32(*p) + 1
	return &v
}

// topicFromInternal 把存储层的内部 topic 名映射回 proto 枚举。
func topicFromInternal(topic string) (pb.Topic, bool) {
	switch topic {
	case "DATA_COLLECTION":
		return pb.Topic_DATA_COLLECTION, true
	case "KNOWLEDGE_EXTRACTION":
		return pb.Topic_KNOWLEDGE_EXTRACTION, true
	case "VECTOR_PROCESSING":
		return pb.Topic_VECTOR_PROCESSING, true
	case "QA_REQUEST":
		return pb.Topic_QA_REQUEST, true
	case "RECOMMENDATION":
		return pb.Topic_RECOMMENDATION, true
	case "USER_ACTIVITY":
		return pb.Topic_USER_ACTIVITY, true
	case "SYSTEM_EVENT":
		return pb.Topic_SYSTEM_EVENT, true
	default:
		return pb.Topic_DATA_COLLECTION, false
	}
}

// SendMessage implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) SendMessage(ctx context.Context, req *pb.SendMessageReq) (*pb.MessageResp, error) {
	resp := &pb.MessageResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	msg, sendErr := s.MessageService.SendMessage(ctx,
		int32(req.Topic),
		req.Content,
		priorityToInt32(req.Priority),
		req.Headers,
		req.CorrelationId,
	)
	if sendErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = sendErr.Error()
		return resp, nil
	}

	resp.MessageId = msg.GetID()
	resp.Topic = req.Topic
	resp.Timestamp = msg.GetTimestamp()
	return resp, nil
}

// BatchSendMessage implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) BatchSendMessage(ctx context.Context, req *pb.BatchSendMessageReq) (*pb.BatchMessageResp, error) {
	resp := &pb.BatchMessageResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	inputs := make([]struct {
		Topic         int32
		Content       string
		Priority      *int32
		Headers       map[string]string
		CorrelationID *string
	}, len(req.Messages))

	for i, m := range req.Messages {
		inputs[i] = struct {
			Topic         int32
			Content       string
			Priority      *int32
			Headers       map[string]string
			CorrelationID *string
		}{
			Topic:         int32(m.Topic),
			Content:       m.Content,
			Priority:      priorityToInt32(m.Priority),
			Headers:       m.Headers,
			CorrelationID: m.CorrelationId,
		}
	}

	msgs, batchErr := s.MessageService.BatchSendMessage(ctx, inputs)
	if batchErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = batchErr.Error()
		return resp, nil
	}

	for _, m := range msgs {
		topicVal, ok := topicFromInternal(m.GetTopic())
		if !ok {
			// 未知 topic 之前被映射成 DATA_COLLECTION（enum 零值），
			// 客户端会把一条 VECTOR_PROCESSING 消息当成数据采集任务处理。
			// 宁可让整条读取失败，也不返回一个语义错误的枚举。
			logger.Error("批量发送响应无法识别消息主题",
				logger.StringField("message_id", m.GetID()),
				logger.StringField("topic", m.GetTopic()))
			resp.Responses = append(resp.Responses, &pb.MessageResp{
				BaseResp: &pbCommon.BaseResp{
					StatusCode:    500,
					StatusMessage: fmt.Sprintf("消息 %s 的主题无法识别: %q", m.GetID(), m.GetTopic()),
				},
				MessageId: m.GetID(),
			})
			continue
		}
		resp.Responses = append(resp.Responses, &pb.MessageResp{
			BaseResp:  &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
			MessageId: m.GetID(),
			Topic:     topicVal,
			Timestamp: m.GetTimestamp(),
		})
	}
	return resp, nil
}

// Subscribe implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) Subscribe(ctx context.Context, req *pb.SubscribeReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}

	if subErr := s.MessageService.Subscribe(ctx,
		int32(req.Topic),
		req.ConsumerGroup,
		req.Config,
	); subErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = subErr.Error()
	}
	return resp, nil
}

// ConsumeMessages implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) ConsumeMessages(ctx context.Context, req *pb.ConsumeMessageReq) (*pb.ConsumeMessageResp, error) {
	resp := &pb.ConsumeMessageResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	msgs, consumeErr := s.MessageService.ConsumeMessages(ctx,
		req.ConsumerGroup,
		int32(req.Topic),
		req.MaxMessages,
		req.TimeoutMs,
	)
	if consumeErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = consumeErr.Error()
		return resp, nil
	}

	for _, m := range msgs {
		headers := make(map[string]string)
		if rawHeaders := m.GetHeaders(); rawHeaders != "" {
			if err := json.Unmarshal([]byte(rawHeaders), &headers); err != nil {
				logger.Error("消息头不是合法 JSON，拒绝返回半条消息",
					logger.StringField("message_id", m.GetID()),
					logger.ErrorField(err))
				resp.BaseResp.StatusCode = 500
				resp.BaseResp.StatusMessage = fmt.Sprintf("消息 %s 的 headers 不是合法 JSON: %v", m.GetID(), err)
				resp.Messages = nil
				resp.MessageCount = 0
				return resp, nil
			}
		}

		topicVal, ok := topicFromInternal(m.GetTopic())
		if !ok {
			logger.Error("消费消息遇到无法识别的主题",
				logger.StringField("message_id", m.GetID()),
				logger.StringField("topic", m.GetTopic()))
			resp.BaseResp.StatusCode = 500
			resp.BaseResp.StatusMessage = fmt.Sprintf("消息 %s 的主题无法识别: %q", m.GetID(), m.GetTopic())
			resp.Messages = nil
			resp.MessageCount = 0
			return resp, nil
		}

		priorityVal, ok := priorityFromInternal(int32(m.GetPriority()))
		if !ok {
			// 存储约定是 1..4，落到 default 说明数据已经损坏。
			// 旧 switch 没有 default，任何非法值都会被当成 LOW 返回。
			logger.Error("消费消息遇到非法优先级",
				logger.StringField("message_id", m.GetID()),
				logger.IntField("priority", m.GetPriority()))
			resp.BaseResp.StatusCode = 500
			resp.BaseResp.StatusMessage = fmt.Sprintf("消息 %s 的优先级非法: %d", m.GetID(), m.GetPriority())
			resp.Messages = nil
			resp.MessageCount = 0
			return resp, nil
		}

		resp.Messages = append(resp.Messages, &pb.Message{
			Id:            m.GetID(),
			Topic:         topicVal,
			Content:       m.GetContent(),
			Priority:      priorityVal,
			Headers:       headers,
			Timestamp:     m.GetTimestamp(),
			CorrelationId: m.GetCorrelationID(),
		})
	}

	resp.MessageCount = int32(len(resp.Messages))
	return resp, nil
}

// priorityFromInternal 把存储层的 1..4 映射回 proto 枚举。
// 未知值返回 ok=false，由调用方决定报错，避免静默落成 LOW。
func priorityFromInternal(priority int32) (pb.Priority, bool) {
	switch priority {
	case 1:
		return pb.Priority_LOW, true
	case 2:
		return pb.Priority_NORMAL, true
	case 3:
		return pb.Priority_HIGH, true
	case 4:
		return pb.Priority_URGENT, true
	default:
		return pb.Priority_LOW, false
	}
}

// AcknowledgeMessage implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) AcknowledgeMessage(ctx context.Context, req *pb.AcknowledgeMessageReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}

	if ackErr := s.MessageService.AcknowledgeMessage(ctx, req.ConsumerGroup, req.MessageId, int32(req.Topic)); ackErr != nil {
		resp.StatusCode = ackErrorCode(ackErr)
		resp.StatusMessage = ackErr.Error()
	}
	return resp, nil
}

// BatchAcknowledgeMessages implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) BatchAcknowledgeMessages(ctx context.Context, req *pb.BatchAcknowledgeMessageReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}

	if ackErr := s.MessageService.BatchAcknowledgeMessages(ctx, req.ConsumerGroup, req.MessageIds, int32(req.Topic)); ackErr != nil {
		resp.StatusCode = ackErrorCode(ackErr)
		resp.StatusMessage = ackErr.Error()
	}
	return resp, nil
}

func ackErrorCode(err error) int32 {
	if errors.Is(err, service.ErrMessageNotClaimed) {
		return 404
	}
	return 500
}

// GetMessageStats implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) GetMessageStats(ctx context.Context, req *pb.EmptyReq) (*pb.MessageStatsResp, error) {
	resp := &pb.MessageStatsResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	stats, statsErr := s.MessageService.GetMessageStats(ctx)
	if statsErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = statsErr.Error()
		return resp, nil
	}

	for _, stat := range stats {
		topicVal, ok := topicFromInternal(stat.Topic)
		if !ok {
			continue
		}

		resp.Stats = append(resp.Stats, &pb.MessageStats{
			Topic:             topicVal,
			TotalMessages:     stat.Total,
			PendingMessages:   stat.Pending,
			ProcessedMessages: stat.Processed,
			ErrorMessages:     stat.Failed,
		})
	}
	return resp, nil
}

// CreateTopic implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) CreateTopic(ctx context.Context, req *pb.GetByTopicReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}
	if createErr := s.MessageService.CreateTopic(ctx, int32(req.Topic)); createErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = createErr.Error()
	}
	return resp, nil
}

// DeleteTopic implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) DeleteTopic(ctx context.Context, req *pb.GetByTopicReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}
	if delErr := s.MessageService.DeleteTopic(ctx, int32(req.Topic)); delErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = delErr.Error()
	}
	return resp, nil
}

// ClearMessages implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) ClearMessages(ctx context.Context, req *pb.GetByTopicReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}
	if clearErr := s.MessageService.ClearMessages(ctx, int32(req.Topic)); clearErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = clearErr.Error()
	}
	return resp, nil
}
