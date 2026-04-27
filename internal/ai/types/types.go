package types

// AI 领域共享类型
// 包含知识库、问答、搜索、向量、推荐、提取、集合服务共用的数据结构

// KnowledgeType 知识类型
type KnowledgeType int

const (
	KnowledgeTypeDocument  KnowledgeType = iota + 1 // 文档
	KnowledgeTypeQA                                 // 问答对
	KnowledgeTypeConcept                            // 概念/实体
	KnowledgeTypeRelation                           // 关系
)

// ExtractionStatus 提取状态
type ExtractionStatus int

const (
	ExtractionStatusPending   ExtractionStatus = iota + 1 // 待处理
	ExtractionStatusProcessing                             // 处理中
	ExtractionStatusCompleted                              // 已完成
	ExtractionStatusFailed                                 // 失败
)

// CollectionType 集合类型
type CollectionType int

const (
	CollectionTypePersonal  CollectionType = iota + 1 // 个人知识库
	CollectionTypeTeam                                 // 团队知识库
	CollectionTypePublic                               // 公开知识库
)

// SearchResult 搜索结果
type SearchResult struct {
	ID        int64   `json:"id"`
	Title     string  `json:"title"`
	Content   string  `json:"content"`
	Score     float64 `json:"score"`
	Source    string  `json:"source"`    // 来源
	KnowledgeType string `json:"knowledge_type"` // 知识类型
}

// VectorSearchResult 向量搜索结果
type VectorSearchResult struct {
	ID       int64   `json:"id"`
	Score    float64 `json:"score"`
	Metadata map[string]string `json:"metadata"`
}

// QuestionRequest 问答请求
type QuestionRequest struct {
	Question      string   `json:"question"`
	KnowledgeIDs  []int64  `json:"knowledge_ids,omitempty"`  // 限定知识范围
	TopK          int      `json:"top_k,omitempty"`          // 检索数量
	Temperature   float64  `json:"temperature,omitempty"`    // 生成温度
}

// QuestionResponse 问答响应
type QuestionResponse struct {
	Answer      string   `json:"answer"`
	References  []int64  `json:"references"`   // 引用的知识ID
	Confidence  float64  `json:"confidence"`   // 置信度
}

// AIEvent AI 领域事件（用于 Kafka）
type AIEvent struct {
	Type       string      `json:"type"`        // 事件类型: document_uploaded, extraction_completed...
	ResourceID int64       `json:"resource_id"` // 资源ID
	Payload    interface{} `json:"payload"`     // 事件负载
	Timestamp  int64       `json:"timestamp"`   // 时间戳
}

// AI errors
var (
	ErrKnowledgeNotFound = &AIError{Code: 60401, Message: "knowledge not found"}
	ErrCollectionNotFound = &AIError{Code: 60402, Message: "collection not found"}
	ErrExtractionFailed  = &AIError{Code: 60501, Message: "extraction failed"}
	ErrVectorSearchFail  = &AIError{Code: 60502, Message: "vector search failed"}
	ErrQuestionTimeout   = &AIError{Code: 60503, Message: "question timeout"}
	ErrModelUnavailable  = &AIError{Code: 60504, Message: "model unavailable"}
	ErrQuotaExceeded     = &AIError{Code: 60505, Message: "quota exceeded"}
)

// AIError AI 领域错误
type AIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *AIError) Error() string {
	return e.Message
}
