namespace go recommend

include "common.thrift"

// 推荐项
struct RecommendationItem {
    1: string id
    2: string type
    3: string title
    4: string description
    5: double score
    6: string entityId
    7: optional string imageUrl
    8: i64 createdAt
}

// 推荐请求
struct RecommendationReq {
    1: i64 userId
    2: optional string type
    3: optional i32 limit
    4: optional map<string, string> context
}

// 推荐响应
struct RecommendationResp {
    1: common.BaseResp BaseResp
    2: list<RecommendationItem> items
    3: i64 total
}

// 相关推荐请求
struct RelatedRecommendationReq {
    1: string entityId
    2: optional string type
    3: optional i32 limit
}

// 推荐反馈请求
struct FeedbackReq {
    1: string itemId
    2: i64 userId
    3: string action
    4: i64 timestamp
}

// 推荐历史请求
struct HistoryReq {
    1: i64 userId
    2: i32 page
    3: i32 pageSize
}

// 推荐历史项
struct HistoryItem {
    1: string id
    2: string itemId
    3: string itemType
    4: string title
    5: string action
    6: i64 timestamp
}

// 推荐历史响应
struct HistoryResp {
    1: common.BaseResp BaseResp
    2: list<HistoryItem> items
    3: i64 total
}

// 批量推荐请求
struct BatchRecommendationReq {
    1: list<i64> userIds
    2: optional string type
    3: optional i32 limit
}

// 批量推荐响应
struct BatchRecommendationResp {
    1: common.BaseResp BaseResp
    2: map<i64, list<RecommendationItem>> recommendations
}

// 推荐服务接口
service RecommendationService {
    // 获取个性化推荐
    RecommendationResp GetRecommendations(1: RecommendationReq req)
    
    // 获取相关推荐
    RecommendationResp GetRelatedRecommendations(1: RelatedRecommendationReq req)
    
    // 提交推荐反馈
    common.BaseResp SubmitFeedback(1: FeedbackReq req)
    
    // 获取推荐历史
    HistoryResp GetRecommendationHistory(1: HistoryReq req)
    
    // 批量获取推荐
    BatchRecommendationResp BatchGetRecommendations(1: BatchRecommendationReq req)
}
