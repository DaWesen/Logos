package handler

import (
	common "Noah/kitex_gen/common"
	message "Noah/kitex_gen/message"
	"context"
)

// MessageServiceImpl implements the last service interface defined in the IDL.
type MessageServiceImpl struct{}

// SendMessage implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) SendMessage(ctx context.Context, req *message.SendMessageReq) (resp *message.MessageResp, err error) {
	// TODO: Your code here...
	return
}

// BatchSendMessage implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) BatchSendMessage(ctx context.Context, req *message.BatchSendMessageReq) (resp *message.BatchMessageResp, err error) {
	// TODO: Your code here...
	return
}

// Subscribe implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) Subscribe(ctx context.Context, req *message.SubscribeReq) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// ConsumeMessages implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) ConsumeMessages(ctx context.Context, req *message.ConsumeMessageReq) (resp *message.ConsumeMessageResp, err error) {
	// TODO: Your code here...
	return
}

// AcknowledgeMessage implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) AcknowledgeMessage(ctx context.Context, req *message.AcknowledgeMessageReq) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// BatchAcknowledgeMessages implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) BatchAcknowledgeMessages(ctx context.Context, req *message.BatchAcknowledgeMessageReq) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// GetMessageStats implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) GetMessageStats(ctx context.Context) (resp *message.MessageStatsResp, err error) {
	// TODO: Your code here...
	return
}

// CreateTopic implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) CreateTopic(ctx context.Context, topic message.Topic) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// DeleteTopic implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) DeleteTopic(ctx context.Context, topic message.Topic) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// ClearMessages implements the MessageServiceImpl interface.
func (s *MessageServiceImpl) ClearMessages(ctx context.Context, topic message.Topic) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}
