namespace go user

include "common.thrift"

// 注册请求
struct RegisterReq {
    1: string username
    2: string password
    3: optional string email
    4: optional string phone
}

// 登录请求
struct LoginReq {
    1: string account
    2: string password
}

// 登录注册响应
struct LoginRegisterResp {
    1: common.BaseResp BaseResp
    2: common.User user
    3: string token
    4: i64 expireAt
}

// 用户信息请求
struct UserInfoReq {
    1: i64 userId
}

// 用户信息响应
struct UserInfoResp {
    1: common.BaseResp BaseResp
    2: common.User user
}

// 批量用户信息请求
struct BatchUserInfoReq {
    1: list<i64> userIds
}

// 批量用户信息响应
struct BatchUserInfoResp {
    1: common.BaseResp BaseResp
    2: map<i64, common.User> users
}

// 更新用户请求
struct UpdateUserReq {
    1: i64 userId
    2: optional string email
    3: optional string phone
    4: optional string avatar
    5: optional map<string, string> preferences
    6: optional list<string> interests
    7: optional string oldPassword
    8: optional string newPassword
}

// 检查用户名请求
struct CheckUsernameReq {
    1: string username
}

// 检查用户名响应
struct CheckUsernameResp {
    1: bool available
    2: common.BaseResp BaseResp
}

// 用户统计请求
struct UserStatsReq {
    1: i64 userId
}

// 用户统计信息
struct UserStats {
    1: i64 userId
    2: i64 questionCount
    3: i64 answerCount
    4: i64 recommendationCount
}

// 用户统计响应
struct UserStatsResp {
    1: common.BaseResp BaseResp
    2: UserStats stats
    3: i64 totalUserCount
}

// 通过用户名获取用户信息请求
struct UserInfoByUsernameReq {
    1: string username
}

// 更新头像请求
struct UpdateAvatarReq {
    1: i64 userId
    2: binary avatarData
}

// 批量检查用户名请求
struct BatchCheckUsernamesReq {
    1: list<string> usernames
}

// 批量检查用户名响应
struct BatchCheckUsernamesResp {
    1: common.BaseResp BaseResp
    2: map<string, bool> availableMap
}

// 搜索用户请求
struct SearchUsersReq {
    1: string keyword
    2: i32 page
    3: i32 pageSize
}

// 搜索用户响应
struct SearchUsersResp {
    1: common.BaseResp BaseResp
    2: list<common.User> users
    3: i64 total
}

// 用户服务接口
service UserService {
    // 注册
    LoginRegisterResp Register(1: RegisterReq req)
    // 登录
    LoginRegisterResp Login(1: LoginReq req)
    // 获取用户信息
    UserInfoResp GetUserInfo(1: UserInfoReq req)
    // 通过用户名获取用户信息
    UserInfoResp GetUserInfoByUsername(1: UserInfoByUsernameReq req)
    // 批量获取用户信息
    BatchUserInfoResp BatchGetUserInfo(1: BatchUserInfoReq req)
    // 更新用户信息
    common.BaseResp UpdateUser(1: UpdateUserReq req)
    // 更新头像
    common.BaseResp UpdateAvatar(1: UpdateAvatarReq req)
    // 检查用户名是否可用
    CheckUsernameResp CheckUsername(1: CheckUsernameReq req)
    // 批量检查用户名是否可用
    BatchCheckUsernamesResp BatchCheckUsernames(1: BatchCheckUsernamesReq req)
    // 获取用户统计信息
    UserStatsResp GetUserStats(1: UserStatsReq req)
    // 搜索用户
    SearchUsersResp SearchUsers(1: SearchUsersReq req)
    // 验证令牌
    bool VerifyToken(1: string token)
}
