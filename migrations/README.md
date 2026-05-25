# Logos 数据库迁移

本项目使用结构化的 SQL 迁移管理，参考了 WeKnora 的设计模式。

## 迁移结构

```
migrations/
└── versioned/
    ├── 000000_init.up.sql      # 初始化表结构
    ├── 000000_init.down.sql    # 回滚初始化
    └── README.md               # 本文件
```

## 使用方法

### 方法 1：GORM AutoMigrate（默认）
Logos 服务启动时会自动调用 GORM AutoMigrate 来同步表结构，这是最简单的方式。

### 方法 2：手动执行 SQL
你也可以手动执行迁移脚本：

```bash
# 连接到数据库
docker exec -it logos-postgres psql -U logos -d logos

# 在 psql 中执行迁移
\i /path/to/migrations/versioned/000000_init.up.sql
```

## 表结构说明

### 1. User 模块
- **users**: 用户表

### 2. Contact 模块
- **friendships**: 好友关系表
- **friend_requests**: 好友请求表
- **friend_groups**: 好友分组表
- **friend_group_members**: 分组成员表

### 3. Chat 模块
- **conversations**: 会话表
- **conversation_participants**: 会话参与者表
- **messages**: 消息表
- **groups**: 群组表
- **group_members**: 群成员表

### 4. IM 模块
- **online_records**: 在线状态表

### 5. Message 模块
- **queue_messages**: 消息队列表
- **message_subscriptions**: 消息订阅表

### 6. Billing 模块
- **accounts**: 账户表（余额、使用统计）
- **transactions**: 交易记录表（充值/消费/退款/提现）

### 7. Monitoring 模块
- **service_statuses**: 服务状态表（服务名、状态、最后检查时间、元数据）

### 8. Bot 模块
- **bots**: Bot 配置表（名称、人设、模型配置、RAG 开关、关联知识库）
- **bot_messages**: Bot 消息表（用户消息和 Bot 回复）
- **conversations**: Bot 对话表
- **user_memories**: 用户记忆表（Bot 自动提取的用户偏好）

### 9. AI 模块
- **vector_collections**: 向量集合表（知识库配置，含模型配置）
- **documents**: 文档表（上传的文档及其处理状态）
- **document_chunks**: 文档分块表（文档分块内容及向量 ID）

## 类型规范

### 用户 ID 类型
- 数据库：`BIGINT` (PostgreSQL)
- Go 模型：`int64`
- 避免使用 `string` 类型存储用户 ID

### 时间戳类型
- 数据库：`TIMESTAMPTZ` (带时区的时间戳)
- Go 模型：`time.Time`
- 避免使用 UNIX 时间戳存储时间

## 创建新迁移

要创建新的迁移，请遵循以下步骤：

1. 创建升级脚本 `000001_feature.up.sql`
2. 创建回滚脚本 `000001_feature.down.sql`
3. 在 SQL 中使用 `IF NOT EXISTS` 避免错误
4. 添加详细的日志输出

示例：
```sql
DO $$ BEGIN RAISE NOTICE '[Migration 000001] Starting...'; END $$;

ALTER TABLE users ADD COLUMN IF NOT EXISTS new_column VARCHAR(100);

DO $$ BEGIN RAISE NOTICE '[Migration 000001] Completed'; END $$;
```

## 注意事项

1. **始终使用 `IF NOT EXISTS`**：避免重复创建表或索引错误
2. **向后兼容**：迁移脚本应保持向后兼容性
3. **事务安全**：重要操作使用事务
4. **日志输出**：使用 `RAISE NOTICE` 输出进度日志
5. **PostgreSQL 兼容性**：确保使用 PostgreSQL 兼容的语法
