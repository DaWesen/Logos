namespace go extraction

include "common.thrift"

// 抽取任务类型
enum ExtractionTaskType {
    ENTITY_RECOGNITION = 1
    RELATION_EXTRACTION = 2
    TRIPLE_EXTRACTION = 3
    SUMMARIZATION = 4
    KEYPHRASE_EXTRACTION = 5
}

// 抽取任务状态
enum TaskStatus {
    PENDING = 1
    RUNNING = 2
    SUCCESS = 3
    FAILED = 4
    CANCELLED = 5
}

// 实体
struct ExtractedEntity {
    1: string id
    2: string text
    3: string type
    4: double confidence
    5: i32 startPos
    6: i32 endPos
}

// 关系
struct ExtractedRelation {
    1: string id
    2: string type
    3: string sourceId
    4: string targetId
    5: double confidence
    6: string text
}

// 三元组
struct Triple {
    1: string id
    2: string subject
    3: string predicate
    4: string object
    5: double confidence
}

// 抽取结果
struct ExtractionResult {
    1: string id
    2: string taskId
    3: TaskStatus status
    4: list<ExtractedEntity> entities
    5: list<ExtractedRelation> relations
    6: list<Triple> triples
    7: optional string summary
    8: optional list<string> keyphrases
    9: optional string errorMessage
    10: i64 startTime
    11: i64 endTime
}

// 抽取任务
struct ExtractionTask {
    1: string id
    2: ExtractionTaskType type
    3: string dataId
    4: string dataType
    5: TaskStatus status
    6: map<string, string> parameters
    7: optional string scheduledTime
    8: optional string startTime
    9: optional string endTime
    10: i64 createdAt
    11: i64 updatedAt
}

// 创建抽取任务请求
struct CreateExtractionTaskReq {
    1: ExtractionTaskType type
    2: string dataId
    3: string dataType
    4: map<string, string> parameters
    5: optional string scheduledTime
}

// 更新抽取任务请求
struct UpdateExtractionTaskReq {
    1: string id
    2: optional ExtractionTaskType type
    3: optional map<string, string> parameters
    4: optional string scheduledTime
}

// 执行抽取任务请求
struct ExecuteExtractionTaskReq {
    1: string taskId
}

// 任务响应
struct ExtractionTaskResp {
    1: common.BaseResp BaseResp
    2: ExtractionTask task
}

// 批量任务响应
struct BatchExtractionTaskResp {
    1: common.BaseResp BaseResp
    2: list<ExtractionTask> tasks
}

// 结果响应
struct ExtractionResultResp {
    1: common.BaseResp BaseResp
    2: ExtractionResult result
}

// 批量结果响应
struct BatchExtractionResultResp {
    1: common.BaseResp BaseResp
    2: list<ExtractionResult> results
}

// 文本抽取请求
struct TextExtractionReq {
    1: string text
    2: ExtractionTaskType type
    3: map<string, string> parameters
}

// 文本抽取响应
struct TextExtractionResp {
    1: common.BaseResp BaseResp
    2: list<ExtractedEntity> entities
    3: list<ExtractedRelation> relations
    4: list<Triple> triples
    5: optional string summary
    6: optional list<string> keyphrases
}

// 知识抽取服务接口
service KnowledgeExtractionService {
    // 任务管理
    ExtractionTaskResp CreateTask(1: CreateExtractionTaskReq req)
    ExtractionTaskResp UpdateTask(1: UpdateExtractionTaskReq req)
    common.BaseResp DeleteTask(1: string taskId)
    ExtractionTaskResp GetTask(1: string id)
    BatchExtractionTaskResp ListTasks()
    
    // 任务执行
    ExtractionResultResp ExecuteTask(1: ExecuteExtractionTaskReq req)
    common.BaseResp CancelTask(1: string taskId)
    
    // 结果管理
    ExtractionResultResp GetExtractionResult(1: string id)
    BatchExtractionResultResp ListExtractionResults(1: string taskId)
    
    // 实时文本抽取
    TextExtractionResp ExtractFromText(1: TextExtractionReq req)
}
