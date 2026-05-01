# Logos

## 项目简介

Logos 是一个面向多人在线的即时通讯系统，内置可自部署的 AI 助手，将大模型能力深度集成到聊天场景中，实现 "通讯 + AI" 的深度融合。

## 架构概览

项目采用**微服务架构**，分为三大领域：

```
┌─────────────────────────────────────────────────────────────────┐
│                        Platform (平台层)                         │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐        │
│  │ Gateway  │  │   User   │  │ Billing  │  │ Monitor  │        │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘        │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Messaging (通讯层)                         │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐        │
│  │    IM    │  │   Chat   │  │ Contact  │  │ Message  │        │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘        │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                       AI (智能能力层)                            │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐        │
│  │Knowledge │  │ Question │  │  Search  │  │  Vector  │        │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘        │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐        │
│  │Recommend │  │Extraction│  │Collection│  │  Process  │        │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘        │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐        │
│  │    Bot   │  │  Summary │  │  MCP    │  │ Moderate │        │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘        │
└─────────────────────────────────────────────────────────────────┘
```

## 核心功能

### 即时通讯
- **实时消息收发**：基于 WebSocket，支持单聊、群聊、广播消息
- **消息类型**：文本、图片、文件、语音
- **消息已读回执**
- **输入状态提示**
- **在线状态管理**
- **消息本地存储与云端漫游**
- **历史消息搜索**：按关键词、时间范围搜索

### 好友与群组
- **好友关系管理**：添加、删除、分组、备注
- **群组管理**：创建、邀请、踢出、禁言、转让群主、群公告、管理员设置
- **消息引用与回复**
- **限时撤回与编辑**
- **离线推送与上线同步**
- **多端消息同步**：同一账号多端登录，消息实时同步

### AI 能力
- **内置聊天 Bot**：接入多家厂商模型接口，用户可直接 @Bot 对话
- **RAG 知识库 Bot**：用户可上传多种格式的文档构建私有知识库，Bot 可基于知识库回答问题
- **MCP 工具集成**：Bot 可调用外部工具（天气查询、代码执行、Web 搜索等），扩展能力边界
- **多 Bot 协作**：支持配置多个不同人设/能力的 Bot，按场景自动路由或由用户指定
- **记忆能力**：Bot 记住用户偏好与历史交互，跨会话保持上下文
- **消息总结**：一键总结群聊/单聊历史消息，生成要点摘要与待办提取
- **智能回复候选**：根据上下文生成回复候选，用户可一键选用
- **实时多语言翻译**
- **消息审核**：接入模型对消息内容进行实时审核与过滤

### 计费管理
- 支持用户提供自己的 API Key，也可使用平台模型
- 完整的计费记录与余额管理

## 快速开始

### 环境要求
- Go 1.25+
- Docker & Docker Compose

### 启动基础设施

```bash
docker-compose up -d
```

将启动以下基础设施：
- PostgreSQL：主数据库
- Redis：缓存与会话管理
- Milvus：向量数据库
- Elasticsearch：全文搜索
- Neo4j：图数据库（知识图谱）
- MinIO：对象存储
- Kafka：消息队列
- Etcd：服务发现
- Prometheus + Grafana：监控与告警
- Jaeger：链路追踪

### 开发模式

```bash
# 启动 Gateway
cd cmd/platform/gateway
go run main.go

# 启动其他服务
cd cmd/platform/user
go run main.go

cd cmd/messaging/im
go run main.go

cd cmd/ai/bot
go run main.go
```

## 技术栈

| 层级 | 技术 |
|------|------|
| 编程语言 | Go 1.25 |
| API 网关 | Gin + WebSocket |
| RPC 框架 | gRPC |
| 服务发现 | Etcd |
| 关系数据库 | PostgreSQL |
| 图数据库 | Neo4j |
| 向量数据库 | Milvus |
| 搜索引擎 | Elasticsearch |
| 缓存 | Redis |
| 消息队列 | Kafka |
| 对象存储 | MinIO |
| ORM | GORM |
| AI 框架 | Eino (字节跳动) |
| 可观测性 | OpenTelemetry + Prometheus + Grafana + Jaeger |
| 限流 | Redis 限流 + 中间件 |

## 项目结构

```
Logos/
├── cmd/                              # 服务入口
│   ├── platform/                     # Platform 领域服务
│   │   ├── gateway/
│   │   ├── user/
│   │   ├── billing/
│   │   └── monitoring/
│   ├── messaging/                    # Messaging 领域服务
│   │   ├── im/
│   │   ├── chat/
│   │   ├── contact/
│   │   └── message/
│   └── ai/                           # AI 领域服务
│       ├── knowledge/
│       ├── question/
│       ├── search/
│       ├── vector/
│       ├── recommend/
│       ├── extraction/
│       ├── collection/
│       ├── process/
│       ├── bot/
│       └── ...
├── internal/                         # 业务逻辑
│   ├── service/                      # 服务实现
│   │   ├── platform/
│   │   │   ├── gateway/              # handler/middleware/websocket
│   │   │   ├── user/                 # handler/service/dao/model
│   │   │   ├── billing/
│   │   │   └── monitoring/
│   │   ├── messaging/
│   │   │   ├── im/
│   │   │   ├── chat/
│   │   │   ├── contact/
│   │   │   └── message/
│   │   └── ai/
│   │       ├── knowledge/
│   │       ├── question/
│   │       ├── search/
│   │       ├── vector/
│   │       ├── recommend/
│   │       ├── extraction/
│   │       ├── collection/
│   │       ├── process/              # 文档解析
│   │       │   └── parser/           # PDF/图片/音频/视频解析
│   │       └── bot/
│   ├── bot/                         # Bot 引擎
│   │   └── agent/                   # Agent 管理
│   └── models/                      # AI 模型封装
│       ├── asr/                     # 语音识别
│       ├── video/                   # 视频处理
│       └── vlm/                     # 视觉语言模型
├── idl/                             # Proto IDL 定义
│   ├── platform/
│   ├── messaging/
│   └── ai/
├── proto_gen/                       # Proto 自动生成代码
├── pkg/                             # 共享库
│   ├── cache/                       # Redis 缓存
│   ├── database/                    # PostgreSQL
│   ├── es/                          # Elasticsearch
│   ├── graph/                       # Neo4j
│   ├── jwt/                         # JWT 认证
│   ├── logger/                      # Zap 日志
│   ├── mq/                          # Kafka
│   ├── obs/                         # OpenTelemetry 可观测性
│   ├── ratelimit/                   # 限流
│   ├── storage/                     # MinIO
│   ├── vector/                      # Milvus
│   ├── grpcserver/                  # gRPC 服务启动器
│   ├── register/                    # Etcd 服务发现
│   └── client/                      # gRPC 客户端工厂
├── config/                          # 配置文件
├── docs/                            # 文档
├── docker-compose.yml               # Docker Compose 编排
├── Dockerfile                       # 服务 Dockerfile
├── go.mod
└── README.md
```

## 服务端口

| 领域 | 服务 | 端口 |
|------|------|------|
| Platform | Gateway | 8080 |
| Platform | User | 9001 |
| Platform | Billing | 9002 |
| Platform | Monitoring | 9010 |
| Messaging | IM | 9011 |
| Messaging | Chat | 9012 |
| Messaging | Contact | 9013 |
| Messaging | Message | 9009 |
| AI | Knowledge | 9003 |
| AI | Search | 9004 |
| AI | Vector | 9005 |
| AI | Question | 9006 |
| AI | Recommend | 9007 |
| AI | Extraction | 9008 |
| AI | Collection | 9009 |
| AI | Process | 9015 |
| AI | Bot | 9014 |

## 监控与可观测性

- **Grafana**：http://localhost:3000
- **Prometheus**：http://localhost:9090
- **Jaeger**：http://localhost:16686
