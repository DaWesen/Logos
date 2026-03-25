namespace go vector

include "common.thrift"

// 向量模型类型
enum VectorModelType {
    BERT = 1
    Word2Vec = 2
    GloVe = 3
    fastText = 4
    SentenceBERT = 5
    Custom = 6
}

// 向量索引类型
enum IndexType {
    FLAT = 1
    IVF_FLAT = 2
    IVF_PQ = 3
    HNSW = 4
}

// 向量
struct Vector {
    1: string id
    2: list<double> values
    3: map<string, string> metadata
    4: i64 createdAt
}

// 向量集合
struct VectorCollection {
    1: string id
    2: string name
    3: VectorModelType modelType
    4: IndexType indexType
    5: i32 dimension
    6: map<string, string> parameters
    7: i64 size
    8: i64 createdAt
    9: i64 updatedAt
}

// 创建向量集合请求
struct CreateCollectionReq {
    1: string name
    2: VectorModelType modelType
    3: IndexType indexType
    4: i32 dimension
    5: map<string, string> parameters
}

// 更新向量集合请求
struct UpdateCollectionReq {
    1: string id
    2: optional string name
    3: optional map<string, string> parameters
}

// 删除向量集合请求
struct DeleteCollectionReq {
    1: string id
}

// 向量集合响应
struct CollectionResp {
    1: common.BaseResp BaseResp
    2: VectorCollection collection
}

// 批量向量集合响应
struct BatchCollectionResp {
    1: common.BaseResp BaseResp
    2: list<VectorCollection> collections
}

// 向量化请求
struct VectorizeReq {
    1: string text
    2: string collectionId
    3: optional map<string, string> metadata
}

// 批量向量化请求
struct BatchVectorizeReq {
    1: list<string> texts
    2: string collectionId
    3: optional list<map<string, string>> metadataList
}

// 向量化响应
struct VectorizeResp {
    1: common.BaseResp BaseResp
    2: Vector vector
}

// 批量向量化响应
struct BatchVectorizeResp {
    1: common.BaseResp BaseResp
    2: list<Vector> vectors
}

// 相似性搜索请求
struct SearchReq {
    1: string collectionId
    2: list<double> queryVector
    3: i32 topK
    4: optional double threshold
    5: optional map<string, string> filter
}

// 搜索结果项
struct SearchResultItem {
    1: string vectorId
    2: double score
    3: map<string, string> metadata
}

// 搜索响应
struct SearchResp {
    1: common.BaseResp BaseResp
    2: list<SearchResultItem> results
    3: i64 searchTime
}

// 文本搜索请求
struct TextSearchReq {
    1: string collectionId
    2: string text
    3: i32 topK
    4: optional double threshold
    5: optional map<string, string> filter
}

// 删除向量请求
struct DeleteVectorReq {
    1: string collectionId
    2: string vectorId
}

// 批量删除向量请求
struct BatchDeleteVectorReq {
    1: string collectionId
    2: list<string> vectorIds
}

// 向量处理服务接口
service VectorService {
    // 集合管理
    CollectionResp CreateCollection(1: CreateCollectionReq req)
    CollectionResp UpdateCollection(1: UpdateCollectionReq req)
    common.BaseResp DeleteCollection(1: DeleteCollectionReq req)
    CollectionResp GetCollection(1: string id)
    BatchCollectionResp ListCollections()
    
    // 向量化
    VectorizeResp Vectorize(1: VectorizeReq req)
    BatchVectorizeResp BatchVectorize(1: BatchVectorizeReq req)
    
    // 相似性搜索
    SearchResp Search(1: SearchReq req)
    SearchResp TextSearch(1: TextSearchReq req)
    
    // 向量管理
    common.BaseResp DeleteVector(1: DeleteVectorReq req)
    common.BaseResp BatchDeleteVector(1: BatchDeleteVectorReq req)
}
