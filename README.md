# Logos
### AI-Powered Instant Messaging Platform

> λóγος — 法则、理性、道

## 项目简介

Logos 是一个基于微服务架构的 AI 增强型即时通讯平台，集成了知识图谱、RAG 问答、向量检索、智能推荐等 AI 能力，提供完整的即时通讯与智能助手服务。

## 领域架构

```
Platform (平台基础设施)          Messaging (即时通讯)            AI (智能引擎)
├── gateway  API 网关            ├── im       连接管理           ├── knowledge  知识库
├── user     用户认证            ├── chat     会话管理           ├── question   智能问答
└── monitoring 监控              ├── contact  联系人             ├── search     全文搜索
                                 └── message  消息存储           ├── vector     向量检索
                                                     ├── recommend  推荐
                                                     ├── extraction 提取
                                                     └── collection 集合
```

## 快速开始

### 环境要求

- Go 1.25+
- Docker & Docker Compose
- Kitex CLI (`go install github.com/cloudwego/kitex/tool/cmd/kitex@latest`)

### 启动

```bash
# 一键启动（推荐）
./start.sh          # Linux/macOS
start.bat           # Windows

# 或手动启动
docker-compose up -d                              # 启动基础设施 + 所有服务
make build && make run-gateway                    # 本地开发模式
```

### 按领域构建

```bash
make build-platform     # 只构建 Platform 领域 (gateway/user/monitoring)
make build-messaging    # 只构建 Messaging 领域 (im/chat/contact/message)
make build-ai           # 只构建 AI 领域 (knowledge/question/search/...)
```

### 生成 Kitex 代码

```bash
make generate-idl       # 从 IDL 生成所有 kitex_gen 代码
```

## 技术栈

| 层级 | 技术 |
|------|------|
| 网关 | Hertz (HTTP) + WebSocket |
| RPC | Kitex (Thrift) |
| 注册中心 | Etcd |
| 数据库 | PostgreSQL + Neo4j + Milvus + Redis + Elasticsearch |
| 消息队列 | Kafka |
| 对象存储 | MinIO |
| AI 框架 | Eino (字节跳动) |
| 可观测性 | OpenTelemetry + Prometheus + Jaeger |

## 服务端口

| 领域 | 服务 | 端口 |
|------|------|------|
| Platform | Gateway | 8888 |
| Platform | User | 9001 |
| Platform | Monitoring | 9010 |
| Messaging | IM | 9011 |
| Messaging | Chat | 9012 |
| Messaging | Contact | 9013 |
| Messaging | Message | 9009 |
| AI | Knowledge | 9002 |
| AI | Search | 9003 |
| AI | Vector | 9004 |
| AI | Question | 9005 |
| AI | Recommend | 9006 |
| AI | Extraction | 9007 |
| AI | Collection | 9008 |

## 项目结构

详细架构设计见 [docs/architecture.md](docs/architecture.md)

```
cmd/{platform,messaging,ai}/     # 服务入口（按领域分组）
internal/{platform,messaging,ai}/ # 业务逻辑（按领域分组）
idl/{platform,messaging,ai}/     # Thrift IDL（按领域分组）
kitex_gen/                       # Kitex 自动生成代码
pkg/                             # 跨领域共享包
config/                          # 配置文件
docs/                            # 架构文档
```

## License

MIT
