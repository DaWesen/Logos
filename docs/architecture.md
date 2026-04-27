# Logos 架构设计文档

> Logos (λóγος) — 意为"法则、理性、道"，象征 AI 与 IM 融合的底层逻辑。

## 项目定位

Logos 是一个基于微服务架构的 **AI 增强型即时通讯平台**，将 AI 能力（知识图谱、RAG 问答、向量检索、智能推荐）深度集成到即时通讯场景中。

## 领域架构

项目采用 **领域驱动设计 (DDD)** 思想，将 14 个微服务划分为 3 个核心领域：

```
Logos/
├── Platform          平台基础设施
│   ├── gateway       API 网关 (HTTP/WS)
│   ├── user          用户认证与管理
│   └── monitoring    系统监控与健康检查
│
├── Messaging         即时通讯核心
│   ├── im            WebSocket 连接管理与消息路由
│   ├── chat          会话管理 (私聊/群聊/频道)
│   ├── contact       联系人与好友关系
│   └── message       消息存储与检索
│
└── AI                智能能力引擎
    ├── knowledge     知识库管理 (知识图谱 + 文档)
    ├── question      智能问答 (RAG + LLM)
    ├── search        全文搜索 (Elasticsearch)
    ├── vector        向量检索 (Milvus)
    ├── recommend     智能推荐
    ├── extraction    文档解析与知识提取
    └── collection    知识集合管理
```

## 目录结构

```
Noah/
├── cmd/                        # 服务入口
│   ├── platform/               # Platform 领域
│   │   ├── gateway/main.go
│   │   ├── user/main.go
│   │   └── monitoring/main.go
│   ├── messaging/              # Messaging 领域
│   │   ├── im/main.go
│   │   ├── chat/main.go
│   │   ├── contact/main.go
│   │   └── message/main.go
│   └── ai/                     # AI 领域
│       ├── knowledge/main.go
│       ├── question/main.go
│       ├── search/main.go
│       ├── vector/main.go
│       ├── recommend/main.go
│       ├── extraction/main.go
│       └── collection/main.go
│
├── internal/                   # 业务逻辑
│   ├── platform/
│   │   ├── gateway/            # 网关 (handler/router/middleware)
│   │   ├── user/               # 用户 (handler/model/dao/service)
│   │   ├── monitoring/         # 监控
│   │   └── types/              # Platform 领域共享类型
│   ├── messaging/
│   │   ├── im/                 # IM 连接
│   │   ├── chat/               # 会话
│   │   ├── contact/            # 联系人
│   │   ├── message/            # 消息
│   │   └── types/              # Messaging 领域共享类型
│   └── ai/
│       ├── knowledge/          # 知识库
│       ├── question/           # 问答
│       ├── search/             # 搜索
│       ├── vector/             # 向量
│       ├── recommend/          # 推荐
│       ├── extraction/         # 提取
│       ├── collection/         # 集合
│       └── types/              # AI 领域共享类型
│
├── idl/                        # Thrift IDL 定义 (按领域分组)
│   ├── platform/
│   ├── messaging/
│   └── ai/
│
├── kitex_gen/                  # Kitex 自动生成代码 (运行 kitex 后生成)
│
├── pkg/                        # 跨领域共享包
│   ├── cache/                  # Redis 缓存
│   ├── client/                 # Kitex 客户端工厂
│   ├── database/               # PostgreSQL 连接
│   ├── eino/                   # Eino AI 框架
│   ├── es/                     # Elasticsearch 客户端
│   ├── graph/                  # Neo4j 图数据库
│   ├── jwt/                    # JWT 认证
│   ├── logger/                 # Zap 日志
│   ├── mq/                     # Kafka 消息队列
│   ├── obs/                    # OpenTelemetry 可观测性
│   ├── ratelimit/              # 限流
│   └── vector/                 # Milvus 向量库
│
├── config/                     # 配置文件与加载
├── script/                     # 脚本
├── docs/                       # 架构文档
├── Dockerfile
├── Makefile                    # 支持领域级构建 (make build-platform 等)
├── docker-compose.yml          # 按领域分组的服务编排
├── start.sh / start.bat        # 一键管理脚本
├── go.mod                      # module Logos
└── README.md
```

## 技术栈

| 层级 | 技术 |
|------|------|
| API 网关 | Hertz (HTTP) + WebSocket |
| RPC 框架 | Kitex (Thrift) |
| 服务注册 | Etcd |
| 关系数据库 | PostgreSQL (pgvector) |
| 图数据库 | Neo4j |
| 向量数据库 | Milvus |
| 搜索引擎 | Elasticsearch |
| 缓存 | Redis |
| 消息队列 | Kafka |
| 对象存储 | MinIO |
| AI 框架 | Eino (字节跳动) |
| 可观测性 | OpenTelemetry + Prometheus + Jaeger |

## 领域间通信

```
┌─────────────────────────────────────────────────┐
│                    Platform                      │
│  Gateway ──HTTP──▶ User / Monitoring            │
│     │                                            │
│     │ Kitex RPC                                  │
│     ▼                                            │
├─────────────────────────────────────────────────┤
│                   Messaging                      │
│  IM ◀──WebSocket──▶ Client                      │
│  IM ──Kafka──▶ Chat / Message                   │
│  Chat ──Kitex──▶ Contact                        │
│                                                  │
│     │ 跨领域事件                                  │
│     ▼                                            │
├─────────────────────────────────────────────────┤
│                     AI                           │
│  Question ──▶ Knowledge ──▶ Vector              │
│  Question ──▶ Search                            │
│  Extraction ──Kafka──▶ Collection               │
│  Recommend ──▶ Vector / Knowledge               │
└─────────────────────────────────────────────────┘
```

## 领域共享类型

每个领域有一个 `types/` 包，定义该领域内跨服务共享的数据结构和错误码：

- `internal/platform/types/` — APIResponse, UserStatus, HealthStatus, PlatformError
- `internal/messaging/types/` — MessageType, ChatType, MessagingEvent, MessagingError
- `internal/ai/types/` — KnowledgeType, SearchResult, AIEvent, AIError

## 错误码规划

| 范围 | 领域 |
|------|------|
| 40xxx | Platform |
| 50xxx | Messaging |
| 60xxx | AI |

## 后续规划

1. **Gateway IM 路由** — 添加 WebSocket 升级端点，代理到 IM 服务
2. **领域事件总线** — Messaging ↔ AI 的跨领域事件（如聊天中触发 AI 问答）
3. **Kitex 代码生成** — 运行 `make generate-idl` 按 IDL 生成 kitex_gen
4. **服务重命名** — `question` → `assistant`（可选，概念上更贴合 AI 助手）
5. **API 版本化** — Gateway 支持 `/api/v1/` 前缀
6. **MinIO 集成** — 文件上传/下载管道
7. **文档解析** — Extraction 服务接入 PDF/Word 解析
