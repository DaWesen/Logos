namespace go collection

include "common.thrift"

// 数据源类型
enum DataSourceType {
    INTERNAL_SYSTEM = 1
    DOCUMENT = 2
    API = 3
    DATABASE = 4
    WEBSITE = 5
}

// 数据格式
enum DataFormat {
    JSON = 1
    CSV = 2
    XML = 3
    TXT = 4
    PDF = 5
    WORD = 6
    EXCEL = 7
}

// 数据源配置
struct DataSource {
    1: string id
    2: string name
    3: DataSourceType type
    4: string url
    5: map<string, string> config
    6: optional string description
    7: i64 createdAt
    8: i64 updatedAt
}

// 添加数据源请求
struct AddDataSourceReq {
    1: string name
    2: DataSourceType type
    3: string url
    4: map<string, string> config
    5: optional string description
}

// 更新数据源请求
struct UpdateDataSourceReq {
    1: string id
    2: optional string name
    3: optional DataSourceType type
    4: optional string url  
    5: optional map<string, string> config
    6: optional string description
}

// 删除数据源请求
struct DeleteDataSourceReq {
    1: string id
}

// 数据源响应
struct DataSourceResp {
    1: common.BaseResp BaseResp
    2: DataSource dataSource
}

// 批量数据源响应
struct BatchDataSourceResp {
    1: common.BaseResp BaseResp
    2: list<DataSource> dataSources
}

// 采集任务
struct CollectionTask {
    1: string id
    2: string dataSourceId
    3: string name
    4: DataFormat format
    5: string status
    6: optional string schedule
    7: optional string lastRunTime
    8: optional string nextRunTime
    9: i64 createdAt
    10: i64 updatedAt
}

// 创建采集任务请求
struct CreateTaskReq {
    1: string dataSourceId
    2: string name
    3: DataFormat format
    4: optional string schedule
}

// 更新采集任务请求
struct UpdateTaskReq {
    1: string id
    2: optional string name
    3: optional DataFormat format
    4: optional string schedule
}

// 执行采集任务请求
struct ExecuteTaskReq {
    1: string taskId
}

// 任务响应
struct TaskResp {
    1: common.BaseResp BaseResp
    2: CollectionTask task
}

// 批量任务响应
struct BatchTaskResp {
    1: common.BaseResp BaseResp
    2: list<CollectionTask> tasks
}

// 采集结果
struct CollectionResult {
    1: string id
    2: string taskId
    3: string status
    4: i64 collectedCount
    5: i64 processedCount
    6: optional string errorMessage
    7: i64 startTime
    8: i64 endTime
}

// 采集结果响应
struct CollectionResultResp {
    1: common.BaseResp BaseResp
    2: CollectionResult result
}

// 批量采集结果响应
struct BatchCollectionResultResp {
    1: common.BaseResp BaseResp
    2: list<CollectionResult> results
}

// 数据采集服务接口
service DataCollectionService {
    // 数据源管理
    DataSourceResp AddDataSource(1: AddDataSourceReq req)
    DataSourceResp UpdateDataSource(1: UpdateDataSourceReq req)
    common.BaseResp DeleteDataSource(1: DeleteDataSourceReq req)
    DataSourceResp GetDataSource(1: string id)
    BatchDataSourceResp ListDataSources()
    
    // 任务管理
    TaskResp CreateTask(1: CreateTaskReq req)
    TaskResp UpdateTask(1: UpdateTaskReq req)
    common.BaseResp DeleteTask(1: string taskId)
    TaskResp GetTask(1: string id)
    BatchTaskResp ListTasks()
    
    // 任务执行
    CollectionResultResp ExecuteTask(1: ExecuteTaskReq req)
    common.BaseResp StopTask(1: string taskId)
    
    // 结果管理
    CollectionResultResp GetCollectionResult(1: string id)
    BatchCollectionResultResp ListCollectionResults(1: string taskId)
}
