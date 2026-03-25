namespace go search

include "common.thrift"

// 搜索索引
enum IndexType {
    ENTITY = 1
    RELATION = 2
    QUESTION = 3
    ANSWER = 4
    DOCUMENT = 5
    USER = 6
}

// 搜索条件
struct SearchCondition {
    1: string field
    2: string value
    3: string operator
}

// 排序条件
struct SortCondition {
    1: string field
    2: string order
}

// 搜索请求
struct SearchReq {
    1: string query
    2: IndexType indexType
    3: i32 page
    4: i32 pageSize
    5: optional list<SearchCondition> conditions
    6: optional list<SortCondition> sorts
    7: optional map<string, string> filters
}

// 搜索结果项
struct SearchResultItem {
    1: string id
    2: string type
    3: string title
    4: string content
    5: double score
    6: map<string, string> metadata
}

// 搜索响应
struct SearchResp {
    1: common.BaseResp BaseResp
    2: list<SearchResultItem> results
    3: i64 total
    4: i64 searchTime
}

// 索引文档
struct IndexDocument {
    1: string id
    2: IndexType type
    3: string title
    4: string content
    5: map<string, string> metadata
    6: i64 createdAt
    7: i64 updatedAt
}

// 添加文档请求
struct AddDocumentReq {
    1: IndexType type
    2: string title
    3: string content
    4: map<string, string> metadata
}

// 更新文档请求
struct UpdateDocumentReq {
    1: string id
    2: optional string title
    3: optional string content
    4: optional map<string, string> metadata
}

// 删除文档请求
struct DeleteDocumentReq {
    1: string id
}

// 文档响应
struct DocumentResp {
    1: common.BaseResp BaseResp
    2: IndexDocument document
}

// 批量添加文档请求
struct BatchAddDocumentReq {
    1: list<AddDocumentReq> documents
}

// 批量删除文档请求
struct BatchDeleteDocumentReq {
    1: list<string> ids
}

// 索引统计
struct IndexStats {
    1: IndexType type
    2: i64 documentCount
    3: i64 sizeInBytes
    4: string lastUpdated
}

// 索引统计响应
struct IndexStatsResp {
    1: common.BaseResp BaseResp
    2: list<IndexStats> stats
}

// 搜索服务接口
service SearchService {
    // 搜索
    SearchResp Search(1: SearchReq req)
    
    // 文档管理
    DocumentResp AddDocument(1: AddDocumentReq req)
    DocumentResp UpdateDocument(1: UpdateDocumentReq req)
    common.BaseResp DeleteDocument(1: DeleteDocumentReq req)
    DocumentResp GetDocument(1: string id)
    
    // 批量操作
    common.BaseResp BatchAddDocuments(1: BatchAddDocumentReq req)
    common.BaseResp BatchDeleteDocuments(1: BatchDeleteDocumentReq req)
    
    // 索引管理
    common.BaseResp CreateIndex(1: IndexType type)
    common.BaseResp DeleteIndex(1: IndexType type)
    common.BaseResp RefreshIndex(1: IndexType type)
    IndexStatsResp GetIndexStats()
}
