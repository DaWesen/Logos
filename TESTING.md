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

### 单元测试（无需基础设施）

纯逻辑测试不依赖 PostgreSQL / Kafka / Redis，可直接运行：

```bash
go test ./pkg/... ./internal/service/ai/bot/service/...
```

当前覆盖的核心点：

- **可靠性链路**：`pkg/outbox` 退避序列与 `FetchPending` 生成 SQL（`FOR UPDATE SKIP LOCKED`、状态与退避过滤、先进先出）；`pkg/mq` 死信信封构造（合法 JSON 原样保留、非法负载包装）、毒丸消息连续失败计数
- **越权防护**：`internal/service/ai/bot/service` 用内存 stub repo 验证 Bot / 对话 / 提示词的归属校验（非所有者按“不存在”处理、公开与预设提示词的可见性规则），并验证删除 Bot 时同步清理 `qq:bot_bind:` 路由缓存
- **空身份不得触达 DAO**：`CreateBot` / `CreateConversation` / `CreatePrompt` / `GetUserMemory` / `SetUserMemory` / `DeleteUserMemory` / `DeleteAllUserMemories` 在缺少 metadata 身份时返回 `ErrUnauthenticated`，且 stub repo 的 `createdBots` / `convsQueried` / `promptsQueried` / `memoriesQueried` / `setMemories` / `deletedMemories` 保持为空（回归「空 userID → DAO 不加过滤 → 全库泄露」）；`ListConversations` / `ListPrompts` 同样在触库前拒绝
- **请求体身份不能覆盖认证身份**：`userCtx("alice")` 搭配请求体 `user_id="bob"` 时拒绝并记 Warn；`ListConversations` 下发给 DAO 的过滤条件必须是 `|alice`（botID 为空 + 认证身份），被拒请求不得再查库
- **会话必须挂在自己的 Bot 上**：alice 用自己的身份 + `bot-other`（bob 的 Bot）建会话返回 `ErrBotNotFound` 且不落库；`bot-owner` 建会话成功且 `UserID=alice`、`BotID=bot-owner`
- **Bot handler 未认证映射 401**：`statusCodeForError` 对 `service.ErrUnauthenticated` 返回 401，其余返回 1；网关 `mapStatusCode` 对 `100..599` 直通，未认证不会再被伪装成 500
- **限流分档**：`gateway/middleware` 验证各接口阈值互不相同（登录 5 / 问答 30 / 消息 50 / 默认 600），避免四层同值导致限流失效
- **突发封禁**：已封禁 IP 在自增计数前直接拒绝；超限后写入 `blocked` 键且 TTL 为窗口 2 倍；模拟 burst 计数窗口过期后仍继续拒绝（回归「只写不读」缺陷）；Redis 故障时退化为单机窗口且超限仍拒绝；首次自增设置 burst TTL
- **令牌桶补充**：高频请求（100ms 一次、rate=1/s）20s 内至少放行 20 次，证明分数令牌没有被丢弃；补充量不超过容量上限；时钟回拨既不补充也不扣减；Lua 脚本中不得出现 `math.floor` 作用于补充量；`rate/capacity <= 0` 归一化为 1；桶键 TTL 不短于补满耗时；Lua `tostring` 产生的科学计数法时间戳可被解析，缺少时间时不会按 `now-0` 把桶补满
- **群禁言**：`chat/service` 验证永久禁言、临时禁言未到期、已到期、缺少截止时间四种状态的发送拦截
- **AI 链路**：记忆合并、RRF 混合检索、提示词守卫、语义缓存、上下文压缩、planner、分块、熔断与重试
- **工具执行器**：只读工具瞬时错误重试、副作用工具不重试、panic 不重试、连续失败熔断、半开恢复/再熔断、调用方取消不触发熔断、缓存命中绕过熔断、同参等待者随自己的 ctx 取消
- **QQ 媒体中转**（`internal/service/messaging/qqbridge`）：对象键随机化且不含上游文件名、按内容嗅探纠正扩展名、HTML 伪装图片降级为 `application/octet-stream`、`Content-Length` 超限提前拒绝、伪造路径穿越文件名不生效、上传阶段随 ctx 取消、上游失败保留 `[图片]` 文本占位且不写半成品 URL、临时目录只清理超过 1 小时的遗留文件（正在下载的文件不会被误删）、并发槽位耗尽时随 ctx 超时且成功后必定释放
- **出站媒体地址**：相对路径必须拼上 `gateway.public_base_url`；已有绝对 URL 原样透传；`file://` 等非 http(s) scheme 拒绝；未配置公网基址时只发文本、不发坏链接
- **配置环境变量**：`MINIO_BUCKET`、`QQBRIDGE_TEMP_DIR`、`LOGOS_PUBLIC_BASE_URL` 确实能覆盖配置文件值，环境变量为空时保留文件值
- **文件代理安全**：`SafeProxyContentType` 只允许媒体/文档内联；`text/html`、`image/svg+xml`、XHTML、JavaScript 与未知类型统一降级为 `application/octet-stream` 并强制下载；空类型与带 charset 的类型都能正确归一化
- **对象键校验**：合法 UUID 路径通过；绝对路径、空路径段、`..`、`.`、反斜杠和控制字符全部拒绝
- **QQ→Bot 路由缓存**：启动预热写入全部绑定；单个 key 写失败不中断其余绑定；不完整绑定跳过；缓存命中不查库；缓存未命中/Redis 故障时回源并回填
- **QQ 出站消息**：相对媒体路径拼成绝对 URL；绝对 URL 原样透传；`file://` 等 scheme 拒绝；纯图片消息生成 image 段；语音/视频分别生成 record/video 段
- **快速路径并发**：槽位满时立即返回不阻塞（降级慢路径）；任务完成后槽位释放；异步任务持有事件深拷贝，修改原事件不影响异步侧
- **快速路径隔离**：后台任务 panic 被 recover 并记录，不退出进程且槽位必定释放
- **Kafka 跳过语义**：`ErrSkipMessage` 只处理一次并提交 offset，不重试、不进死信；普通错误仍就地重试，达上限后转死信
- **用户活动事件契约**（`ai/collection/service`）：`user_id`/`action` 齐全时正常处理；`action` 缺失/非字符串/空串、`user_id` 为 `null`/数字/空白、`target_type` 类型错误、生产端只发通用信封（三个契约字段全缺）都返回 `ErrSkipMessage` 且零副作用；整包 JSON 为 `null` 同样跳过；非法 JSON 不跳过，保留重试/死信路径以便留存原始载荷；合法事件的首尾空白会被裁剪
- **消息枚举反解**（`messaging/message/handler`）：内部优先级 1..4 正确映射为 `LOW`/`NORMAL`/`HIGH`/`URGENT`，`0`、`5`、`-1` 返回 `ok=false`；消费时未知 topic、非法优先级、非 JSON `headers` 都返回 500 且清空半批消息，错误信息包含具体 `message_id`；批量发送遇到未知 topic 只失败该条，其余条目仍返回成功与实际 topic
- **实体查重失败关闭**（`ai/knowledge/service`）：`FindEntityByNameAndType` 返回错误时，`FindOrCreateEntity` 必须返回 `ErrInternalServer` 且不返回实体、不进入创建事务；只有明确返回 `nil, nil`（确实不存在）才允许创建
- **总结服务失败关闭**（`ai/summary/service`）：脚本化 ChatModel + 内存仓储覆盖——历史拉取失败或历史为空时不调用模型、不落库并返回带阶段信息的错误；`include_todos=true` 时提取失败返回错误且半成品摘要不落库；`key_points` 失败只降级为空要点，摘要仍成功落库；`CreateSummary` 失败不返回记录；候选/待办解析失败（含一次 JSON 修复后仍失败）返回错误而不是空列表；`message_ids` 暂不支持按 ID 取内容时显式回退最近历史并原样留档；`marshalField` 对不可序列化值返回错误
- **知识提取失败关闭**（`ai/process/service`）：实体全部创建失败、三元组全部非法、关系写入失败且没有其他产物时返回 `ErrKnowledgeExtractionAllFailed`，且非法关系零落库；坏实体不影响合法实体写入，名称会裁剪空白，字符串属性按原值保留；关系端点复用已有实体而不是重复创建；模型确实没返回实体或关系时视为成功且不触达知识服务
- **文档 DTO 统计失败边界**（`ai/process`）：单条与批量块数查询失败都返回带「查询文档块数量失败」的错误，不返回 `chunk_count=0` 的半成品 DTO 或整页零值；正常路径按文档 ID 映射真实块数；文档不存在返回 `nil, nil`，重处理不存在文档返回 `ErrDocumentNotFound`（handler 映射 404）；静态守卫禁止 handler 再用 `dto, _ := GetDocumentDTO`
- **翻译失败关闭**（`ai/moderation` + `pkg/client`）：无 ChatModel 时 `Translate` 返回错误、不返回记录、不写库，错误信息不含占位文案；客户端对 `TranslateResponse.Code != 0` 返回错误且不返回译文，`Code=0` 正常返回 `translated_content`
- **消费循环生命周期**（`pkg/mq` + `ai/collection` + `ai/extraction` + `ai/knowledge`）：`Subscribe` 拿到已取消 ctx 时不拉取任何消息并关闭 reader；extraction `handleCollectionEvent` 建任务时透传 `msg.Context`（用 ctx value 断言），直接调用时回落 Background；三个服务入口统一用 SIGINT/SIGTERM 派生的 ctx 启动消费者
- **实体缓存编码边界**（`ai/knowledge`）：注入 `marshalCachedEntity` 返回错误时 `cacheEntity` 不得调用 `Set`，且必须删除已有缓存，避免更新后继续返回旧实体；缓存内容损坏时 `GetEntity` 按未命中回源一次，并用回源结果覆盖坏值
- **推荐负缓存**（`ai/recommend`）：空结果连续请求只回源 1 次，且写入 TTL 不超过 2 分钟（正常结果是 30 分钟）；向量检索失败导致的空列表不写缓存，第二次请求仍会回源且 `Set` 调用数为 0
- **推荐并行化边界**（`ai/recommend`）：向量检索与知识图谱检索同时进行，慢的一路不会串行阻塞另一路；标题改写并发数不超过 4，`-race` 下所有标题都被正确回填；已有真实标题不会被模型覆盖；两路检索其中一路失败时另一路结果照常合并，不互相取消；`chatGenerator` 为 nil 时不发起任何模型调用
- **向量集合读取边界**（`ai/vector/dao`）：数据库故障 + 指定 `userID` 时 `ListCollections` 必须报错且不返回任何结果（禁止回退到无归属的 Milvus 全量列表）；数据库故障且 Milvus 未连接、或两套存储都未初始化时同样报错，不能返回空切片；`row_count` 缺失、空 map、`"12abc"` 后缀垃圾、负数都让 `parseCollectionSize` 返回错误，正常值与带空白值可解析；`Search` / `TextSearch` 在 Milvus 未连接时返回「Milvus未连接」错误而不是空结果
- **QQ 出站错误分类**：缺目标 QQ 号、缺 Bot QQ 号、缺群号、空消息、不支持的会话类型都返回 `ErrSkipMessage`；QQ 未连接是可重试错误，不能被当成不可恢复
- **快速路径接管契约**：Bot 客户端缺失、槽位已满、小号自己发的消息、群聊消息都返回未接管；只有真正接管才写 `qq_direct_bot`，未接管时不占槽位
- **快速路径补偿**：可恢复失败（panic、Bot 调用失败、空响应、回复完全发不出去）生成 `bot_recovery` 事件；成功与「未绑定 Bot」不补偿；补偿事件换新 ID、撤销 `qq_direct_bot`、保留 QQ 地址与 Bot 提及、不携带接收者；Chat Service 只补跑 Bot，不重复落库/推送；缺少补偿标记的事件安静忽略
- **Bot 回复幂等**：`(botID, 源消息ID)` 派生确定性 ID，同输入稳定、不同输入不碰撞、缺字段返回空、长度不超 `varchar(64)`；回复已落库时补偿不再调 Bot；重放守卫只拦 `bot_` 发送者，普通用户重复内容不受影响；主键查询失败时失败开放
- **QQ 并发与限额配置**：`fast_concurrency`/`media_concurrency`/`media_max_bytes_mb`/`media_timeout_seconds` 支持配置文件与环境变量；nil、未配置、负数回落默认值，0 不会产生无缓冲 chan，超大值被压到硬上限；配置真实作用到 Bridge 与 MediaRelay；非法环境变量保留配置文件值
- **容量信号与故障信号分离**：媒体排队失败打 `stage=queue`，快速路径槽位打满打 `result=busy`；两条容量告警必须引用这两个专用标签，不能混回 `failed`
- **数据库重试边界**：`shouldWaitBeforeNextAttempt` 覆盖单次尝试、中间一次、最后一次与非法 attempts；最后一次失败后不再白等一个退避周期
- **缓存懒加载自愈**：连接失败进入 30s 冷却期而非永久失败；冷却内不重连，冷却结束后恢复；成功实例只连一次且并发首调唯一；`Mapper` 构造时缓存不可用、恢复后仍能写入；缓存不可用时用户/群映射返回确定性 ID，反查返回错误
- **缓存预算全覆盖**：`runOp` 自带上限且尊重调用方更短的 deadline；静态检查遍历 `RedisCache` 全部方法，任何直接调用 `c.client` 又没有套 `runOp/runErr` 的方法都会失败（`Close`/`RawClient` 显式豁免），防止批量/Hash/List/Pipeline 等新方法漏掉预算
- **直连治理**：直连一个无人监听的端口且调用方不设 deadline 时，客户端超时拦截器必须让调用在预算内返回；调用方自带 deadline 时不被放大
- **重试白名单**：SendMessage/SendBotMessage/CreatePaymentOrder/ConfirmPayment/Deposit/Withdraw/Consume*/Refund/AddFriend/Create*/Delete* 一律不重试；只读方法与幂等覆盖写可重试；方法名为空保守不重试；配置的 `retryable_codes` 真正生效，`UNAVAILABLE` 与 `Unavailable` 两种写法都能匹配；空配置保留最小安全集合
- **重试白名单可配置**：`grpc.client.retry_safe_methods` 只做加法且被真正读取；空配置下危险写仍不重试；策略持有切片副本，调用方后续修改不影响已建连接；etcd 与直连两条路径都接收同一策略
- **健康检查语义**：无检查时视为就绪；硬依赖失败阻塞就绪、可选依赖失败只标记 `degraded`；检查 panic 被 recover 成失败而不是打崩进程；慢检查按总预算超时返回，关键检查超时判定未就绪；并发探针合并成一次回源（8 个并发只执行 1 次）；TTL 内复用结果且返回副本，调用方修改不污染缓存；失败结果同样缓存，避免探针反复打已过载的依赖
- **探针适配器**：nil 数据库/nil 缓存必须显式报错而不是静默健康；Redis `Ping` 失败向上传递；`Timeout` 包装器按预算返回 `DeadlineExceeded`
- **HTTP 探针**：`/livez` 在依赖全部失败时仍返回 200（只反映进程存活）；`/readyz` 健康返回 200、硬依赖失败返回 503 并暴露 `failed` 列表；可选依赖失败仍返回 200 且列出 `degraded`
- **gRPC 健康联动**：依赖失败时具名服务与空 service 名都切到 `NOT_SERVING`，恢复后切回 `SERVING`；`watchReadiness` 随 ctx 取消退出，不泄漏 goroutine
- **etcd 注册联动**：初始未注册、首次就绪才注册；单次失败不摘除，连续失败达到阈值才 `Deregister`；恢复后立即重新注册；重复就绪不重复写 etcd；注册失败下次就绪重试；摘除失败保持未注册并继续重试；lease 中途失效后即使本地仍认为已注册也会重新 Put；无依赖探针的服务由校正循环自愈；`Close` 幂等且关闭后不再注册；注册地址按 `Host > SERVICE_HOST > HOSTNAME > 127.0.0.1` 解析
- **就绪告警规则**：`ServiceNotReady` 只统计 `result="not_ready"` 并按 service 分组，避免正常探测稀释故障率
- **服务状态采集**：一次前缀查询按服务名归并实例数，服务名含 `/` 也能正确解析，非法 key 被忽略；批量 upsert 使用 `ON CONFLICT DO UPDATE`（不是逐条两次往返）；空批次不发 SQL；etcd 查询失败时保留上次状态并降级为 `DEGRADED`，不会把监控自身故障误报成全部服务下线
- **监控数据保留**：删除 SQL 按 id 子查询 + `LIMIT` 分批并带 `FOR UPDATE SKIP LOCKED`；告警清理限定 `resolved = true`；非法/非正数保留天数回退默认；超长保留期收敛到 10 年，避免 `Duration` 溢出成负数后变成立即删除
- **看板 HTTP 生命周期**：复用 `pkg/httpserver` 的 `ReadHeaderTimeout`/`IdleTimeout`/`MaxHeaderBytes`，慢请求头不会长期占住连接；SIGTERM 时先停止接收新连接、再以 15s 排空在途请求（主流程等待预算不小于 `Shutdown` 预算，避免排空被提前打断）。看板端口被占用时只记录错误并禁用看板，gRPC 服务继续注册与提供服务——旁路能力故障不应升级为整进程退出
- **网关启动/退出路径**：HTTP 与 TCP 端口先 `net.Listen` 再 `Serve`，端口占用作为启动错误返回；`run()` 错误返回保证 OTel flush 与客户端关闭在失败路径也执行；HTTP `Serve` 运行期失败与 SIGTERM 共用同一停机流程，WS/TCP/广播器仍会排空后退出
- **对象存储初始化**：首次失败后连续调用都必须返回错误而不是 `(nil, nil)`（回归 `sync.Once` 缓存失败的缺陷）；5s 负缓存窗口内不重复探测、窗口过后重新尝试并成功复用；16 个并发首次调用只探测一次且错误一致（`-race`）；已取消 ctx 在上传/下载/删除/预签名/存在性检查中先于后端返回 `context.Canceled`
- **懒初始化原语**：`pkg/lazyinit` 验证失败永不返回零值+nil；冷却窗口内不重复探测、窗口过后重试并自愈；成功结果永久复用；16 并发首次调用只执行一次 load（`-race`）；零冷却窗口表示立即重试；nil loader 显式 panic 而不是静默空转
- **消费生命周期 ctx**：订阅时传入的 ctx 会出现在 handler 收到的 `Message.Context` 上；ES 实体 create/update/delete 与索引统计使用该 ctx，停机时在途写请求可被取消，不再退回 `context.Background()`
- **问答投递与执行语义**：7 个内部 topic 名全部映射到对应 Kafka 常量，`QA_REQUEST` 必须落到 `qa_request`；`AskQuestion` 失败时 handler 返回错误（触发重试），成功才提交 offset；问答应拿到带 deadline 的子 ctx，父 ctx 取消后 handler 立即退出；非问答消息不触发 `AskQuestion`
- **Kafka 事件字段校验**：`content` / `message_id` 非字符串时返回 `ErrSkipMessage`，`AskQuestion` 零调用且不写数据库；问答缺 `content` 同样按不可恢复消息跳过；`questionService` 未注入返回可重试错误而不是静默成功；`message_id` 为 `null` 视为事件不需要回写状态，正常结束且不触库
- **推荐问题客户端契约**：`BaseResp.StatusMessage` 为 `{"questions":[...]}` 时正确解析；坏 JSON 返回 error 而不是空列表；空 `StatusMessage` 返回空列表且不报错
- **gRPC 指标只反映真实调用**：`UnaryServerMetricsInterceptor` 对成功与失败请求分别记入 `code=OK` / `code=InvalidArgument`，耗时直方图按 method+code 拆分；只创建 meters 而没有任何调用时不得出现数据点（防止「定时器造指标」回归）；nil meters 时拦截器原样透传
- **批量确认语义**：多个 id 只发一条批量 UPDATE（不是 N 次往返）；空字符串与重复 id 被过滤；没有有效 id 时不访问数据库；数据库失败时返回错误而不是 `continue` 后报成功；消费后状态更新失败也会上抛触发重试
- **FCM 响应契约**：`success=1` 判定成功且携带 `key=` 授权头；`results[0].error=NotRegistered` 归为 permanent 并触发令牌注销、`Unavailable` 归为 retryable；HTTP 200 + 截断 JSON 返回 `invalid FCM response body`（unknown、不注销令牌、原始 `io.ErrUnexpectedEOF` 可通过 `errors.Is` 取到），不得出现 `message rejected`；`success=0/failure=0` 的空壳响应返回 `empty FCM response`
- **Kafka 主题初始化**：第二次尝试成功时不返回错误且只等待 2s；连续失败恰好 3 次、只在 2s/4s 后重试（最后一次失败不再白等 6s）；broker 拨号失败包装 `errKafkaBrokerUnreachable` 并短路剩余 topic（只 3 次 Dial，而非 9 个 topic × 3 次）；单个 topic 配置错误不阻断其余 topic，`errors.Join` 聚合的错误包含 topic 名；已取消 ctx 零拨号；零值 `KafkaManager` 返回错误而不是 panic
- **数据库指标回调**：query/create/update/delete/row/raw 六类操作都注册成功；一次 `Find` 只产生 1 个数据点（重复注册会把延迟和慢查询计数翻倍）；`db.Exec` 记为 `operation=raw`、`table=unknown` 而不是被丢弃；`gorm:query` 锚点被移除时返回含锚点名的错误、不半注册 before 回调，且不影响其他 processor 的埋点
- **后台任务 panic 隔离**：`safego.Run` 把 panic 转成 `*PanicError`（保留任务名、panic 值与非空堆栈），正常任务返回 nil；每次 panic 恰好累加 1 个 `logos.safego.panic.count` 数据点（连续两次为 2）；`safego.Go` 中一个任务 panic 后，另一个后台任务仍能正常执行；聊天服务里一个 Bot 客户端 panic 不会阻断同批其他 Bot（修复前会直接终止测试进程）
- **抽取结果契约与落库**：`toExtractedEntity` 对缺失、`null`、数字、空白字段返回带字段名的错误；合法记录保留自定义属性与置信度描述（字符串置信度不得生成 `%!f` 占位符）；关系缺 `targetId`、三元组缺任一部分都被拒绝；部分记录非法时只跳过非法项、合法项照常写入，全部非法或图数据库写入失败时任务失败，不再出现「抽取成功、图谱为空」；任务参数是坏 JSON 时返回错误；`scheduled_time` 非 RFC3339 时 `CreateTask` 拒绝落库
- **计划任务调度**：到期任务按 `scheduled_at` 升序批量执行（`limit` 有上限）；单条任务成功/失败/被其他实例领取都不影响同批剩余任务；被领取（`claimed=false`）记为 skipped 而不是 failure；扫描失败时该轮零执行且单独上报扫描失败指标；扫描取满一批时留下积压告警日志；卡死的 `RUNNING` 任务按 `started_at` 超时回收，写入的失败原因非空且 cutoff ≈ now - staleAfter；非法的 interval/batch/timeout 回退默认值而不是把 ticker 设成 0；`Start` 先立即执行一轮、失败后仍按间隔继续；`Stop` 取消后及时返回
- **抽取任务类型/状态映射**：proto 五类任务（0..4）与存储层（1..5）逐项双向映射且往返不变；`SUMMARIZATION` 创建时被拒绝且零落库；`taskTypeToProto` 对 0、99 返回错误，`taskStatusToProto` 对 0、99 返回错误，不得猜一个「最接近」的值；`extractionToProto` 遇到非法类型/状态、或参数 JSON 损坏时返回错误而不是回包空参数；`ExecuteTask` 在服务返回错误且无结果时不 panic、透传原始错误；服务层 `validateTaskType` 接受 1/2/3/5，拒绝 0/4/99，`UpdateTask` 校验失败时零写入
- **集合嵌入配置分类**：`collectionEmbedder` 在集合为 nil、或模型与密钥都为空时返回 nil（按约定回退全局 Embedding）；只配模型或只配密钥时返回「配置不完整」错误且不返回 embedder（回归「静默回退全局模型」）；模型与密钥齐全时返回可用 Embedder，构造过程不发起网络请求
- **批量嵌入结果校验**：`toFloat32Vectors` 拒绝条数与输入不一致、拒绝空向量、拒绝「第 N 条维度与首条不同」；正常输入按 float32 转换且数值不变；空批次返回非 nil 空切片；`BatchVectorize` 在文本列表为空时直接拒绝，不再创建维度为 0 或 768 的空集合
- **向量集合查询与写入降级**：`GetCollection` 只有 `gorm.ErrRecordNotFound` 允许回退 Milvus 探测；带 `userID` 的查询在 Not Found 时返回 `nil, nil`，不得用无归属信息的探测结果放行；其余数据库错误必须上抛且不调用 `HasCollection`；`Vectorize` / `BatchVectorize` / `TextSearch` 不得用 `_` 吞掉 `GetCollection` 错误；集合显式配置的嵌入模型调用失败时原样上抛，不回退全局模型；Milvus 维度与当前嵌入输出不一致时返回错误且**不调用** `DropCollection`（历史向量只能由运维显式重建）；`TextSearch` 在集合不存在、维度不一致、无可用 Embedder、全局嵌入返回空向量时返回错误而不是 200 + 空列表
- **集合更新持久化**：`UpdateCollection` 必须先查 `vector_collections` 再动 Milvus，记录不存在时直接报错且不调用 `DropIndex`；写回只更新 `name` / `model_type` / `index_type` / `dimension` / `parameters` / `vlm_*` / `llm_*` / `asr_*` / `embedding_*`，SQL 不得出现 `user_id`、`size`、`created_at`；`parameters` 必须经 `serializer:json` 序列化（DryRun SQL 中可见 `{"M":"16"}`）；索引类型、维度、参数均未变化时跳过 `DropIndex` / `CreateIndex` / `LoadCollection`；service 层维度变更返回错误且不调用仓储；只传 `__embedding_base_url` 时 `Model` / `APIKey` 保持原值；handler 剥离 `__embedding_*` 时必须复制 map，只传内部键时不能把已有 `parameters` 清空
- **知识图谱批量导入**：`decodeImportObject` 用 `json.Number` 保留大整数原文；`null`、数组、截断 JSON、对象后的多余内容都被拒绝。`importStringField` 区分「缺失 / 空串 / 类型错误」，`"type": 123` 报「实际为数字」而不是「缺少字段」；`importStringMap` 保留字符串/数字/布尔标量、丢弃 `null`、嵌套对象显式报错。实体导入「一条合法 + 三条非法」时返回「成功 1/4 条」并逐条定位到第 2/3/4 条，合法条目的属性仍按原文合并进已有实体。关系导入读取 `collection_id` 并写入 `Relation.CollectionID`（修复前恒为空串）。`importBatchError` 明细最多列 3 条，其余以「另有 N 条失败」计数
- **图同步与子图补全**：`hydrateGraphEntities` / `hydrateGraphRelations` 按 Neo4j 返回顺序补全；读失败返回带「来源 + ID」的错误而不是半截结果；节点/关系缺 `id`、非字符串、空白串跳过并计数；PostgreSQL 中已删除的记录跳过但不报错；空输入返回非 nil 空切片。`resolveRelationEndpoints` 覆盖四个分支：两端都存在时返回类型、源实体缺失、目标实体缺失、读取失败，失败信息分别带「源实体 / 目标实体」ID。创建关系写入 `createdAt`，更新关系不写；`properties` 可覆盖内置字段。仓储在 `neo4jManager == nil` 时零查询
- **MCP 注册与配置转换**：`RegisterTool` / `CreateService` 在查重仓储报错时返回错误且零写入；空名称、工具类型 0/6、未知传输类型在访问仓储前被拒绝；创建时名称去空白，参数与配置按 JSON 落库。`UpdateTool` / `UpdateService` 改名为已被他人使用的名称时拒绝且不写库，改回自己原名不触发查重。自定义工具未配置聊天模型时返回错误，调用日志状态为 `error` 而不是 `success`。`convertModelToolToProtoTool` / `convertModelMCPServiceToProto` 对损坏的 `config` / `headers` / `auth_config` / `advanced_config` 返回带字段名的错误，合法 JSON 原样解码
- **执行权原子性**：`ExecuteTask` 在 `MarkTaskRunning` 返回 false 时报「任务正在执行中」且不再调用模型；`RunScheduledTask` 领取报错时 `claimed=false` 且错误上抛（不能伪装成被其他实例抢走）；领取 SQL 必须同时带 `status = Pending` 与 `scheduled_at <= now` 条件，只有 `RowsAffected == 1` 才算抢到
- **调度 SQL 守卫**：用 GORM `DryRun` + 记录型 logger 断言 `ListDueTasks` 的 SQL 含 `status = Pending`、`scheduled_at IS NOT NULL`、`scheduled_at <=`、按 `scheduled_at` 升序与 `LIMIT`；`ClaimDueTask` 的 `UPDATE` 同样带全部领取条件；`MarkTaskRunning` 只排除 `status = Running`（允许重跑完成/失败任务）；`ListStaleRunningTasks` 带 `started_at IS NOT NULL`、`started_at < cutoff` 且按 `started_at` 升序。测试不连真实数据库，任何语句真的尝试执行会立刻失败
- **ES 同步事件契约**：`action` 缺失、非字符串或空白时返回 `mq.ErrSkipMessage`（不重试、不静默）；未知 `action` 同样进入 skipped 指标，而不是 `return nil` 悄悄提交 offset
- **topic/priority 枚举映射**：`TopicString(0..6)` 必须与 proto 枚举一一对应（`0 = DATA_COLLECTION`、`6 = SYSTEM_EVENT`），越界值显式成为 `UNKNOWN(n)` 并被 `ValidTopic` 拒绝；proto Priority（0 基）转内部 1..4 后，`LOW/NORMAL/HIGH/URGENT` 往返不偏移；存储层 topic 名回映射 proto 时未知值不得伪装成 `DATA_COLLECTION`
- **批量发送原子性**：一批消息只开一个事务、一次多值 INSERT，并写入等量 outbox 事件；任一条非法时在开启事务前被拒绝且错误定位到「第 N 条」；写库失败整批回滚、不留下任何 outbox 事件；超过 100 条直接拒绝；批量响应逐条回显消息的真实 topic
- **消费租约与确认语义**：领取 SQL 必须包含 `FOR UPDATE SKIP LOCKED`、过期租约条件与 `UPDATE ... RETURNING`；消费返回前不再额外标记状态；默认 limit 与服务端一致；领取失败必须上抛而不是返回空列表；确认 SQL 同时校验 `status IN (PROCESSED, ACKED)` 与 `claimed_by`；非本消费组/不存在的消息确认返回 `ErrMessageNotClaimed`，handler 映射 404
- **统计聚合语义**：一条 `GROUP BY topic` 返回全部 topic（不是 1+7 次查询）；空 topic 补零行且总数/待处理/已处理/失败字段齐全；`COALESCE` 保证空表不报错；查询失败必须上抛，不能返回空列表冒充正常
- **统计跨层类型**：服务层返回 `[]dao.TopicStats`（不再是 `[]map[string]interface{}`），handler 直接读字段而不做裸类型断言；未知 topic 跳过；下游查询失败返回 `BaseResp.StatusCode=500` 且 `Stats` 为空，不能用一串 0 冒充「一切正常」
- **状态常量一致性**：Kafka 消费成功后写入 `PROCESSED`（统计认识的集合），不是历史上的 `PROCESSED_FROM_KAFKA`；创建/消费/确认/清理/统计共用 model 中的状态常量，新增状态必须同时更新统计口径
- **网关路径参数边界**：`topic` 路径参数只接受 `0..6`；`abc`、空串、负数、`7`、小数、超长整数全部返回 400；非法请求不得调用下游 `DeleteTopic`（否则会被当成 `DATA_COLLECTION` 删除）；合法值必须原样透传枚举，不被中间层改写
- **网关状态码映射**：下游 `BaseResp.StatusCode=0` 映射为 HTTP 200，`1` 映射为 500，`100..599` 直通，其余值（含负数、`600`）收敛到 500；裸 `int32 code` 的 bot/billing/MCP/moderation/summary 接口走同一张映射表，`respondProtoCode` 保证响应体 `code` 与状态行一致；handler 目录的静态守卫会扫描源码，出现 `c.JSON(int(...))`、`toHTTPCode(`、`statusCode = int(resp.Code)` 等绕过写法即失败
- **网关分页参数边界**：`page` / `pageSize` / `limit` 的非数字、`0`、负数、溢出 `int` 一律返回 400，且拒绝时不调用下游 gRPC（billing 交易列表用 fake client 断言调用次数为 0）；`pageSize=1000000000` 收敛到 100 而不是原样透传；缺省与空字符串（`?page=&pageSize=`）回落默认值；knowledge 接口的 `pageSize` 与 `page_size` 两种写法都被同一套校验覆盖；静态守卫扫描 handler 源码，出现 `strconv.Atoi(` / `strconv.ParseInt(` 且同行含 `"page"` / `"page_size"` / `"pageSize"` / `"limit"` 即失败
- **网关查询参数边界**：`start_time` / `end_time` 非数字、负数、溢出 int64 返回 400 且不调用下游；两侧都给出时 `start > end` 也返回 400（否则 SQL 恒为空集，调用方只会看到「没有数据」）；`type` / `level` 等枚举越界、负数、非数字一律 400，合法值原样透传（`type=7` → `METRIC_TYPE_LATENCY`）；告警 `level=0`（`UNSPECIFIED`）表示「全部级别」，必须转成 nil 而不是按 0 过滤；`resolved` 只接受 `true`/`false`，`yes` 返回 400 而不是静默取消过滤；缺省参数保持原有默认语义（如指标类型默认 CPU）
- **字符串枚举白名单**：`/bots` 的 `?type=` 只接受 `builtin` / `custom`，`?status=` 只接受 `active` / `inactive` / `deleted`；拼错、大小写不一致、误传数字（`bulitin`、`Builtin`、`1`）一律 400 且不调用下游，不能再掉进 `else` 分支变成「不过滤」并返回全部 Bot；缺省参数仍下发 `UNSPECIFIED`；错误信息列出排序后的合法值
- **登出开关严格解析**：`?all=yes` / `?all=maybe` 返回 400，不能静默降级成「只登出当前设备」；`all` 缺省、`false`、`0` 登出当前设备，`true`、`1`（大小写不敏感）登出全部设备；静态守卫禁止 `c.Query("all") ==` 式比较回归
- **process 文档列表参数**：`page` / `page_size` 非数字、`0`、负数、溢出返回 400（旧实现吞成 0，DAO 归一化后固定返回第一页）；`status` 只接受 `1..4`，`abc` / `0` / `5` / 负数返回 400（旧实现解析失败让 status 保持 nil，等于取消过滤返回全部状态）；`status=99` 不再原样下传 SQL；`page_size=1000000000` 收敛到 100，且响应回显实际生效的 `page_size`；handler 目录静态守卫禁止 `, _ := strconv.` 写法
- **索引接口参数名对齐**：`POST /search/indices` 缺省 `type` 下发 `ENTITY`、`?type=4` 下发 `DOCUMENT`；`DELETE /search/indices/:id` 与 `POST /:id/refresh` 必须读 `:id`，`:id=4` 下发 `DOCUMENT`（回归「恒删 ENTITY」）、`:id=2` 下发 `QUESTION`；`:id` 为 `abc` / 负数 / 越界时返回 400 且 fake client 调用次数为 0；静态守卫扫描 `handler_search.go`，出现 `c.Param("type")` 或缺少 `parseIndexTypePath` / `parseIndexTypeQuery` 即失败
- **身份来源与越权**：`/qa/history`、`/qa/recommended`、`/recommend/items`、`/recommend/history`、`/users/stats` 未认证一律 401；登录用户传 `?user_id=` 指向他人时返回 403 且不调用下游（IDOR 回归）；不传或传自己的 ID 时下游收到的是 JWT 身份；`/qa/recommended` 使用登录身份而不是不存在的 `:userId`，并把下游 `StatusMessage` 里的 `{"questions":[...]}` 解包成 `data: [{question}]`，`limit` 生效；`StatusMessage` 不是合法 JSON 时返回 502，不以空数组冒充成功；静态守卫禁止 handler（`handler.go` 除外）直接读 `user_id` 查询参数、禁止 `, _ := strconv.` 吞错写法
- **消费长轮询语义**：`timeout_ms > 0` 时空结果按 200ms 间隔重试，拿到消息立即返回，预算耗尽返回空列表且不报错；`<= 0` 只领取一次；超过 30s 收敛到 30s；客户端取消立即返回 `context.Canceled` 并停止后续领取；数据库错误始终上抛，不能被长轮询吞成空结果
- **Milvus 启动预算**：持续连接失败时在总预算内返回而不是固定重试 60 次；单次尝试超时被剩余预算截断（预算 200ms、单次超时 10s 时仍在 1s 内返回）；首次成功立即返回且不继续重试；日志包含「预算耗尽」结论
- **日志无配置降级**：`GetLogger` 在没有配置文件时退回 info + stdout，不 panic。单元测试、CLI 工具等未加载配置的场景仍可正常打日志
- **HTTP 排空兜底**：`Shutdown` 在窗口内完成时在途请求正常返回；handler 超过窗口时 `Serve` 返回错误并调用 `Close` 强制断开，卡住请求的 `r.Context()` 会被取消，不会留下悬挂连接与 goroutine
- **依赖启动探测超时**：ES Ping 一直不应答时在 100ms 测试预算内返回错误（不是永久阻塞）；健康 ES 正常返回 client；Neo4j 连通性校验与失效驱动关闭均有独立超时预算
- **Outbox 租约**：领取同时写 `lease_owner`；续租 SQL 带 `lease_owner + status` 条件；`MarkSent` 校验 fencing token；`leaseWindow` 在任意 `sendTimeout` 下都覆盖 `size × sendTimeout`；分块条数落在 `[1, 20]` 且不超预算；空 id 列表不发 `IN ()`；`Stop` 在有界时间内返回；每个 relay 实例的 token 唯一
- **采集任务状态与依赖顺序**：数据源不存在时返回 `ErrNotFound` 且**不写** `RUNNING`（断言 `UpdateTask` 未被调用）；数据源查询失败必须上抛而不是继续采集（回归 `ds=nil` 的 `switch ds.Type` panic）；`RUNNING` 落库失败时立即返回且不产生任何结果；知识写入失败时任务为 `FAILED`、计数只统计已落库条目（第 2 条失败时 `CollectedCount=1`）；知识服务为 nil 时不得返回 `SUCCESS`；结果事务失败时不返回成功、不写 outbox；成功路径必须写结果并发出 `data_collection` 事件
- **抽取任务状态与结果落库**：`RUNNING` 落库失败时立即返回且不执行抽取；不支持的任务类型返回 `Status=4` 的结果并落库、任务状态更新为 4、`EndedAt` 非空；失败结果落库失败时错误同时包含原始抽取失败原因与落库原因（`errors.Join`）；成功路径事务失败返回错误且不返回可查询结果；成功路径写 2 条 outbox（`knowledge_extraction` 与 `vector_processing`）
- **采集事件消费契约**：事件用 `data_source_id` 建任务（断言 `DataID == "ds-42"`、`DataType == "data_source"`），不是旧代码读的 `data_id`；缺少 `data_source_id` 时返回错误且不建任务；`status=FAILED` 的事件跳过且不建任务
- **抽取限流降级**：缓存构造返回 nil 时放行且不 panic（单测注入内存 cache，不再连接真实 Redis）；`IncrBy` 失败放行并记 Warn；`Expire` 失败不影响本次判定
- **抽取结果字段转换**：字段缺失返回零值且不报错；字段类型不符时错误信息带上字段名；位置字段为负数或超出 int32 范围时报错；`json.Unmarshal` 失败必须上抛，不能返回 200 + 空列表；批量列表中第 N 条坏数据时清空已拼好的结果并在错误里带上序号；缺省 `id` 仍按 `text_type` 派生
- **记忆写入的查询失败**：按 key 查询报错时 `Failed` 加一并跳过，**不得**新建同名 key（回归「查询失败退化成重复插入」）；`GetUserMemoryByKey` 报错时 `SetUserMemoryWithCategory` 返回 `ErrInternalServer`，不得走新建分支；快照查询报错时整轮不写；精确 key 命中走合并而非插入；相似 key 命中时合并到已有条目；批量超过上限时只写上限条；`SetUserMemory` 失败计入 `Failed` 且不影响后续条目
- **清空记忆是原子操作**：`DeleteAllUserMemories` 用认证身份发起**一条**批量 `DELETE`（断言 `bulkDeleteCalls == ["alice|bot-owner"]`），不得退化成逐条 `DeleteUserMemoryByID`（回归「逐条删 + `_ =` 吞错 → 部分失败仍返回成功」）；DAO 报错时返回 `ErrInternalServer`；未认证请求不触发任何删除
- **已读位点单一真相**：`MarkMessagesRead` 只推进 `last_read_seq`（显式 msgIDs 取最大 seq，不带 msgIDs 取会话最大 seq），不再逐条写无人读取的 `messages.is_read`；缺身份或非会话成员时不触达位点表；`handleMessageReadEvent` 在 seq 查询失败时原样上抛且不推进位点（回归「失败后发假回执」），ID 全部失效时不发布空回执
- **ffmpeg 解析**：`00:01:23.45` → 83.45 秒；`02:15:30.50` 正确进位；`Duration: N/A`、缺 `Duration` 行、`abc:def:ghi`、`1.2.3`、`99` 分钟、`60` 秒、负数秒、段数不足全部报错；`1920x1080 [SAR 1:1 DAR 16:9]` 与 `1080x1920` 正确识别；`(avc1 / 0x31637661)` 不被当成分辨率；缺 `Video:` 行、`0x0`、`1920x1080i` 均报错；`ffmpeg` 不存在时 `GetVideoInfo` 返回错误且不返回半截 map
- **视频解析降级**：元信息探测失败时 `metadata` 不含 `video_info` 且抽帧回落到默认 1s 间隔；抽帧失败时仍保留 ASR 文本；单帧 VLM 失败只跳过该帧、不产生空帧段落；全部帧描述失败时不渲染「视频帧详情」；`duration=60` 时 `FrameInterval=10`、`MaxFrames=5`；`duration=3` 时间隔被夹到 1s；调用方显式传入的 `ExtractOptions` 优先且不被就地修改

- **转发目标类型解析**：`GetGroupByID` 返回错误时 `ForwardMessage` 原样上抛且不写任何消息（即使前一个目标已解析成功）；已有群会话按群聊类型落库（`ChatType=Group`、`ChatID=目标群`），不能按私聊建出幽灵会话
- **私聊拉黑拦截**：对方拉黑或自己拉黑都返回 `ErrChatBlocked`，且拦截发生在建会话/落库之前（`privateConvCalls == 0`）；关系服务查询失败返回 `ErrChatRelationshipUnavailable`（失败关闭）而不是放行；无拉黑时正常放行且双向各查一次；`system` / `bot_` 发送者与 `bot_` 目标跳过联系人查询；适配器在 TTL 内只回源一次、过期后取到最新拉黑状态、上游故障时**不**返回过期缓存；`CheckFriendship` 无记录返回 200/false/false，数据库故障返回 500，拉黑状态原样回传
- **邀请群成员**：待邀请成员的查询失败必须整批拒绝且不写任何成员（回归 `existing, _ := ...` 把数据库故障当成「不是成员」，导致重复插入撞唯一约束）；同一请求里的重复/空白 ID 被过滤后只写一批；已存在成员跳过；Bot 只写成员表、不写会话参与者；普通成员无邀请权限时零写入；邀请写入统一走 `InviteGroupMembers`（成员/参与者/成员数同事务），不再拆成三次独立写入
- **审核拦截提示**：补充审核判定违规后提示先落库再推送（`system` / 系统消息、`ChatID` 与 `ConversationID` 一致、`Channel=system`）；审核服务报错或判定通过时不写任何消息；`system` 发送者不再送审（避免提示被拦截后递归生成提示）；群聊接收者为全体成员、私聊只回发送者；群成员查询失败向上返回错误，由调用方降级为「仅落库不推送」
- **退出/踢出群**：`GetAllGroupMemberIDs` 失败时 `LeaveGroup` 返回错误且**不解散群**（回归 `len(memberIDs) <= 1` 把空列表当最后一人）；`KickGroupMember` 踢人成功后读剩余成员失败返回成功但不解散群；成员身份查询失败返回「查询群成员身份失败」而不是「您不是该群组成员」；群主转让失败中止退出且不写成员数据；普通成员退出走一次 `RemoveGroupMemberAndConversation` 事务（成员/参与者/成员数），不再分三次独立写入；最后一人退出才调用 `DeleteGroup`
- **群成员查询失败必须失败关闭**：用「报错同时返回非 nil 零值成员」的违约仓储模拟旧 DAO，`authorizeIncomingMessage` 必须拒绝入站事件、`SendMessage` 必须返回 `ErrChatMembershipUnavailable` 且零落库（回归零值指针绕过 `member == nil` 导致任意人可向群聊发消息）；成员存在时仍正常放行作为对照；服务层 `GetGroupMember` 原样上抛查询错误，不把零值成员交给 RPC 调用方
- **群主自动转让顺序稳定**：剩余成员列表按 `joined_at, user_id` 排序后，群主退出固定转让给最早入群的成员；测试桩同步排序，避免 `map` 遍历顺序让用例随机命中 u2/u3
- **群管理写接口失败关闭**：禁言/设置管理员/转让群主在目标成员查询失败或目标不存在时上抛错误且零写入（回归 GORM `Update` 命中 0 行仍返回 `nil`，导致「禁言成功」但什么都没写）；操作者查询失败在禁言/转让/公告/头像/设置管理员五个入口统一返回「查询群成员身份失败」；`JoinGroup` 判重查询失败必须拒绝加入且不插入（回归查询失败被当成「不在群里」重复插入撞唯一约束）；正常路径分别写入一次禁言/角色/转让/成员，验证校验没有误伤合法请求
- **撤回/编辑事件接收者降级可观测**：群成员查询失败时 `editRecipients` 必须返回 error，接收者列表退化为仅发送者；调用方记录「降级为仅通知发送者」而不把已落库的撤回/编辑判为失败（回归 `recipientIDs, _ = ...` 静默吞错，其他成员永远停在旧内容且无日志）；正常路径仍覆盖群全体成员并包含发送者本人
- **建群原子性与入参去重**：`member_ids` 含群主本人/重复 ID 时按首次出现顺序去重且不改调用方切片；群、会话、成员、参与者、成员数走 `CreateGroupWithMembers` 单事务，任一步失败整体上抛且不写系统消息；Bot 只写 `group_members` 不写 `conversation_participants`；空群主返回 `ErrChatBadRequest` 且零写入
- **监控服务状态契约漂移可观测**：`ListServiceStatuses` 在 nil client / RPC 失败 / 非法 JSON / 空数组 / 缺 `data` / 顶层非对象时回退本地探测，且 `data` 必须是非 nil 空数组（端口配置为 0 时不发起真实拨号）；合法列表原样透传、长度不变；不可识别条目（非对象、缺 `service_name`、`status` 非字符串）保留在输出中而不被静默丢弃；`GetServiceStatus` 在非法 JSON / 顶层非对象 / 空 message 时返回 500 `invalid monitoring response` 且不携带 `data`，合法响应解包 `data` 字段，RPC 失败与下游业务错误码分别映射到 500
- **群成员列表查询失败的读写差异**：`CreateGroup` 在成员列表查询失败时仍返回 200，`member_ids` 为空但 `member_count` 必须是事务回填的真实人数（回归 `memberIDs, _ := ...` 吞错导致新建群显示 0 人）；`GetGroup` 在同样失败下返回 500「查询群成员列表失败」且不返回 `data`（回归返回 200 + 空成员列表让详情页渲染成空群）；非成员访问仍返回 403，正常路径同时返回 `member_ids` 与 `member_count`

需要真实基础设施、因此不在单元测试中覆盖的部分：消息 `seq` 单调性、消费幂等、`after_seq` 增量补拉、`SKIP LOCKED` 的并发领取行为。这些请在启动 docker 基础设施后按下方 API 用例验证。

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
- ✅ 集合配置的模型调用失败时接口返回错误，不会悄悄改用全局模型继续写入
- ✅ 把集合换成不同维度的嵌入模型后再写入，返回「维度不一致」错误且**不删除**已有向量
- ✅ 集合只配了模型名、没配密钥时，写入与检索都显式报「配置不完整」

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
