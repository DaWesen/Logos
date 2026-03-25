namespace go question

include "common.thrift"

// 问题请求
struct QuestionReq {
    1: string content
    2: i64 userId
    3: optional map<string, string> context
}

// 回答响应
struct AnswerResp {
    1: common.BaseResp BaseResp
    2: string answer
    3: double confidence
    4: list<string> sources
    5: string questionId
    6: i64 timestamp
}

// 问答历史
struct QARecord {
    1: string id
    2: string question
    3: string answer
    4: double confidence
    5: i64 userId
    6: i64 timestamp
    7: optional string feedback
    8: optional i32 rating
}

// 历史记录请求
struct HistoryReq {
    1: i64 userId
    2: i32 page
    3: i32 pageSize
}

// 历史记录响应
struct HistoryResp {
    1: common.BaseResp BaseResp
    2: list<QARecord> records
    3: i64 total
}

// 反馈请求
struct FeedbackReq {
    1: string questionId
    2: string feedback
    3: optional i32 rating
}

// 批量问题请求
struct BatchQuestionReq {
    1: list<string> questions
    2: i64 userId
}

// 批量回答响应
struct BatchAnswerResp {
    1: common.BaseResp BaseResp
    2: map<string, string> answers
}

// 问答服务接口
service QAService {
    // 处理单个问题
    AnswerResp AskQuestion(1: QuestionReq req)
    
    // 批量处理问题
    BatchAnswerResp BatchAskQuestions(1: BatchQuestionReq req)
    
    // 获取问答历史
    HistoryResp GetHistory(1: HistoryReq req)
    
    // 提交反馈
    common.BaseResp SubmitFeedback(1: FeedbackReq req)
    
    // 获取推荐问题
    common.BaseResp GetRecommendedQuestions(1: i64 userId)
}
