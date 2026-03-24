namespace go knowledge

include "common.thrift"

// 实体
struct Entity {
    1: string id
    2: string type
    3: string name
    4: map<string, string> properties
    5: optional string description
    6: i64 createdAt
    7: i64 updatedAt
}

// 关系
struct Relation {
    1: string id
    2: string type
    3: string sourceId
    4: string targetId
    5: map<string, string> properties
    6: optional string description
    7: i64 createdAt
    8: i64 updatedAt
}

// 添加实体请求
struct AddEntityReq {
    1: string type
    2: string name
    3: map<string, string> properties
    4: optional string description
}

// 更新实体请求
struct UpdateEntityReq {
    1: string id
    2: optional string type
    3: optional string name
    4: optional map<string, string> properties
    5: optional string description
}

// 删除实体请求
struct DeleteEntityReq {
    1: string id
}

// 实体响应
struct EntityResp {
    1: common.BaseResp BaseResp
    2: Entity entity
}

// 批量实体响应
struct BatchEntityResp {
    1: common.BaseResp BaseResp
    2: list<Entity> entities
}

// 添加关系请求
struct AddRelationReq {
    1: string type
    2: string sourceId
    3: string targetId
    4: map<string, string> properties
    5: optional string description
}

// 更新关系请求
struct UpdateRelationReq {
    1: string id
    2: optional string type
    3: optional string sourceId
    4: optional string targetId
    5: optional map<string, string> properties
    6: optional string description
}

// 删除关系请求
struct DeleteRelationReq {
    1: string id
}

// 关系响应
struct RelationResp {
    1: common.BaseResp BaseResp
    2: Relation relation
}

// 批量关系响应
struct BatchRelationResp {
    1: common.BaseResp BaseResp
    2: list<Relation> relations
}

// 查询实体请求
struct QueryEntityReq {
    1: optional string type
    2: optional string name
    3: optional map<string, string> properties
    4: i32 page
    5: i32 pageSize
}

// 查询关系请求
struct QueryRelationReq {
    1: optional string type
    2: optional string sourceId
    3: optional string targetId
    4: i32 page
    5: i32 pageSize
}

// 图谱统计响应
struct GraphStatsResp {
    1: common.BaseResp BaseResp
    2: i64 entityCount
    3: i64 relationCount
    4: map<string, i64> entityTypeCount
    5: map<string, i64> relationTypeCount
}

// 导入数据请求
struct ImportDataReq {
    1: string dataType
    2: list<string> data
}

// 知识服务接口
service KnowledgeService {
    // 实体管理
    EntityResp AddEntity(1: AddEntityReq req)
    EntityResp UpdateEntity(1: UpdateEntityReq req)
    common.BaseResp DeleteEntity(1: DeleteEntityReq req)
    EntityResp GetEntity(1: string id)
    BatchEntityResp QueryEntities(1: QueryEntityReq req)
    
    // 关系管理
    RelationResp AddRelation(1: AddRelationReq req)
    RelationResp UpdateRelation(1: UpdateRelationReq req)
    common.BaseResp DeleteRelation(1: DeleteRelationReq req)
    RelationResp GetRelation(1: string id)
    BatchRelationResp QueryRelations(1: QueryRelationReq req)
    
    // 图谱分析
    GraphStatsResp GetGraphStats()
    BatchEntityResp GetRelatedEntities(1: string entityId, 1: optional string relationType)
    
    // 数据导入
    common.BaseResp ImportData(1: ImportDataReq req)
}
