# Logos

Logos 是一个面向多人在线的即时通讯系统，内置可自部署的 AI 助手，将大模型能力深度集成到聊天场景中，实现「通讯 + AI」的深度融合。

## 架构概览

项目采用**微服务架构**，分为三大领域层：

```
                          ┌──────────────┐
                          │   Nginx :80  │
                          └──────┬───────┘
                                 │
┌────────────────────────────────┼────────────────────────────────────┐
│                        Platform (平台层)                             │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐            │
│  │ Gateway  │  │   User   │  │ Billing  │  │ Monitor  │            │
│  │  :8888   │  │  :9001   │  │  :9015   │  │  :9010   │            │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘            │
└────────────────────────────────┬────────────────────────────────────┘
                                 │ gRPC + Etcd
┌────────────────────────────────┼────────────────────────────────────┐
│                      Messaging (通讯层)                              │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐            │
│  │    IM    │  │   Chat   │  │ Contact  │  │ Message  │            │
│  │  :9011   │  │  :9012   │  │  :9013   │  │  :9009   │            │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘            │
└────────────────────────────────┬────────────────────────────────────┘
                                 │ gRPC + Etcd
┌────────────────────────────────┼────────────────────────────────────┐
│                       AI (智能能力层)                                │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐            │
│  │Knowledge │  │  Search  │  │  Vector  │  │ Question │            │
│  │  :9002   │  │  :9003   │  │  :9004   │  │  :9005   │            │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘            │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐            │
│  │Recommend │  │Extraction│  │Collection│  │  Process │            │
│  │  :9006   │  │  :9007   │  │  :9008   │  │  :9016   │            │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘            │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐            │
│  │    Bot   │  │  Summary │  │   MCP    │  │Moderate  │            │
│  │  :9014   │  │  :9017   │  │  :9018   │  │  :9019   │            │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘            │
└─────────────────────────────────────────────────────────────────────┘
                                 │
┌─────────────────────────────────────────────────────────────────────┐
│                        基础设施层                                    │
│  PostgreSQL │ Redis │ Kafka │ Etcd │ Milvus │ Elasticsearch │ Neo4j  │
│  MinIO │ ZooKeeper │ OpenTelemetry │ Prometheus │ Grafana │ Jaeger  │
└─────────────────────────────────────────────────────────────────────┘
```

## 核心功能

### 即时通讯

- **实时消息收发**：基于 WebSocket，支持单聊、群聊、广播消息
- **消息类型**：文本、图片、文件、语音
- **消息已读回执**
- **输入状态提示**
- **在线状态管理**
- **历史消息搜索**：按关键词、时间范围搜索
- **离线推送与上线同步**
- **多端消息同步**：同一账号多端登录，消息实时同步

### 好友与群组

- **好友关系管理**：添加、删除、分组、备注、黑名单
- **群组管理**：创建、邀请、踢出、禁言、转让群主、群公告、管理员设置
- **消息引用与回复**
- **限时撤回与编辑**

### AI 能力

- **内置聊天 Bot**：接入多家厂商模型接口，用户可直接 @Bot 对话
- **RAG 知识库 Bot**：用户可上传多种格式的文档构建私有知识库，Bot 可基于知识库回答问题
- **MCP 工具集成**：Bot 可调用外部工具（天气查询、代码执行、Web 搜索、计算器等），扩展能力边界
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
- Docker & Docker Compose (推荐 v2+)
- 至少 8GB 可用内存

### 一键部署（Docker Compose）

```bash
# 克隆项目
git clone https://github.com/your-org/Logos.git
cd Logos

# 启动全部服务
docker compose up -d

# 查看服务状态
docker compose ps

# 查看网关日志
docker compose logs -f gateway
```

首次启动会自动构建所有微服务镜像，耗时较长。后续启动使用缓存会快很多。

### 配置 AI 模型

项目使用 [Eino](https://github.com/cloudwego/eino) 框架接入大模型，默认配置为 DeepSeek。修改 `config/config.yaml` 中的 `eino` 部分：

```yaml
eino:
  api_key: your-api-key
  model: deepseek-chat
  base_url: https://api.deepseek.com/
  embedding_model: deepseek-embedding
```

或在 `docker-compose.yml` 中通过环境变量覆盖：

```yaml
environment:
  ARK_API_KEY: your-api-key
  ARK_MODEL: your-model-name
```

### 开发模式

```bash
# 先启动基础设施
docker compose up -d postgres redis etcd kafka elasticsearch neo4j milvus minio

# 本地运行单个服务
cd cmd/platform/gateway
go run main.go

# 本地运行其他服务
cd cmd/platform/user && go run main.go
cd cmd/messaging/chat && go run main.go
cd cmd/ai/bot && go run main.go
```

### 管理脚本

项目提供了交互式管理脚本：

```bash
# Linux / macOS
bash start.sh

# Windows
start.bat
```

支持启动/停止/重启/查看状态/日志/构建镜像/清理缓存等操作。

## API 接口

所有接口通过 Gateway（默认 `:8888`）统一入口访问。

### 公开接口

| 方法   | 路径                      | 说明           |
| ---- | ----------------------- | ------------ |
| POST | `/api/v1/auth/register` | 用户注册         |
| POST | `/api/v1/auth/login`    | 用户登录         |
| GET  | `/health`               | 健康检查         |
| GET  | `/ws`                   | WebSocket 连接 |

### 认证方式

除公开接口外，所有请求需在 Header 中携带 JWT Token：

```
Authorization: Bearer <token>
```

### 业务接口

| 路径前缀                 | 说明     | 主要端点                                                   |
| -------------------- | ------ | ------------------------------------------------------ |
| `/api/v1/users`      | 用户管理   | GET /:id, PUT, POST /avatar, POST /search              |
| `/api/v1/chat`       | 聊天     | POST /message, POST /search, GET /history, POST /group |
| `/api/v1/im`         | 即时通讯   | POST /connect, POST /online-status, GET /stream        |
| `/api/v1/message`    | 消息队列   | POST /send, POST /subscribe, GET /consume, POST /ack   |
| `/api/v1/bot`        | AI 机器人 | POST, GET, POST /message, GET /history                 |
| `/api/v1/knowledge`  | 知识图谱   | POST /entities, POST /relations, POST /search          |
| `/api/v1/search`     | 全文搜索   | POST, POST /documents, POST /indexes/:type             |
| `/api/v1/vector`     | 向量检索   | POST /collections, POST /vectorize, POST /search       |
| `/api/v1/question`   | 智能问答   | POST /ask, GET /history, POST /feedback                |
| `/api/v1/recommend`  | 推荐     | GET, GET /related/:entityId, POST /feedback            |
| `/api/v1/extraction` | 知识提取   | POST /tasks, POST /tasks/:id/execute                   |
| `/api/v1/collection` | 数据采集   | POST /data, POST /tasks, POST /tasks/:id/execute       |
| `/api/v1/process`    | 文档处理   | POST /file, POST /url, GET /documents                  |
| `/api/v1/summary`    | 消息总结   | POST /messages, POST /reply-candidates, POST /todos    |
| `/api/v1/mcp`        | MCP 工具 | POST /call, POST /tools, GET /tools                    |
| `/api/v1/moderation` | 内容审核   | POST /translate, POST /content, GET /records           |
| `/api/v1/billing`    | 计费     | POST /deposit, GET /account, GET /transactions         |
| `/api/v1/monitoring` | 监控     | POST /metric, POST /log, GET /alerts                   |
| `/api/v1/file`       | 文件上传   | POST /upload, DELETE, GET /url                         |

## 服务端口

| 领域        | 服务         | 端口     | 说明                       |
| --------- | ---------- | ------ | ------------------------ |
| Platform  | Gateway    | 8888   | API 网关，HTTP/WebSocket 入口 |
| Platform  | User       | 9001   | 用户注册/登录/信息管理             |
| Platform  | Monitoring | 9010   | 指标/日志/告警/服务状态            |
| Platform  | Billing    | 9015   | 计费/充值/交易/用量统计            |
| Messaging | IM         | 9011   | 连接管理/在线状态/心跳/广播          |
| Messaging | Chat       | 9012   | 消息收发/群组管理/已读/撤回          |
| Messaging | Contact    | 9013   | 好友关系/分组/黑名单              |
| Messaging | Message    | 9009   | Kafka 消息队列管理/订阅/消费       |
| AI        | Knowledge  | 9002   | 知识图谱（Neo4j + ES）         |
| AI        | Search     | 9003   | 全文搜索（Elasticsearch）      |
| AI        | Vector     | 9004   | 向量检索（Milvus）             |
| AI        | Question   | 9005   | 智能问答（RAG）                |
| AI        | Recommend  | 9006   | 个性化推荐                    |
| AI        | Extraction | 9007   | 知识提取（实体/关系/三元组）          |
| AI        | Collection | 9008   | 数据采集（数据源/任务/结果）          |
| AI        | Bot        | 9014   | AI 聊天机器人                 |
| AI        | Process    | 9016   | 文档处理（PDF/图片/音频/视频）       |
| AI        | Summary    | 9017   | 消息总结/回复候选/待办提取           |
| AI        | MCP        | 9018   | MCP 工具集成                 |
| AI        | Moderation | 9019   | 内容审核/翻译                  |
| <br />    | <br />     | <br /> | <br />                   |

## 技术栈

| 层级     | 技术                                            | 说明                          |
| ------ | --------------------------------------------- | --------------------------- |
| 编程语言   | Go 1.25                                       | 高性能后端                       |
| API 网关 | Gin + WebSocket                               | HTTP 路由 + 实时通信              |
| RPC 框架 | gRPC                                          | 服务间通信                       |
| 服务发现   | Etcd                                          | 服务注册与发现                     |
| 关系数据库  | PostgreSQL + GORM                             | 持久化存储                       |
| 图数据库   | Neo4j                                         | 知识图谱                        |
| 向量数据库  | Milvus                                        | 向量检索                        |
| 搜索引擎   | Elasticsearch                                 | 全文搜索                        |
| 缓存     | Redis                                         | 缓存/限流/会话                    |
| 消息队列   | Kafka                                         | 异步消息/事件驱动                   |
| 对象存储   | MinIO                                         | 文件/文档存储                     |
| AI 框架  | Eino (字节跳动)                                   | LLM + Embedding 集成          |
| 可观测性   | OpenTelemetry + Prometheus + Grafana + Jaeger | 链路追踪/指标/可视化                 |
| 服务治理   | 熔断/重试/限流/超时                                   | gobreaker + backoff + Redis |

## 项目结构

```
Logos/
├── cmd/                              # 服务入口
│   ├── platform/                     # Platform 领域
│   │   ├── gateway/                  #   API 网关
│   │   ├── user/                     #   用户服务
│   │   ├── billing/                  #   计费服务
│   │   └── monitoring/               #   监控服务
│   ├── messaging/                    # Messaging 领域
│   │   ├── im/                       #   即时通讯
│   │   ├── chat/                     #   聊天
│   │   ├── contact/                  #   联系人
│   │   └── message/                  #   消息队列
│   └── ai/                           # AI 领域
│       ├── knowledge/                #   知识图谱
│       ├── search/                   #   全文搜索
│       ├── vector/                   #   向量检索
│       ├── question/                 #   智能问答
│       ├── recommend/                #   推荐
│       ├── extraction/               #   知识提取
│       ├── collection/               #   数据采集
│       ├── bot/                      #   AI 机器人
│       ├── process/                  #   文档处理
│       ├── summary/                  #   消息总结
│       ├── mcp/                      #   MCP 工具
│       └── moderation/               #   内容审核
├── internal/                         # 业务逻辑
│   ├── service/                      # 服务实现 (handler/dao/model/service)
│   │   ├── platform/
│   │   │   ├── gateway/              #   路由/中间件/WebSocket
│   │   │   ├── user/
│   │   │   ├── billing/
│   │   │   └── monitoring/
│   │   ├── messaging/
│   │   │   ├── im/
│   │   │   ├── chat/
│   │   │   ├── contact/
│   │   │   └── message/
│   │   └── ai/
│   │       ├── knowledge/
│   │       ├── search/
│   │       ├── vector/
│   │       ├── question/
│   │       ├── recommend/
│   │       ├── extraction/
│   │       ├── collection/
│   │       ├── process/              #   含 parser/ (PDF/图片/音频/视频)
│   │       ├── bot/
│   │       ├── summary/
│   │       ├── mcp/
│   │       └── moderation/
│   ├── bot/                          # Bot 引擎
│   │   ├── agent/                    #   Agent 管理 (预设/编码/默认)
│   │   ├── coordinator/              #   多 Bot 协调器
│   │   ├── provider/                 #   模型 Provider 抽象
│   │   └── tools/                    #   工具系统 (Bot 工具 + MCP 工具)
│   ├── mcp_server/                   # MCP 工具实现
│   │   ├── calculator.go             #   计算器
│   │   ├── code_execution.go         #   代码执行
│   │   ├── filesystem.go             #   文件系统
│   │   ├── weather.go                #   天气查询
│   │   ├── websearch.go              #   Web 搜索
│   │   └── registry.go               #   工具注册表
│   └── models/                       # AI 模型封装
│       ├── asr/                      #   语音识别 (OpenAI ASR)
│       ├── video/                    #   视频处理 (FFmpeg)
│       └── vlm/                      #   视觉语言模型
├── idl/                              # Proto IDL 定义
│   ├── common.proto
│   ├── platform/                     #   user.proto, billing.proto, monitoring.proto
│   ├── messaging/                    #   chat.proto, im.proto, contact.proto, message.proto
│   └── ai/                           #   11 个 AI 服务 proto
├── proto_gen/                        # Proto 自动生成代码 (pb.go + grpc.pb.go)
├── pkg/                              # 共享库
│   ├── auth/                         #   gRPC 认证拦截器
│   ├── cache/                        #   Redis 缓存
│   ├── client/                       #   gRPC 客户端工厂 (16 个服务客户端)
│   ├── database/pgsql/               #   PostgreSQL (GORM)
│   ├── eino/                         #   Eino AI 框架管理器
│   ├── es/                           #   Elasticsearch
│   ├── governance/                   #   服务治理 (熔断/重试/限流/超时/LLM 治理)
│   ├── graph/                        #   Neo4j
│   ├── grpcserver/                   #   gRPC 服务器 (Etcd 注册/健康检查/Keepalive)
│   ├── jwt/                          #   JWT 认证
│   ├── logger/                       #   Zap 日志
│   ├── model/                        #   基础模型 (GORM 公共字段)
│   ├── mq/                           #   Kafka
│   ├── obs/                          #   OpenTelemetry 可观测性
│   ├── ratelimit/                    #   Redis 限流器
│   ├── register/                     #   Etcd 服务注册
│   ├── storage/                      #   MinIO 对象存储
│   └── vector/                       #   Milvus 向量数据库
├── config/                           # 配置
│   ├── config.go                     #   配置结构体 + Viper 加载
│   ├── config.yaml                   #   默认配置文件
│   ├── prometheus.yml                #   Prometheus 配置
│   └── otelcol-config.yaml           #   OpenTelemetry Collector 配置
├── script/                           # 脚本
│   ├── test_api.sh                   #   API 测试脚本
│   ├── test.sh / test.bat            #   测试启动脚本
│   └── bootstrap.sh                  #   引导脚本
├── docker-compose.yml                # Docker Compose 编排
├── Dockerfile                        # 多阶段构建 (17 个服务)
├── start.sh / start.bat              # 交互式管理脚本
├── .env.example                      # 环境变量示例
├── go.mod / go.sum                   # Go 模块
└── LICENSE                           # Apache 2.0
```

## 服务治理

项目内置了完整的微服务治理能力（`pkg/governance/`）：

| 能力     | 配置                           | 说明                |
| ------ | ---------------------------- | ----------------- |
| 超时控制   | 服务端 30s / 客户端 10s / LLM 120s | gRPC 拦截器自动注入      |
| 重试策略   | 3 次，200ms-5s 指数退避            | 可重试码自动识别          |
| 熔断器    | 5 次失败阈值 / 30s 超时 / 3 次成功恢复   | 基于 sony/gobreaker |
| 限流     | 100 RPS / 50 并发              | gRPC 服务端拦截器       |
| LLM 治理 | 120s 超时 / 特殊重试策略             | 专为大模型调用设计         |

Gateway 层额外提供多级限流：

| 限流策略  | 配置         |
| ----- | ---------- |
| 全局限流  | 120 次/分钟   |
| IP 限流 | 200 次/分钟   |
| 登录限流  | 5 次/分钟     |
| 问答限流  | 30 次/分钟    |
| 消息限流  | 50 次/分钟    |
| 突发保护  | 1000 次/5分钟 |

## 监控与可观测性

| 服务            | 地址                       | 说明       |
| ------------- | ------------------------ | -------- |
| Grafana       | <http://localhost:3000>  | 指标可视化仪表盘 |
| Prometheus    | <http://localhost:9090>  | 指标采集与存储  |
| Jaeger        | <http://localhost:16686> | 分布式链路追踪  |
| Kiali         | <http://localhost:20001> | 服务网格可视化  |
| MinIO Console | <http://localhost:9901>  | 对象存储管理   |
| Neo4j Browser | <http://localhost:7474>  | 图数据库管理   |

## 基础设施端口

| 服务             | 端口                   | 说明       |
| -------------- | -------------------- | -------- |
| PostgreSQL     | 5432                 | 关系数据库    |
| Redis          | 6379                 | 缓存       |
| Etcd           | 2379, 2380           | 服务发现     |
| Kafka          | 9093 (外部), 9092 (内部) | 消息队列     |
| ZooKeeper      | 2182                 | Kafka 依赖 |
| Elasticsearch  | 9200, 9300           | 搜索引擎     |
| Neo4j          | 7474, 7687           | 图数据库     |
| Milvus         | 19530, 9091          | 向量数据库    |
| MinIO          | 9000, 9901           | 对象存储     |
| OTEL Collector | 4317, 4318, 8889     | 遥测数据采集   |

## 配置说明

项目通过 `config/config.yaml` 加载配置，同时支持环境变量覆盖。环境变量优先级高于配置文件。

### 关键环境变量

| 环境变量             | 说明                    | 默认值                     |
| ---------------- | --------------------- | ----------------------- |
| `POSTGRES_HOST`  | PostgreSQL 地址         | localhost               |
| `POSTGRES_PORT`  | PostgreSQL 端口         | 5432                    |
| `REDIS_ADDR`     | Redis 地址 (host:port)  | localhost:6379          |
| `ETCD_ENDPOINTS` | Etcd 地址 (逗号分隔)        | localhost:2379          |
| `KAFKA_BROKERS`  | Kafka 地址 (逗号分隔)       | localhost:9093          |
| `MILVUS_ADDRESS` | Milvus 地址 (host:port) | localhost:19530         |
| `ES_ADDRESSES`   | Elasticsearch URL     | <http://localhost:9200> |
| `NEO4J_URI`      | Neo4j URI             | bolt://localhost:7687   |
| `MINIO_ENDPOINT` | MinIO 地址              | localhost:9000          |
| `ARK_API_KEY`    | AI 模型 API Key         | -                       |
| `ARK_MODEL`      | AI 模型名称               | deepseek-chat           |
| `JWT_SECRET`     | JWT 签名密钥              | (见 config.yaml)         |

## 构建

### Docker 构建

```bash
# 构建镜像
docker compose build

# 强制重新构建（不使用缓存）
docker compose build --no-cache

# 仅构建单个服务
docker compose build gateway
```

### 本地构建

```bash
# 编译所有服务
go build -o bin/gateway ./cmd/platform/gateway
go build -o bin/user ./cmd/platform/user
go build -o bin/chat ./cmd/messaging/chat
go build -o bin/bot ./cmd/ai/bot
# ... 其他服务同理
```

## License

[Apache License 2.0](LICENSE)
