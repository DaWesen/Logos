# Logos 测试文档

本文档说明 Logos 项目的测试方法，包括 AI Bot 功能、知识库管理和 RAG 检索。

## 测试环境准备

### 基础设施启动

```bash
# 启动基础设施（PostgreSQL、Redis、etcd、Kafka、Milvus、MinIO、Neo4j）
docker compose up -d postgres redis etcd kafka milvus minio neo4j

# Milvus 需要等待约 30 秒初始化，确认就绪后再启动服务
```

### AI 服务启动（按顺序）

```bash
# 1. 向量服务（RAG 核心，必须先启动）
cd cmd/ai/vector && go run main.go

# 2. 提取服务（知识图谱）
cd cmd/ai/extraction && go run main.go

# 3. 文档处理服务
cd cmd/ai/process && go run main.go

# 4. Bot 服务
cd cmd/ai/bot && go run main.go

# 5. 网关（对外 API）
cd cmd/platform/gateway && go run main.go
```

### 前端启动

```bash
cd web
npm install
npm run dev
```

### 最小启动方案（快速测试）

如果只需要测试 Bot 对话 + RAG，启动以下服务即可：

```bash
# 确保 docker 基础设施运行后：
go run ./cmd/platform/gateway   # 端口 8888
go run ./cmd/ai/bot             # 端口 9020
go run ./cmd/ai/vector          # 端口 9004（RAG 必需）
go run ./cmd/ai/process         # 端口 9016（上传文档必需）
```

## 自动化测试

### 运行快速测试脚本

```bash
# Linux / macOS
bash script/test.sh

# Windows
script/test.bat
```

这个脚本会执行：
1. Go Vet 静态检查
2. 全项目编译检查
3. 逐服务编译检查
4. gRPC 健康检查注册验证

### 运行 API 测试脚本

```bash
bash script/test_api.sh
```

---

## 知识库管理测试（重点）

### 前置条件

开始前确认：
- ✅ vector 服务已启动
- ✅ process 服务已启动
- ✅ PostgreSQL、Milvus 连接正常（日志无报错）

### 1. 配置解析模型（通用配置）

#### 测试步骤：
1. 进入知识库页面
2. 点击顶部「解析模型」按钮
3. 配置以下四种模型（只需填需要用到的）：

| 模态 | 用途 | 推荐模型示例 |
|------|------|-------------|
| 🖼️ VLM 视觉语言模型 | 图片/文档理解 | `gpt-4o` / `qwen-vl-max` |
| 🧠 LLM 大语言模型 | 文本提取/摘要 | `gpt-4o-mini` / `deepseek-chat` |
| 🎙️ ASR 语音识别模型 | 音频转写 | `whisper-1` |
| 📐 Embedding 向量模型 | **RAG 核心** | `doubao-embedding-vision-250615` / `text-embedding-3-small` |

4. 填好 API Key 和 Base URL
5. 关闭弹窗（配置自动保存到本地）

#### 验证要点：
- ✅ 刷新页面后配置依然保留
- ✅ 所有 API Key 可以显示/隐藏切换

### 2. 创建向量集合（知识库）

#### 测试步骤：
1. 点击「新建集合」按钮
2. 填写集合名称（如"我的知识库"）
3. 选择向量模型（默认 SentenceBERT）、索引类型（默认 HNSW）、维度（默认 768）
4. 在模型配置区域：会自动从通用配置填入。可按需修改
5. 点击创建

#### 验证要点：
- ✅ 集合创建成功，显示在集合列表中
- ✅ 集合卡片显示配置的模型名称（VLM/LLM/ASR/Embedding）
- ✅ PostgreSQL `vector_collections` 表有对应记录
- ✅ 集合卡片显示向量数量

### 3. 上传文档测试

#### 测试步骤：
1. 在知识库页面，确保已选中刚创建的知识库
2. 点击「上传文档」
3. 选择一个文本文件（支持 .txt、.md、.pdf、.docx、图片、音频等）
4. 等待处理完成

#### 验证要点：
- ✅ 文档出现在文档列表中
- ✅ 状态从 `processing` → `completed`
- ✅ 日志显示：`解析完成` → `分块完成` → `向量化完成`
- ✅ vector 服务日志无 UTF-8 编码错误

**已知修复**：
- 修复了文件含非法 UTF-8 字节导致 `invalid byte sequence for encoding "UTF8"` 错误
- 修复了 chunk ID 重复导致 `duplicate key value violates unique constraint` 错误
- 修复了 document_chunks 表缺少 `updated_at` 字段的问题

### 4. URL 导入测试

#### 测试步骤：
1. 点击「URL 导入」
2. 输入网页 URL
3. 选择目标知识库
4. 点击导入

#### 验证要点：
- ✅ URL 内容被下载并处理
- ✅ 文档状态正常流转

### 5. 文档查看

#### 测试步骤：
1. 在文档列表找到已完成的文档
2. 点击「👁️」查看按钮

#### 验证要点：
- ✅ 弹出文档详情窗口
- ✅ 显示文档分块列表及内容
- ✅ 分块编号和类型正确

### 6. 文档重处理

#### 测试步骤：
1. 点击文档卡片的「重处理」按钮

#### 验证要点：
- ✅ 状态重新变为 `processing` 然后回到 `completed`
- ✅ 向量化也重新执行
- ✅ 旧 chunk 被删除，新 chunk 被创建

### 7. 删除与重建

#### 测试步骤：
1. 点击集合卡片的「🗑️」删除按钮
2. 刷新页面确认集合消失
3. 新建同名集合

#### 验证要点：
- ✅ Milvus 集合和 PostgreSQL 记录一并删除
- ✅ 重建后可以正常使用

**已知修复**：
- 修复了删除集合只删 Milvus 不删 PostgreSQL 导致"删不掉"的问题

---

## RAG 检索测试（Bot + 知识库）

### 前置条件
- ✅ 至少有一个知识库包含已完成的文档
- ✅ vector 服务运行正常
- ✅ bot 服务运行正常

### 1. Bot 关联知识库

#### 测试步骤：
1. 进入 Bot 管理页面
2. 创建新 Bot 或编辑已有 Bot
3. 在配置区域找到「RAG 检索」开关
4. **开启 RAG 开关**
5. 在下方勾选一个或多个知识库
6. 保存 Bot

#### 验证要点：
- ✅ Bot 卡片出现 RAG 标签
- ✅ 保存后 Bot 的 config 中有 `enable_rag: true` 和 `collection_ids`
- ✅ 查看数据库 `bots` 表的 config 字段确认

### 2. 知识库问答测试

#### 测试步骤：
1. 进入与 Bot 的聊天
2. 发送涉及知识库内容的问题，例如：
   - `"你还记得我写的诗吗"`
   - `"关于《渐变》那首诗的内容"`
   - `"帮我看看知识库里有什么"`

#### 验证要点：
- ✅ Bot 回复中引用了知识库的内容（而非通用知识）
- ✅ bot 日志显示：`RAG 查询成功，找到相关文档`
- ✅ vector 日志显示各个步骤耗时

### 3. 闲聊不触发 RAG

#### 测试步骤：
1. 发送短消息如 `"你好"`、`"一起去吃甜品吧"`
2. 发送无关键词的消息如 `"讲个故事"`

#### 验证要点：
- ✅ Bot 秒回（不触发 RAG）
- ✅ bot 日志没有 RAG 相关日志
- ✅ `isLikelyQuery` 函数返回 false，跳过检索

### 4. RAG 超时降级

#### 测试步骤：
1. 发送需要 RAG 的问题
2. 如果 Embedding API 响应慢（超过 8 秒）

#### 验证要点：
- ✅ 8 秒后不阻塞对话，直接走普通回复
- ✅ 日志显示 `RAG 查询失败，继续普通对话`

**机制说明**：
- `isLikelyQuery` 智能判断是否触发 RAG（含疑问词/知识词/长消息）
- RAG 超时仅 8 秒，快速失败不阻塞对话
- 搜索时优先使用知识库自定义的 Embedding 模型配置

---

## MCP 工具调用测试

### 前置条件
- ✅ bot 服务运行正常
- ✅ gateway 服务运行正常
- ✅ 前端已启动

### 1. MCP 外部服务管理

#### 测试步骤：
1. 进入 MCP 管理页面（侧边栏导航）
2. 查看「内置工具」列表，确认显示系统内置工具（如 knowledge_search、grep_chunks、mcp_calculator 等）
3. 点击「添加服务」
4. 填写外部 MCP 服务信息：
   - 名称：测试服务
   - 传输类型：SSE / HTTP Streamable
   - URL：`http://example.com/mcp`
   - 请求头（可选）：`{"Authorization": "Bearer xxx"}`
5. 点击「测试连接」
6. 点击「保存」

#### 验证要点：
- ✅ 内置工具列表正常显示
- ✅ 外部 MCP 服务可以添加成功
- ✅ 测试连接功能返回正常响应
- ✅ 服务列表显示刚添加的服务
- ✅ 可以编辑和删除服务

### 2. MCP 工具调用（前端直接调用）

#### 测试步骤：
1. 在 MCP 管理页面的「调用工具」区域
2. 从下拉框选择一个工具（如 `mcp_calculator`）
3. 在参数输入框中填写 JSON 格式参数，如 `{"a": 123, "b": 456}`
4. 点击「调用」按钮
5. 查看结果区域

#### 验证要点：
- ✅ 工具调用成功返回计算结果
- ✅ 调用日志表格显示调用记录（工具名、参数、结果、耗时、状态）
- ✅ 调用失败时显示错误信息

### 3. Bot 对话中调用 MCP 工具

#### 测试步骤：
1. 进入 Bot 聊天页面
2. 发送需要调用工具的消息，例如：
   - `"帮我算一下 123 * 456"`
   - `"查询一下 https://zh.moegirl.org.cn/xxx 里的大致信息"`
3. 观察 Bot 回复

#### 验证要点：
- ✅ Bot 一次对话直接返回完整结果（不需追问）
- ✅ Bot 日志中 `tools_count` 包含 MCP 工具（如 `mcp_calculator`、`mcp_http_request`）
- ✅ 日志 events 数 > 1（包含 tool_call 事件 + tool_result 事件 + 最终回复）
- ✅ **工具名称被自动 sanitize**：即使外部 MCP 服务名称或内置工具名称包含中文/特殊字符，也会自动过滤为 `[a-zA-Z0-9_-]`
- ✅ **无冗余中间文本**：Bot 不会说"让我查一下"之类的废话，直接返回最终结果

**验证通过**（2026-05-17 实测）：
- ✅ 外部 MCP 服务（百炼 WebSearch）的完整注册 → 注入 Bot → 对话调用链路
- ✅ 工具名称含中文/特殊字符不再导致 API 400 错误，自动 sanitize 生效
- ✅ Bot 一次对话直接返回完整结果，无"让我查一下"等冗余中间文本
- ✅ 最终答案逐 token 流式返回，工具执行期间用户端零输出
- ✅ 纯闲聊场景（无工具调用）回复不受影响

---

## MCP 功能测试（WeKnora 对比）

### 功能对比验证

| 功能 | Logos | WeKnora | 说明 |
|------|-------|---------|------|
| 内置工具注册 | ✅ | ✅ | knowledge_search、grep_chunks、MCP工具 |
| 外部服务连接 | ✅ | ✅ | SSE / HTTP Streamable |
| 服务管理 CRUD | ✅ | ✅ | 添加/查看/编辑/删除 |
| 测试连接 | ✅ | ✅ | 保存前验证连接 |
| 工具列表查询 | ✅ | ✅ | 列出所有可用工具 |
| 调用工具 | ✅ | ✅ | 通过 JSON-RPC 格式调用 |
| 调用日志 | ✅ | ❌ | 记录每次调用的参数、结果、耗时 |
| Bot 集成 | ✅ | ✅ | Agent 自动注入 MCP 工具 |
| 安全防护 | ❌ | ✅ | 鉴权、速率限制等 |

---

## AI Bot 功能测试

### 1. Bot 创建与配置

#### 测试步骤：
1. 登录系统
2. 进入 Bot 管理页面
3. 点击「创建 Bot」
4. 填写以下信息：
   - 名称：三号机
   - 描述：蔚蓝档案助手
   - 头像：选择 emoji 或上传图片
   - 系统提示：`你是一个蔚蓝档案助手，人设类似Arnoa。我是老师`
   - 模型提供商：选择一个（如 DeepSeek）
   - 模型名称：`deepseek-chat`
   - API Key：填写你的 API Key
   - Base URL：`https://api.deepseek.com/v1`
5. 点击创建

#### 验证要点：
- ✅ Bot 创建成功
- ✅ 头像正确保存
- ✅ 在 Bot 列表中能看到创建的 Bot

### 2. 人设更新测试

#### 测试步骤：
1. 在 Bot 管理页面点击「编辑」
2. 修改系统提示
3. 保存更改

#### 验证要点：
- ✅ 修改成功保存
- ✅ 新对话立即使用新人设

**已知修复**：
- 修复了 gateway 的 UpdateBot handler 未正确从 URL 路径提取 bot_id 的问题
- 日志应显示：`更新 Bot 请求` 和 `更新 Bot 成功`

### 3. 多轮对话测试

#### 测试步骤：
1. 点击 Bot 进入聊天
2. 发送第一条消息：`你好`
3. 发送第二条消息：`你是谁？`
4. 发送第三条消息：`你刚才说了什么？`

#### 验证要点：
- ✅ 第一条消息正常回复
- ✅ 第二条消息记住之前的对话
- ✅ 第三条消息能回顾整个对话历史

**已知修复**：
- 修复了前端 chat_id 与后端 conversation_id 不匹配的问题
- 修复了对话不存在时创建新对话使用前端传入 ID 的问题
- 日志应显示历史消息 count 递增：`构建历史消息, count: 3`

### 4. 记忆能力测试

#### 测试步骤：
1. 发送：`我喜欢吃冰淇淋`
2. 发送：`我的名字是老师`
3. 刷新页面（断开 WebSocket 连接）
4. 重新进入聊天
5. 发送：`我喜欢吃什么？`
6. 发送：`我叫什么名字？`

#### 验证要点：
- ✅ 能记住用户偏好
- ✅ 跨会话保持记忆
- ✅ 记忆存储在数据库 `user_memories` 表

**已知修复**：
- 修复了记忆提取 goroutine 使用请求 context 导致 `context canceled` 的问题
- 现在使用独立的 context 加 30s 超时
- 日志应显示：`自动提取记忆完成, count: X`

### 6. Agent 响应精简测试

测试 Agent 事件过滤机制，确保工具调用时不输出冗余中间文本。

#### 测试步骤：
1. 向有 MCP 工具或 RAG 的 Bot 发送需要调用工具的问题
2. 例如：
   - `"现在天气怎么样"`
   - `"帮我用计算器算一下 123 * 456"`
   - `"查询知识库里的内容"`
3. 观察 Bot 的完整回复过程

#### 验证要点：
- ✅ **无冗余中间文本**：Bot 不会输出"好的，让我查一下""我来搜索一下"等废话
- ✅ **无工具结果暴露**：工具执行的原始结果（如 JSON）不会暴露给用户
- ✅ **一次返回最终答案**：Bot 只在拿到完整结果后回复，中间不输出"等一下""马上好"等提示
- ✅ **流式逐 token 输出**：最终答案是逐 token 流式返回的，而非一次性全量返回
- ✅ **非工具场景不受影响**：纯闲聊场景（无工具调用）回复不受影响，正常返回

#### 调试方法：
- 查看 bot 日志中的 `Bot chat response` / `Bot stream chat request` 日志
- 日志中 `events` 数量可以反映 ReAct 循环的事件数（包含工具调用事件）
- 日志中的 `tool_calls` 字段可看到每次事件是否包含工具调用

**实现说明**：
- Agent 事件循环中通过 `Role == schema.Assistant` 和 `len(msg.ToolCalls) > 0` 过滤中间事件
- 工具调用后的最终 Assistant 消息（无 ToolCalls）才被返回给用户
- 启用 `EnableStreaming: true` 的流式 Runner，最终回答逐 token 输出

### 7. 头像显示测试

#### 测试步骤：
1. 查看 Bot 列表：确认头像显示
2. 进入聊天：确认聊天窗口头像显示
3. 发送消息：确认 Bot 回复中的头像显示

#### 验证要点：
- ✅ Bot 列表显示头像
- ✅ 聊天窗口头部显示头像
- ✅ 消息气泡显示头像

**已知修复**：
- 修复了左侧 Bot 分组列表头像写死使用默认 Bot 图标的问题
- 现在会检查 chat.avatar 是否存在，优先显示自定义头像

---

## 多模态模型配置测试

### 创建集合时配置模型

#### 测试步骤：
1. 打开「解析模型配置」，填好通用配置
2. 点击「新建集合」
3. 验证模型配置字段自动填入
4. 修改其中某个字段（如不同知识库用不同 Embedding 模型）
5. 创建集合

#### 验证要点：
- ✅ 通用配置自动填入
- ✅ 集合的模型配置存入 PostgreSQL
- ✅ 向量化时优先使用集合自定义配置

### 集合卡片展示模型信息

#### 验证要点：
- ✅ 集合卡片显示已配置的模型名称
- ✅ VLM/LLM/ASR/Embedding 各自独立显示

---

## 多服务集成测试

### 完整链路：上传 → 向量化 → RAG 检索

#### 测试步骤：
1. 在知识库页面上传一个文档（如一首诗）
2. 等待处理完成（状态 → completed）
3. 编辑 Bot → 开启 RAG → 勾选该知识库
4. 在聊天中问关于文档内容的问题

#### 验证要点：
- ✅ 文档解析成功（process 日志）
- ✅ 向量化成功（process 日志 + vector 日志）
- ✅ 向量存入 Milvus（vector 日志）
- ✅ Bot 对话时触发 RAG 检索（bot 日志）
- ✅ Bot 回复引用了知识库内容

### 完整链路流程图

```
用户上传文档
  → Process 服务解析、分块
  → Process 调用 Vector 服务的 Vectorize API
  → Vector 服务调用 Embedding API 生成向量
  → 向量存入 Milvus
  → 文档状态标记为 completed

用户与 Bot 对话（开启 RAG）
  → Bot 服务调用 Vector 服务的 TextSearch API
  → Vector 服务用集合配置的 Embedding 模型做向量化
  → Vector 服务搜索 Milvus 获取相似内容
  → 内容返回给 Bot 服务
  → Bot 服务将内容拼入 prompt
  → LLM 基于知识库内容回答
```

---

## Bot 自动存入知识库测试

### 前置条件
- ✅ Bot 已关联至少一个 RAG 知识库
- ✅ Bot 已配置向量模型（Embedding）
- ✅ vector 服务运行正常

### 1. 自动存入知识库开关测试

#### 测试步骤：
1. 进入与 Bot 的聊天界面
2. 观察聊天输入区域上方的"自动存入知识库"开关
3. 开启开关
4. 发送一条消息，如 `"今天天气不错"`
5. 等待 Bot 回复

#### 验证要点：
- ✅ 开关默认关闭，可手动开启
- ✅ 开启后发送消息，Bot 回复正常
- ✅ 后端日志显示 `自动存入知识库` 相关日志
- ✅ 对话内容被向量化并存入关联的 RAG 知识库

### 2. 关闭开关测试

#### 测试步骤：
1. 关闭"自动存入知识库"开关
2. 发送消息
3. 等待 Bot 回复

#### 验证要点：
- ✅ Bot 正常回复
- ✅ 对话内容不被存入知识库
- ✅ 后端日志无自动存入相关记录

---

## 计费与交易记录测试

### 1. 交易记录显示测试

#### 测试步骤：
1. 使用 Bot 发送消息（使用平台模型或自有 API Key）
2. 进入钱包页面
3. 查看交易记录

#### 验证要点：
- ✅ 交易金额正确显示（金额为0时不显示正负号）
- ✅ 交易时间正确显示（非 Invalid Date）
- ✅ Provider 显示正确（如 deepseek 而非 openai）
- ✅ 交易类型标签正确（充值/消费/退款）

### 2. 多 Provider 计费测试

#### 测试步骤：
1. 配置 Bot 使用 DeepSeek 模型
2. 发送消息
3. 查看交易记录

#### 验证要点：
- ✅ 交易记录描述显示 `模型调用：deepseek deepseek-chat`
- ✅ Provider 自动根据模型名称推断（deepseek-chat → deepseek）

---

## 系统监控测试

### 前置条件
- ✅ monitoring 服务已启动（含 collector）
- ✅ etcd 已配置且连接正常
- ✅ 至少有一个其他微服务已注册到 etcd

### 1. 服务状态面板测试

#### 测试步骤：
1. 进入系统观测页面
2. 查看"服务状态"标签页

#### 验证要点：
- ✅ 服务卡片显示服务名称
- ✅ 每个服务卡片显示"服务发现"（etcd 注册名称，如 logos.user）
- ✅ 每个服务卡片显示"直连地址"（如 127.0.0.1:9001）
- ✅ 已注册到 etcd 的服务显示"运行中"状态
- ✅ 未注册到 etcd 的服务显示"未上报"状态（半透明卡片）
- ✅ 服务总数、运行中、已停止统计正确

### 2. 服务发现与直连地址测试

#### 测试步骤：
1. 查看任意服务卡片的连接信息区域
2. 确认"服务发现"和"直连地址"都有值

#### 验证要点：
- ✅ 服务发现名称与 etcd 注册名一致（如 logos.bot）
- ✅ 直连地址格式为 `127.0.0.1:端口`
- ✅ Gateway 服务无 etcd 名称（不注册到 etcd），但仍显示直连地址

### 3. 指标/日志/告警查询测试

#### 测试步骤：
1. 切换到"指标"标签页
2. 选择服务名称和类型进行查询
3. 切换到"日志"标签页
4. 选择服务名称和级别进行查询
5. 切换到"告警"标签页
6. 查看告警列表

#### 验证要点：
- ✅ 服务下拉列表包含所有 19 个微服务
- ✅ 查询结果正确展示
- ✅ 无数据时显示友好的空状态提示

---

## Bot 聊天历史恢复测试

### 1. 刷新后消息恢复测试

#### 测试步骤：
1. 与 Bot 进行多轮对话
2. 刷新页面
3. 重新进入与 Bot 的聊天

#### 验证要点：
- ✅ 所有历史消息正确恢复（包括用户消息和 Bot 回复）
- ✅ 消息不重复
- ✅ Bot 回复的消息标记为 Bot 消息（头像和名称正确）
- ✅ 消息时间正确显示

### 2. 附件上传测试

#### 测试步骤：
1. 在聊天中上传一个 txt 文件
2. 查看消息气泡中的附件显示
3. 刷新页面
4. 再次查看附件显示

#### 验证要点：
- ✅ 上传后附件文件名正确显示（非"文件"）
- ✅ 刷新后附件文件名正确显示（非 {}）
- ✅ 附件图标和文件类型匹配

---

## 翻译功能测试

### 1. 前端配置翻译模型测试

#### 测试步骤：
1. 进入 AI 设置页面
2. 配置翻译模型（如 DeepSeek）
3. 在聊天中使用翻译功能

#### 验证要点：
- ✅ 翻译使用前端配置的模型（非硬编码 OpenAI）
- ✅ 后端日志显示正确的 provider（如 deepseek 而非 openai）
- ✅ 翻译结果正确

---

## 问题排查指南

### Bot 不回复？

检查日志：
```bash
# Gateway 日志
go run ./cmd/platform/gateway

# Bot 服务日志
go run ./cmd/ai/bot
```

常见问题：
1. **模型配置错误**：检查 API Key、Base URL、模型名称
2. **对话 ID 不匹配**：确认前端使用 `bot-{bot_id}` 格式
3. **记忆提取超时**：查看是否有 `context canceled` 错误
4. **工具名称含非法字符**：如日志报错 `Invalid 'tools[X].function.name': string does not match pattern`，说明外部 MCP 服务名称或工具名包含中文/特殊字符，已在 `sanitizeName` 中自动过滤，检查 MCP 服务配置名称是否合理

### RAG 检索失败？

检查日志：
```bash
# Bot 日志：是否有 RAG 查询失败？
# Vector 日志：是否有 TextSearch 请求到达？哪一步超时？
```

关键日志字段：
- `RAG 查询成功，找到相关文档` → RAG 工作正常
- `RAG 查询失败` → 向量化超时或 Milvus 异常
- `[向量] GetCollection` → 数据库查询耗时
- `[向量] 自定义Embedding API调用` → Embedding API 耗时
- `[向量] Milvus搜索` → 向量搜索耗时

### Bot 回复拖沓（说"让我查一下"等废话）？

检查：
1. 确认已应用 Agent 事件过滤修复（2026-05-17）
2. 查看 bot 服务 `agent.go` 中的 `chatWithMessages` 和 `chatStreamWithMessages` 方法
3. 日志中应不再出现跳过中间 Assistant 消息的日志
4. 如果仍有冗余文本，检查 system prompt 中的工具调用规则是否生效

### 文档处理失败？

检查 process 服务日志：
- `开始解析文档` → `文档解析完成`
- `开始向量化` → `向量化完成`
- `文档处理完成`（状态变为 completed）
- 如果出现 ERROR：查看具体错误信息（编码/主键重复/SQL 错误）

### 头像不显示？

检查：
1. 数据库中 Bot 的 avatar 字段是否有值
2. 前端是否正确获取并传递 avatar
3. 如果是 URL，检查是否能正常访问

### 历史记忆丢失？

检查：
1. 数据库 `user_memories` 表是否有数据
2. 日志是否有 `自动提取记忆完成`
3. 确认构建历史消息时包含了记忆提示

---

## 测试数据示例

### 数据库检查

```sql
-- 查看创建的 Bot
SELECT * FROM bots WHERE name = '三号机';

-- 查看对话记录
SELECT * FROM conversations WHERE id LIKE 'bot-%';

-- 查看用户记忆
SELECT * FROM user_memories WHERE user_id = 2;

-- 查看向量集合（知识库）
SELECT id, name, vlm_model, llm_model, asr_model, embedding_model FROM vector_collections;

-- 查看文档
SELECT id, file_name, status, vector_collection_id FROM documents;

-- 查看文档分块
SELECT id, chunk_index, LEFT(content, 50) AS preview FROM document_chunks WHERE document_id = 'xxx';
```

### API 测试

```bash
# 测试 Bot 对话
curl -X POST http://localhost:8888/api/v1/bot/message \
  -H "Authorization: Bearer <your-token>" \
  -H "Content-Type: application/json" \
  -d '{
    "bot_id": "8a78e875-d451-4be8-bac0-8bb2b53a39c6",
    "content": "你好",
    "user_id": "2",
    "chat_id": "bot-8a78e875-d451-4be8-bac0-8bb2b53a39c6"
  }'

# 查看向量集合
curl -H "Authorization: Bearer <your-token>" \
  http://localhost:8888/api/v1/vector/collections

# 文本搜索（RAG 内部调用）
curl -X POST http://localhost:8888/api/v1/vector/text-search \
  -H "Authorization: Bearer <your-token>" \
  -H "Content-Type: application/json" \
  -d '{
    "collection_id": "col_xxx",
    "text": "查询内容",
    "top_k": 5
  }'
```

---

## 已修复问题列表

| 问题 | 位置 | 修复时间 |
|-----|------|---------|
| UpdateBot 未提取 URL 中的 bot_id | gateway/handler_bot.go:48 | 2026-05-13 |
| 对话 ID 不匹配导致多轮失败 | bot/service/bot.go | 2026-05-13 |
| 记忆提取 context canceled | memory/memory.go | 2026-05-13 |
| Bot 列表头像不显示 | ChatPage.tsx | 2026-05-13 |
| 删除集合后 PostgreSQL 记录残留 | vector/dao/vector.go | 2026-05-14 |
| StatusCode 0 vs 200 导致向量化误判失败 | pkg/client/vector_client.go | 2026-05-14 |
| gRPC too_many_pings 连接断开 | pkg/grpcserver/server.go | 2026-05-14 |
| 文件含非法 UTF-8 字节插入失败 | text_parser.go / process.go | 2026-05-14 |
| chunk ID 重复导致主键冲突 | process/service/process.go | 2026-05-14 |
| document_chunks 缺少 updated_at 字段 | process/model/document.go | 2026-05-14 |
| 向量化和知识提取串行阻塞 | process/service/process.go | 2026-05-14 |
| Milvus Search 为占位符（空实现） | pkg/vector/milvus.go | 2026-05-14 |
| 前端未发送 knowledgeBaseIds 给后端 | web/src/api/bot.ts | 2026-05-14 |
| RAG 缺失 enable_rag 开关检查 | bot/service/bot.go | 2026-05-14 |
| RAG 检索不返回文档 content 字段 | pkg/vector/milvus.go | 2026-05-14 |
| RAG 超时过长（60s）阻塞对话 | bot/service/bot.go | 2026-05-14 |
| TextSearch 删除集合级 Embedding 配置读取 | vector/dao/vector.go | 2026-05-14 |
| ListVectors 返回 HTTP 500 导致前端预览失败 | gateway/handler_vector.go | 2026-05-15 |
| Milvus 限流导致批量向量化失败 | docker-compose.yml | 2026-05-15 |
| 分块 ID 重复导致 duplicate key | process/service/process.go | 2026-05-15 |
| UTF-8 非法字节导致向量搜索 500 | process/service/process.go / strutil.go | 2026-05-15 |
| 图片解析未使用集合 VLM 配置（始终走 OpenAI） | process/service/process.go | 2026-05-15 |
| 视频解析未使用集合 VLM 配置 | process/parser/parser.go | 2026-05-15 |
| VLM baseURL 拼接错误（多余 /responses） | models/vlm/remote_api.go | 2026-05-15 |
| VLM 错误响应被静默忽略 | process/parser/image_parser.go | 2026-05-15 |
| VLM 图片描述 prompt 过于简略 | process/parser/image_parser.go | 2026-05-15 |
| URL 导入未注册 crawl 类型解析器 | process/parser/crawler_parser.go | 2026-05-15 |
| 向量预览缺少 ListVectors RPC 实现 | idl/ai/vector.proto / pkg/vector/milvus.go | 2026-05-15 |
| Milvus QueryVectors 过滤表达式错误（字符串类型用双引号） | pkg/vector/milvus.go | 2026-05-15 |
| Bot 消息 media_meta 为空字符串导致 json 类型错误（SQLSTATE 22P02） | bot/model/bot.go | 2026-05-16 |
| Bot Agent 未注入 MCP 工具（tools_count 只有 2） | bot/service/bot.go | 2026-05-16 |
| proto_gen/mcp/mcp.pb.go 中 fmt.Sprintf 递归调用和锁拷贝 | mcp/mcp.pb.go | 2026-05-16 |
| MCP 前端页面缺少调用工具和调用日志功能 | web/src/pages/MCPPage.tsx | 2026-05-16 |
| MCP 缺少外部服务连接功能（管理端+前端） | mcp/manager.go / mcp/service/ | 2026-05-16 |
| LLM 只说"我要用工具"但不实际通过 function_call 调用 | bot/agent/agent.go | 2026-05-16 |
| 外部 MCP 工具名称含中文导致 API 400 错误 | bot/tools/mcp_tool.go / bot_tool.go | 2026-05-17 |
| Agent 工具调用前输出冗余中间文本（"让我查一下"等废话） | bot/agent/agent.go | 2026-05-17 |
| 流式场景 fullResponse 使用 += 低效拼接 | bot/service/bot.go | 2026-05-17 |
| Bot 向量模型未同步到 RAG 知识库 | bot/service/bot.go | 2026-05-22 |
| Bot 聊天缺少"自动存入知识库"开关 | ChatPage.tsx / bot/service/bot.go | 2026-05-22 |
| 附件上传文件名显示为"文件"或刷新后变为 {} | ChatBubble.tsx / chat.ts | 2026-05-22 |
| 翻译服务硬编码 OpenAI，未使用前端配置的模型 | moderation/service/moderation.go / chat.ts | 2026-05-22 |
| 交易记录显示 -0.000000 和 Invalid Date | billing.ts / WalletPage.tsx | 2026-05-22 |
| 交易记录 provider 显示错误（openai 应为 deepseek） | bot/handler/handler.go / bot/service/bot.go | 2026-05-22 |
| Bot 聊天刷新后消息重复（历史消息从错误 API 获取） | ChatPage.tsx / bot.ts | 2026-05-22 |
| 监测页面所有微服务显示"未上报"（collector 未启动） | monitoring/main.go | 2026-05-22 |
| 监测页面缺少服务发现名称和直连地址显示 | MonitoringPage.tsx / handler_monitoring.go | 2026-05-22 |
| 监控采集器 noahServices 列表不完整（仅 10/18 个服务） | monitoring/collector/collector.go | 2026-05-22 |
| 前端 ALL_SERVICES 未定义导致运行时错误 | MonitoringPage.tsx | 2026-05-22 |
| proto Timestamp 对象未正确解析为日期字符串 | billing.ts / ChatPage.tsx | 2026-05-22 |
| getBotHistory 未正确提取 messages 数组 | bot.ts | 2026-05-22 |

---

## 已知限制

### 当前已知问题
1. **Embedding API 调用较慢**（~8-20s）：取决于上游 API 响应速度，建议选择响应快的 Embedding 服务
2. **Kafka 主题自动创建**：billing_events 等主题需在 Kafka 中预先创建，否则发送事件会失败（不影响计费本身）
3. **Search 结果仅返回文本内容**：Milvus 搜索结果暂未返回 metadata（后续可扩展）

### 设计决策
- RAG 使用**快速失败**策略（8s 超时），而非长时间等待
- 使用 `isLikelyQuery` 智能判断是否触发 RAG，避免每条消息都检索
- 知识库支持**多集合**，Bot 可同时关联多个知识库
- 每个集合可独立配置 Embedding 模型（在知识库创建时设置）

---

## 下一步测试建议

1. **多知识库 RAG**：创建多个知识库，Bot 同时勾选，验证跨库检索
2. **并发文档上传**：同时上传多个文档，验证 process 服务并发处理能力
3. **大文档测试**：上传 10MB+ 的大文件，验证分块和向量化性能
4. **多 Bot 并发对话**：多个用户同时与不同 Bot 对话
5. **自定义 Embedding 模型**：为不同知识库配置不同的 Embedding 模型
6. **MCP 外部服务连接**：测试连接外部 MCP 服务（SSE/HTTP Streamable），验证工具注册和调用
7. **Bot + MCP 工具调用**：验证 Bot 对话中 LLM 自动调用 MCP 工具，一次对话直接返回完整结果（无"让我查一下"等冗余文本）
8. **MCP 调用日志**：验证前端调用日志记录正确
9. **知识图谱提取**：测试知识图谱从文档中提取实体和关系
10. **WebSocket 推送**：验证异步文档处理状态推送
11. **压力测试**：高并发 RAG 查询下的系统稳定性
