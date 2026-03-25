namespace go message

include "common.thrift"

// 消息主题
enum Topic {
    DATA_COLLECTION = 1
    KNOWLEDGE_EXTRACTION = 2
    VECTOR_PROCESSING = 3
    QA_REQUEST = 4
    RECOMMENDATION = 5
    USER_ACTIVITY = 6
    SYSTEM_EVENT = 7
}

// 消息优先级
enum Priority {
    LOW = 1
    NORMAL = 2
    HIGH = 3
    URGENT = 4
}

// 消息
struct Message {
    1: string id
    2: Topic topic
    3: string content
    4: Priority priority
    5: map<string, string> headers
    6: i64 timestamp
    7: optional string correlationId
}

// 发送消息请求
struct SendMessageReq {
    1: Topic topic
    2: string content
    3: optional Priority priority
    4: optional map<string, string> headers
    5: optional string correlationId
}

// 批量发送消息请求
struct BatchSendMessageReq {
    1: list<SendMessageReq> messages
}

// 消息响应
struct MessageResp {
    1: common.BaseResp BaseResp
    2: string messageId
    3: Topic topic
    4: i64 timestamp
}

// 批量消息响应
struct BatchMessageResp {
    1: common.BaseResp BaseResp
    2: list<MessageResp> responses
}

// 订阅请求
struct SubscribeReq {
    1: Topic topic
    2: string consumerGroup
    3: map<string, string> config
}

// 消费消息请求
struct ConsumeMessageReq {
    1: string consumerGroup
    2: Topic topic
    3: i32 maxMessages
    4: i32 timeoutMs
}

// 消费消息响应
struct ConsumeMessageResp {
    1: common.BaseResp BaseResp
    2: list<Message> messages
    3: i32 messageCount
}

// 确认消息请求
struct AcknowledgeMessageReq {
    1: string consumerGroup
    2: string messageId
    3: Topic topic
}

// 批量确认消息请求
struct BatchAcknowledgeMessageReq {
    1: string consumerGroup
    2: list<string> messageIds
    3: Topic topic
}

// 消息统计
struct MessageStats {
    1: Topic topic
    2: i64 totalMessages
    3: i64 pendingMessages
    4: i64 processedMessages
    5: i64 errorMessages
}

// 消息统计响应
struct MessageStatsResp {
    1: common.BaseResp BaseResp
    2: list<MessageStats> stats
}

// 消息服务接口
service MessageService {
    // 消息发送
    MessageResp SendMessage(1: SendMessageReq req)
    BatchMessageResp BatchSendMessage(1: BatchSendMessageReq req)
    
    // 消息消费
    common.BaseResp Subscribe(1: SubscribeReq req)
    ConsumeMessageResp ConsumeMessages(1: ConsumeMessageReq req)
    common.BaseResp AcknowledgeMessage(1: AcknowledgeMessageReq req)
    common.BaseResp BatchAcknowledgeMessages(1: BatchAcknowledgeMessageReq req)
    
    // 消息管理
    MessageStatsResp GetMessageStats()
    common.BaseResp CreateTopic(1: Topic topic)
    common.BaseResp DeleteTopic(1: Topic topic)
    common.BaseResp ClearMessages(1: Topic topic)
}
