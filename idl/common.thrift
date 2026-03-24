namespace go common

// 基础响应结构
struct BaseResp {
    1: i32 statusCode
    2: string statusMessage
    3: i64 serviceTime
}

// 用户信息
struct User {
    1: i64 id
    2: string username
    3: optional string email
    4: optional string phone
    5: optional string avatar
    6: optional map<string, string> preferences
    7: optional list<string> interests
    8: i64 createdAt
    9: i64 updatedAt
}
