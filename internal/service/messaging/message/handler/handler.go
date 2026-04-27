package handler

import (
	"context"
	"encoding/json"

	"Logos/internal/service/messaging/message/service"
	pb "Logos/proto_gen/message"
	pbCommon "Logos/proto_gen/common"
)

type MessageServiceImpl struct {
	pb.UnimplementedMessageServiceServer
	MessageService service.MessageService
}

func priorityToInt32(p *pb.Priority) *int32 {
	if p == nil {
		return nil
	}
	v := int32(*p)
	return &v
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
		resp.Responses = append(resp.Responses, &pb.MessageResp{
			BaseResp:  &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
			MessageId: m.GetID(),
			Topic:     pb.Topic(1),
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
		json.Unmarshal([]byte(m.GetHeaders()), &headers)

		var topicVal pb.Topic
		switch m.GetTopic() {
		case "DATA_COLLECTION":
			topicVal = pb.Topic_DATA_COLLECTION
		case "KNOWLEDGE_EXTRACTION":
			topicVal = pb.Topic_KNOWLEDGE_EXTRACTION
		case "VECTOR_PROCESSING":
			topicVal = pb.Topic_VECTOR_PROCESSING
		case "QA_REQUEST":
			topicVal = pb.Topic_QA_REQUEST
		case "RECOMMENDATION":
			topicVal = pb.Topic_RECOMMENDATION
		case "USER_ACTIVITY":
			topicVal = pb.Topic_USER_ACTIVITY
		case "SYSTEM_EVENT":
			topicVal = pb.Topic_SYSTEM_EVENT
		}

		var priorityVal pb.Priority
		switch m.GetPriority() {
		case 1:
			priorityVal = pb.Priority_LOW
		case 2:
			priorityVal = pb.Priority_NORMAL
		case 3:
			priorityVal = pb.Priority_HIGH
		case 4:
			priorityVal = pb.Priority_URGENT
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

// AcknowledgeMessage implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) AcknowledgeMessage(ctx context.Context, req *pb.AcknowledgeMessageReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}

	if ackErr := s.MessageService.AcknowledgeMessage(ctx, req.ConsumerGroup, req.MessageId, int32(req.Topic)); ackErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = ackErr.Error()
	}
	return resp, nil
}

// BatchAcknowledgeMessages implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) BatchAcknowledgeMessages(ctx context.Context, req *pb.BatchAcknowledgeMessageReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}

	if ackErr := s.MessageService.BatchAcknowledgeMessages(ctx, req.ConsumerGroup, req.MessageIds, int32(req.Topic)); ackErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = ackErr.Error()
	}
	return resp, nil
}

// GetMessageStats implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) GetMessageStats(ctx context.Context, req *pb.EmptyReq) (*pb.MessageStatsResp, error) {
	resp := &pb.MessageStatsResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	stats, statsErr := s.MessageService.GetMessageStats()
	if statsErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = statsErr.Error()
		return resp, nil
	}

	for _, stat := range stats {
		topicStr, _ := stat["topic"].(string)
		total, _ := stat["total_messages"].(int64)
		pending, _ := stat["pending_messages"].(int64)
		processed, _ := stat["processed_messages"].(int64)
		failed, _ := stat["error_messages"].(int64)

		var topicVal pb.Topic
		switch topicStr {
		case "DATA_COLLECTION":
			topicVal = pb.Topic_DATA_COLLECTION
		case "KNOWLEDGE_EXTRACTION":
			topicVal = pb.Topic_KNOWLEDGE_EXTRACTION
		case "VECTOR_PROCESSING":
			topicVal = pb.Topic_VECTOR_PROCESSING
		case "QA_REQUEST":
			topicVal = pb.Topic_QA_REQUEST
		case "RECOMMENDATION":
			topicVal = pb.Topic_RECOMMENDATION
		case "USER_ACTIVITY":
			topicVal = pb.Topic_USER_ACTIVITY
		case "SYSTEM_EVENT":
			topicVal = pb.Topic_SYSTEM_EVENT
		default:
			continue
		}

		resp.Stats = append(resp.Stats, &pb.MessageStats{
			Topic:             topicVal,
			TotalMessages:     total,
			PendingMessages:   pending,
			ProcessedMessages: processed,
			ErrorMessages:     failed,
		})
	}
	return resp, nil
}

// CreateTopic implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) CreateTopic(ctx context.Context, req *pb.GetByTopicReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}
	if createErr := s.MessageService.CreateTopic(int32(req.Topic)); createErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = createErr.Error()
	}
	return resp, nil
}

// DeleteTopic implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) DeleteTopic(ctx context.Context, req *pb.GetByTopicReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}
	if delErr := s.MessageService.DeleteTopic(int32(req.Topic)); delErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = delErr.Error()
	}
	return resp, nil
}

// ClearMessages implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) ClearMessages(ctx context.Context, req *pb.GetByTopicReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}
	if clearErr := s.MessageService.ClearMessages(int32(req.Topic)); clearErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = clearErr.Error()
	}
	return resp, nil
}
