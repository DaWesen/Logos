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
│  │  :8888   │  │  :9001   │  │  :9021   │  │  :9010   │            │
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
│  │  :9020   │  │  :9017   │  │  :9018   │  │  :9019   │            │
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
- **流式输出**：Bot 回复经 gRPC 服务端流 → Gateway SSE 全链路逐 token 推送到前端，打字机效果
- **RAG 知识库 Bot**：用户可上传多种格式的文档构建私有知识库，Bot 可基于知识库回答问题
- **混合检索**：`knowledge_search` 单次调用并发执行语义（Milvus）与关键词（ES）双路检索，RRF 倒数排名融合重排
- **结构感知分块**：文档入库按 Markdown 标题边界切分并合并短小节，超长小节降级为重叠窗口，检索上下文更完整
- **自动存入知识库**：Bot 聊天界面可开启"自动存入知识库"开关，每条对话自动向量化存入关联的 RAG 知识库
- **向量模型自动同步**：Bot 配置向量模型时自动同步到关联的 RAG 知识库，确保向量化一致性
- **MCP 工具集成**：Bot 可调用外部工具（天气查询、代码执行、Web 搜索、计算器等），扩展能力边界
- **MCP 代码执行沙箱**：多层防护——环境变量白名单（服务密钥不泄漏给被执行代码）、超时硬上限 + 进程树终止、输出/代码大小限额、全局并发限制；设置 `LOGOS_CODE_SANDBOX=docker` 时升级为容器级隔离（`--network none --memory 128m`，无网络、只读挂载）
- **语义缓存**：语义相似的重复问题直接返回缓存答案（Redis + 向量余弦相似度 ≥ 0.90 命中），省一次 LLM 调用、毫秒级响应；Redis 不可用时自动降级直通
- **长对话上下文压缩**：历史消息按 token 预算装载，超出预算的早期对话滚动摘要为「对话背景」注入（按会话缓存，均摊成本 O(新溢出消息数)）；摘要失败自动降级为截断，不阻塞主流程
- **Prompt 注入防护**：规则引擎检测疑似指令注入（中英文 15 类模式），命中时注入安全护栏而非拒绝（控制误伤）；RAG 检索结果统一加内容边界声明，防止知识库文档中的间接注入
- **多 Bot 协作**：支持配置多个不同人设/能力的 Bot，按场景自动路由或由用户指定
- **Plan-and-Execute 任务规划**：Bot 可开启规划模式（`enable_planner`）——复杂任务先由 Planner 生成显式步骤清单（1-8 步，可展示可审计），逐步交给带工具的 ReAct Agent 执行（单步内工具调用能力完整保留），步骤失败自动重规划一次而非整体失败，最后由 Synthesizer 综合作答；流式模式下规划与步骤进度实时推送到前端
- **记忆管理**：Bot 记住用户偏好与历史交互，跨会话保持上下文；支持 7 种记忆分类（preference/habit/fact/goal/relationship/style/other），手动添加的记忆受保护不被自动清理
- **记忆置信度生命周期**：LLM 抽取的记忆带证据引用（消息 ID，可审计；编造引用整条作废）与归一化置信度（异常值 → 0.5"不知道"）；跨轮次三态合并——确认 +0.05 封顶 0.98（重复确认 ≠ 绝对真理）、补充取更详细值、矛盾 −0.15 保底 0.2 并保留双结论不自动选边；注入 prompt 双档消费：< 0.5 不注入（agent 不会因为标注就不当真）、[0.5, 0.65) 标注「⚠︎证据较少」、类内按置信度降序、截断声明条数
- **消息总结**：一键总结群聊/单聊历史消息，生成要点摘要与待办提取
- **智能回复候选**：根据上下文生成回复候选，用户可一键选用
- **实时多语言翻译**：支持前端配置翻译模型（DeepSeek/OpenAI 等），不再硬编码
- **消息审核**：接入模型对消息内容进行实时审核与过滤；审核模型不可用或输出无法解析时按**失败关闭**处理，直接拒绝群聊发送而非放行

### 计费管理

- 支持用户提供自己的 API Key，也可使用平台模型
- 完整的计费记录与余额管理
- 多 Provider 计费支持（DeepSeek/OpenAI/Claude 等），根据模型名称自动推断 Provider
- **按真实 token 用量计费**：由 eino 采集模型响应里的 `ResponseMeta.Usage`（prompt/completion tokens），一次请求内的多轮调用（ReAct、Planner、反思）累计后按模型单价结算；供应商未返回 usage 时才回退本地估算，并在交易 metadata 用 `token_source`（`eino` / `estimate` / `client`）标记来源；估算占比经 `logos.llm.token_source.count` 指标统计，超过 20% 触发 `LLMTokenUsageEstimateRateHigh` 告警
- **支付单 + 签名回调 + 幂等入账**：用户只能创建支付单（`POST /billing/payments`），金额上限、支付方式白名单在服务端校验；支付渠道回调 `POST /billing/payment-callback` 走公开路由，但必须通过 HMAC-SHA256 验签（`PAYMENT_CALLBACK_SECRET`，未配置时拒绝所有回调），并校验回调时间戳 ±5 分钟、回调金额与订单金额一致；订单 `pending -> paid` 使用 `WHERE id=? AND status=0` 条件更新，重复/并发回调只有一次能入账，回调 nonce 加唯一索引防重放；过期订单在查询/回调时被条件更新为 `expired`，不会残留脏 pending；回调结果按 `result`（paid/invalid_signature/amount_mismatch/order_expired...）打点到 `logos.payment.callback.count`，超过阈值触发 `PaymentCallbackFailureRateHigh` 告警；原 `Deposit` 仅保留为内部/管理端 RPC，不再对普通用户开放
- **计费不随请求取消**：用户中途断开连接/切换会话会取消请求 ctx，计费使用 `context.WithoutCancel` 剥离取消信号并单独限时；流式会话即使因客户端中断提前返回，也按已消耗 token（真实 usage 优先）结算，`sync.Once` 保证一次会话只扣一次
- 交易记录正确展示金额与时间（兼容 proto Timestamp 对象格式）

### 系统监控

- **服务状态面板**：实时展示所有微服务运行状态（UP/DOWN/未上报）
- **双模式连接信息**：同时展示 etcd 服务发现名称和本地直连地址
- **指标/日志/告警**：支持按服务、类型、级别等多维度查询
- **自动采集**：Monitoring 服务启动后自动通过 etcd 采集各服务注册状态
- **请求上下文全链路透传**：Gateway 所有接口统一以 `c.Request.Context()` 调用下游 gRPC，客户端断开即取消下游计算（避免继续烧 LLM token），trace 与调用方身份 metadata 随链路传递

## 快速开始

### 环境要求

- Go 1.25+
- Docker & Docker Compose (推荐 v2+)
- 至少 8GB 可用内存
- FFmpeg（Process 服务视频抽帧功能依赖，容器内已内置）

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

### Kubernetes 部署（kind）

项目提供了完整的 Kubernetes 部署配置，使用 [kind](https://kind.sigs.k8s.io/) 在本地创建集群：

```bash
# Windows
cd deployment
.\deploy.ps1

# Linux / macOS
cd deployment
bash deploy.sh

# 销毁集群
.\destroy.ps1
```

部署脚本会自动完成：
1. 创建 kind 集群（`deployment/kind-cluster.yaml`）
2. 创建命名空间与 ConfigMap/Secret（`deployment/namespace.yaml`、`deployment/configmap.yaml`）
3. 依次部署基础设施（etcd/PostgreSQL/Redis/Kafka/MinIO/Milvus/Elasticsearch/Neo4j）
4. 部署全部 20 个微服务

### 配置 AI 模型

项目使用 [Eino](https://github.com/cloudwego/eino) 框架接入大模型，默认配置为 DeepSeek。通过环境变量配置（参见 `.env.example`）：

```bash
ARK_API_KEY=your-api-key
ARK_MODEL=deepseek-chat
ARK_BASE_URL=https://api.deepseek.com/
EMBEDDING_MODEL=embedding-3
```

或在 `docker-compose.yml` 中通过环境变量覆盖：

```yaml
environment:
  ARK_API_KEY: your-api-key
  ARK_MODEL: your-model-name
  ARK_BASE_URL: https://api.deepseek.com/
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

项目提供了交互式管理脚本（`script/bootstrap.sh`），支持引导式初始化部署环境。

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

| 路径前缀                 | 说明     | 主要端点                                                              |
| -------------------- | ------ | ----------------------------------------------------------------- |
| `/api/v1/users`      | 用户管理   | GET /:id, PUT, POST /avatar, POST /search                         |
| `/api/v1/chat`       | 聊天     | POST /message, POST /search, GET /history, POST /group            |
| `/api/v1/im`         | 即时通讯   | POST /connect, POST /online-status, GET /stream                   |
| `/api/v1/message`    | 消息队列   | POST /send, POST /subscribe, GET /consume, POST /ack              |
| `/api/v1/bot`        | AI 机器人 | POST, GET, POST /message, POST /message/stream (SSE), GET /history, POST /auto-save-kb |
| `/api/v1/bot/memory` | 记忆管理   | GET (查询), POST (添加), DELETE (删除)                                  |
| `/api/v1/knowledge`  | 知识图谱   | POST /entities, POST /relations, POST /search                     |
| `/api/v1/search`     | 全文搜索   | POST, POST /documents, POST /indexes/:type                        |
| `/api/v1/vector`     | 向量检索   | POST /collections, POST /vectorize, POST /search                  |
| `/api/v1/question`   | 智能问答   | POST /ask, GET /history, POST /feedback                           |
| `/api/v1/recommend`  | 推荐     | GET, GET /related/:entityId, POST /feedback                       |
| `/api/v1/extraction` | 知识提取   | POST /tasks, POST /tasks/:id/execute                              |
| `/api/v1/collection` | 数据采集   | POST /data, POST /tasks, POST /tasks/:id/execute                  |
| `/api/v1/process`    | 文档处理   | POST /file, POST /url, GET /documents                             |
| `/api/v1/summary`    | 消息总结   | POST /messages, POST /reply-candidates, POST /todos               |
| `/api/v1/mcp`        | MCP 工具 | POST /call, POST /tools, GET /tools                               |
| `/api/v1/moderation` | 内容审核   | POST /translate, POST /content, GET /records                      |
| `/api/v1/billing`    | 计费     | POST /payments, GET /payments/:id, POST /payment-callback, GET /account, GET /transactions |
| `/api/v1/monitoring` | 监控     | POST /metric, POST /log, GET /alerts, GET /services, GET /service-status/list |
| `/api/v1/file`       | 文件上传   | POST /upload, DELETE, GET /url                                    |

## 服务端口

| 领域        | 服务         | 端口     | 说明                       |
| --------- | ---------- | ------ | ------------------------ |
| Platform  | Gateway    | 8888   | API 网关，HTTP/WebSocket 入口 |
| Platform  | User       | 9001   | 用户注册/登录/信息管理             |
| Platform  | Monitoring | 9010   | 指标/日志/告警/服务状态            |
| Platform  | Billing    | 9021   | 计费/充值/交易/用量统计            |
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
| AI        | Bot        | 9020   | AI 聊天机器人/记忆管理            |
| AI        | Process    | 9016   | 文档处理（PDF/图片/音频/视频）       |
| AI        | Summary    | 9017   | 消息总结/回复候选/待办提取           |
| AI        | MCP        | 9018   | MCP 工具集成                 |
| AI        | Moderation | 9019   | 内容审核/翻译                  |

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
| 系统依赖   | FFmpeg / Python3 / Node.js                    | 视频抽帧/MCP代码执行沙箱              |

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
│   │   │       └── collector/        #     服务状态采集器（etcd）
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
│   │   ├── memory/                   #   记忆系统 (7种分类/自动清理/手动保护)
│   │   ├── provider/                 #   模型 Provider 抽象
│   │   └── tools/                    #   工具系统 (Bot 工具 + MCP 工具)
│   ├── mcp_server/                   # MCP 工具实现
│   │   ├── calculator.go             #   计算器
│   │   ├── code_execution.go         #   代码执行 (Python3/Node.js/Go)
│   │   ├── filesystem.go             #   文件系统
│   │   ├── http_template.go          #   HTTP 请求模板
│   │   ├── weather.go                #   天气查询
│   │   ├── websearch.go              #   Web 搜索
│   │   └── registry.go               #   工具注册表
│   └── models/                       # AI 模型封装
│       ├── asr/                      #   语音识别 (OpenAI ASR)
│       ├── video/                    #   视频处理 (FFmpeg 抽帧)
│       └── vlm/                      #   视觉语言模型
├── idl/                              # Proto IDL 定义
│   ├── common.proto
│   ├── platform/                     #   user.proto, billing.proto, monitoring.proto
│   ├── messaging/                    #   chat.proto, im.proto, contact.proto, message.proto
│   └── ai/                           #   12 个 AI 服务 proto (含 bot.proto 记忆管理)
├── proto_gen/                        # Proto 自动生成代码 (pb.go + grpc.pb.go)
├── pkg/                              # 共享库
│   ├── auth/                         #   gRPC 认证拦截器
│   ├── cache/                        #   Redis 缓存
│   ├── client/                       #   gRPC 客户端工厂 (17 个服务客户端)
│   ├── database/pgsql/               #   PostgreSQL (GORM)
│   ├── eino/                         #   Eino AI 框架管理器
│   ├── es/                           #   Elasticsearch
│   ├── governance/                   #   服务治理 (熔断/重试/限流/超时/LLM 治理)
│   ├── graph/                        #   Neo4j
│   ├── grpcserver/                   #   gRPC 服务器 (Etcd 注册/健康检查/Keepalive)
│   ├── jwt/                          #   JWT 认证
│   ├── logger/                       #   Zap 日志
│   ├── mq/                           #   Kafka
│   ├── obs/                          #   OpenTelemetry 可观测性
│   ├── outbox/                       #   Outbox 模式 (事件可靠投递)
│   ├── push/                         #   离线推送
│   ├── ratelimit/                    #   Redis 限流器
│   ├── register/                     #   Etcd 服务注册
│   ├── storage/                      #   MinIO 对象存储
│   ├── strutil/                      #   字符串工具
│   └── vector/                       #   Milvus 向量数据库
├── deployment/                       # Kubernetes 部署配置 (kind)
│   ├── kind-cluster.yaml             #   Kind 集群定义
│   ├── namespace.yaml                #   K8s 命名空间
│   ├── configmap.yaml                #   ConfigMap + Secret
│   ├── deploy.sh / deploy.ps1        #   部署脚本
│   ├── destroy.ps1                   #   销毁脚本
│   ├── infra/                        #   基础设施部署 (8 个组件)
│   └── services/                     #   微服务部署 (20 个服务)
├── config/                           # 配置
│   ├── config.go                     #   配置结构体 + Viper 加载
│   ├── grafana/                      #   Grafana 仪表盘与数据源配置
│   ├── prometheus.yml                #   Prometheus 配置
│   └── otelcol-config.yaml           #   OpenTelemetry Collector 配置
├── script/                           # 脚本
│   ├── bench/                        #   压测工具 (含 WebSocket 压测)
│   └── bootstrap.sh                  #   引导脚本
├── docker-compose.yml                # Docker Compose 编排
├── Dockerfile                        # 多阶段构建 (20 个服务，含 ffmpeg/python3/nodejs)
├── .env.example                      # 环境变量示例
├── go.mod / go.sum                   # Go 模块
└── LICENSE                           # Apache 2.0
```

## 服务治理

项目内置了完整的微服务治理能力（`pkg/governance/`）：

| 能力     | 配置                           | 说明                |
| ------ | ---------------------------- | ----------------- |
| 超时控制   | 服务端 30s / 客户端 30s / LLM 120s | gRPC 拦截器自动注入      |
| 重试策略   | 3 次，200ms-5s 指数退避            | 可重试码自动识别          |
| 熔断器    | 50 次失败阈值 / 10s 超时 / 3 次成功恢复   | 基于 sony/gobreaker |
| 限流     | 100 RPS / 50 并发              | gRPC 服务端拦截器（线程安全令牌桶） |
| LLM 治理 | 120s 超时 / 特殊重试策略             | 专为大模型调用设计         |

### 事务性发件箱（Outbox）

`pkg/outbox/` 实现与业务同事务写入的 at-least-once 事件投递，投递分三段执行，避免把 Kafka 网络往返放进数据库事务：

1. **短事务领取**：`SELECT ... FOR UPDATE SKIP LOCKED` 取一批消息，同时把 `next_retry_at` 写成租约截止时间，提交即释放行锁 —— 多副本部署不会重复领取同一批消息，进程崩溃时租约到期自动回到可领取状态
2. **事务外发送**：逐条 `producer.Send`，网络抖动不再长时间持有行锁与连接池连接
3. **单语句标记**：发送成功置 `sent`，失败按重试次数置回 `pending` 并设置退避；标记失败时消息会在租约到期后重投，属于 at-least-once 语义，消费端需保证幂等

- **失败重试**：30s 起步的指数退避（30s×2ⁿ，上限 32 分钟），最多 5 次
- **死信终态**：重试耗尽进入 failed 终态保留排查，并触发告警日志/告警钩子
- **死信重放**：每 10 分钟自动把 `replay_count < 3` 的死信重置回 pending，也支持人工确认后手工重放，避免毒消息被无限重投
- **自动清理**：已发送消息 24 小时后定期清理

### Kafka 消费可靠性

- **at-least-once**：消费端用 `FetchMessage` 只拉取不提交，handler 成功后才 `CommitMessages`；失败不提交，下次拉取到同一条消息重投
- **毒丸消息止损**：同一条消息（topic + partition + offset）连续失败 5 次后判定为毒丸，先转存到 `<topic>.dlq`（保留原始 topic/partition/offset/key/error 与原始负载），转存成功后才提交 offset；转存失败则落原始负载日志，保证可人工恢复
- **规范化**：消息按 `chat_id` 等 Key 哈希分区，同一会话有序；消费端通过数据库主键/唯一约束实现幂等

Gateway 层额外提供多级限流。各层按职责分档，阈值必须相互错开——若四层都写成同一个巨大值，实际生效的只有最宽松的一层：

| 限流维度 | 配置 | 说明 |
| --- | --- | --- |
| 全局限流 | 600 次/分钟 | 按 method + path 统计，防止单接口被整体打爆 |
| IP 限流 | 600 次/分钟 | 单 IP 全接口合计 |
| 登录/注册 | 5 次/分钟 | 按用户，防撞库 |
| AI 问答 | 30 次/分钟 | 触发 LLM 调用，成本最高 |
| 文档提取 | 60 次/分钟 | 解析/抽取重计算 |
| 集合任务 | 30 次/分钟 | 仅 POST/PUT，读列表不受限 |
| 消息发送 | 50 次/分钟 | 群聊写扩散到多个接收者 |
| 推荐查询 | 120 次/分钟 | |
| 常规读接口 | 600 次/分钟 | 默认档 |
| 突发保护 | 1000 次/5分钟 | 单 IP 超限后短期封禁 |

## 监控与可观测性

- **Request-ID 链路贯穿**：Gateway 为每个请求生成/透传 `X-Request-ID`（UUID），响应头回写、日志与 monitoring 上报统一携带，单请求全链路可串联排障
- **优雅停机**：全部 gRPC 微服务在 SIGTERM 时执行三段式下线——etcd 主动注销（新流量立即摘除，无需等 lease 过期）→ 健康检查置 NOT_SERVING → GracefulStop 等待 in-flight 请求排空（默认 25s 宽限期，`GRPC_SHUTDOWN_GRACE` 可调，小于 K8s terminationGracePeriodSeconds 保证不被 SIGKILL 撕断）；Gateway HTTP 侧并行关闭 TCP/WebSocket/EventBus 并超时兜底

### 可靠性指标与告警

`pkg/obs` 统一注册指标（指标初始化在各服务 `obs.InitGRPCProvider` 设置 MeterProvider 后惰性完成），经 OTel Collector 的 Prometheus exporter（`:8889`）暴露：

| 指标 | 类型 | 标签 | 含义 |
| --- | --- | --- | --- |
| `logos.kafka.consume.count` | Counter | `topic`、`result=ok/failed` | 消费成功/失败数 |
| `logos.kafka.consume.duration` | Histogram | `topic`、`result` | handler 处理耗时 |
| `logos.kafka.poison.count` | Counter | `topic` | 毒丸消息（已转存死信队列）数 |
| `logos.outbox.dispatch.count` | Counter | `topic`、`result=sent/failed` | Outbox 投递成功/失败数 |
| `logos.outbox.dispatch.duration` | Histogram | `topic`、`result` | Kafka 发送耗时 |
| `logos.outbox.deadletter.count` | Counter | `topic` | Outbox 重试耗尽进入死信的域事件数 |
| `logos.llm.parse.count` | Counter | `module`、`result=ok/failed` | AI 结构化输出解析成功/失败数（memory/moderation/summary/extraction） |

配套告警规则见 `deployment/examples/prometheus-rules.yaml` 的 `logos.reliability` 组：

- `KafkaPoisonMessage`（critical）：5 分钟内出现毒丸，需人工检查 `<topic>.dlq` 并回放
- `KafkaConsumeFailureRateHigh`（warning）：消费失败率 > 5%，通常意味着下游 DB/Redis 异常
- `OutboxDeadLetter`（critical）：域事件已停止投递，需人工确认后调用 `ReplayDeadLetters`
- `OutboxDispatchFailureRateHigh`（warning）：投递失败率 > 10%，通常意味着 Kafka 抖动
- `LLMParseFailureRateHigh`（warning）：LLM 输出解析失败率 > 20%，业务已在静默降级为空结果

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
| OTEL Collector | 4317, 4318, 8890          | 遥测数据采集   |

## 配置说明

项目通过 `config/config.go` 加载配置（Viper 框架），同时支持环境变量覆盖。环境变量优先级高于配置文件。

### 关键环境变量

| 环境变量                          | 说明                      | 默认值                     |
| ----------------------------- | ----------------------- | ----------------------- |
| `POSTGRES_HOST`               | PostgreSQL 地址           | localhost               |
| `POSTGRES_PORT`               | PostgreSQL 端口           | 5432                    |
| `REDIS_ADDR`                  | Redis 地址 (host:port)    | localhost:6379          |
| `ETCD_ENDPOINTS`              | Etcd 地址 (逗号分隔)          | localhost:2379          |
| `KAFKA_BROKERS`               | Kafka 地址 (逗号分隔)         | localhost:9093          |
| `MILVUS_ADDRESS`              | Milvus 地址 (host:port)   | localhost:19530         |
| `ES_ADDRESSES`                | Elasticsearch URL       | <http://localhost:9200> |
| `NEO4J_URI`                   | Neo4j URI               | bolt://localhost:7687   |
| `MINIO_ENDPOINT`              | MinIO 地址                | localhost:9000          |
| `MINIO_BUCKET`                | MinIO 存储桶               | logos-documents          |
| `ARK_API_KEY`                 | AI 模型 API Key           | -                       |
| `ARK_MODEL`                   | AI 模型名称                 | deepseek-chat           |
| `ARK_BASE_URL`                | AI 模型 API Base URL      | https://api.deepseek.com/ |
| `EMBEDDING_MODEL`             | Embedding 模型名称          | -                       |
| `JWT_SECRET`                  | JWT 签名密钥                | (见 config.yaml)         |
| `FFMPEG_PATH`                 | FFmpeg 可执行文件路径          | ffmpeg                  |
| `GRPC_CLIENT_TIMEOUT`         | gRPC 客户端超时 (秒)          | 30                      |
| `GRPC_SERVER_TIMEOUT`         | gRPC 服务端超时 (秒)          | 30                      |
| `CIRCUIT_BREAKER_FAILURE_THRESHOLD` | 熔断器失败阈值           | 50                      |
| `CIRCUIT_BREAKER_TIMEOUT`     | 熔断器超时 (秒)               | 10                      |
| `RETRY_MAX_ATTEMPTS`          | 最大重试次数                  | 3                       |
| `RETRY_INITIAL_DELAY`         | 重试初始延迟 (ms)             | 200                     |
| `MESSAGE_RATE_LIMIT`          | 消息接口限流 (次/分钟)           | 50                      |
| `RECOMMEND_RATE_LIMIT`        | 推荐接口限流 (次/分钟)           | 60                      |
| `LOGOS_CODE_SANDBOX`          | MCP 代码执行沙箱模式            | process                |

完整环境变量列表参见 `.env.example`。

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
