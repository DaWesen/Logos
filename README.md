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
- **会话列表分页**：默认 keyset 游标分页（`(updated_at, id)` 锚点，深页代价恒定），同时兼容 `page/page_size`
- **分页边界统一**：`pkg/pagination` 收敛 `page>=1`、`pageSize∈[1,100]` 与默认页大小，DAO/Service 不再各写一份；ES 检索额外检查 `from+size ≤ 10000`（`index.max_result_window`），深页直接拒绝而不是把后端原始错误透给客户端
- **消息发送幂等**：客户端带 `(sender_id, request_id)` 重复提交时回放首次消息；`logos.message.idempotency.count{result=replayed}` 暴露重试率，重试被挡住但请求在放大时能第一时间看到
- **离线推送与上线同步**
- **多端消息同步**：同一账号多端登录，消息实时同步
- **QQ 媒体中转**：QQ 入站图片/语音/视频先经 `safehttp` 校验（协议白名单、逐 IP 拒绝私网/回环/云元数据、重定向逐跳复核）再下载，单文件默认 100MB 上限（可配）；本地临时文件用 UUID 命名、`0600` 权限，不信任上游文件名；上传 MinIO 时对象键同样随机化，并按内容嗅探决定 `Content-Type`，HTML 等非媒体内容一律降级为 `application/octet-stream`，避免媒体代理变成 XSS 入口。并发下载/上传默认 8 路（可配），排队与消息预算共用同一个 ctx，等太久就放弃媒体而不是无限占住处理协程。MinIO/QQ CDN 失败只降级为文本占位，消息本身不丢；`logos.qqbridge.media.count{stage,result}` 单独暴露失败率，`stage=queue` 把「容量不足」与「下载/上传故障」分开，因为这条链路失败不会体现在消息发送成功率里
- **跨系统媒体地址**：媒体代理返回的是 `/api/v1/...` 相对路径，只对浏览器同源有效；出站到 QQ（LLOneBot 在容器外）前用 `gateway.public_base_url` 拼成绝对 URL，未配置时跳过媒体段而不是发一个注定失效的链接。qqbridge 与网关必须使用同一个 MinIO 桶（`logos-documents`），否则图片会 404
- **QQ→Bot 路由自愈**：Bot 创建/更新时写 `qq:bot_bind:<QQ号>` 缓存，但 Redis 重启或 key 被逐出后路由会退化成普通 QQ 用户（私聊不进 Bot 会话）。Bridge 启动时从 Bot 表批量预热；运行期缓存未命中或 Redis 不可用时按 QQ 号唯一索引回源一次并回填。`NewRedisCache()` 初始化失败返回 nil，所有调用点都做了 nil 判断，避免一条 QQ 消息把 Bridge panic 掉
- **QQ 出站媒体与快速路径一致性**：Bot 回复经 `LogosEventToQQMessage` 统一转成 `text/image/record/video` 多段消息，媒体地址在出站前拼成公网绝对 URL；纯图片消息不再因为 `Content==""` 被丢弃。快速路径直发 QQ 与 Kafka 出站订阅共用同一转换逻辑；直发失败不写 `qq_fast_replied`，否则订阅会跳过这条消息、QQ 侧永久收不到回复
- **快速路径有界并发**：每条私聊消息都会触发一次 LLM 调用，Bridge 用 4 个槽位限流；槽位满时不再堆 goroutine，直接交给 Chat Service → Kafka 慢路径兜底。异步任务拿的是事件快照（Metadata/MentionUserIDs/MediaMeta 深拷贝），避免与发布路径共享 map 产生数据竞争
- **出站失败语义**：`chat_outgoing` 的 handler 现在把可重试失败（QQ 未连接、API 调用失败）上抛给消费者，触发就地重试与死信兜底；此前只记日志并返回 nil，Kafka 会提交 offset，Bot 回复永久丢失。消息本身不可恢复（缺目标 QQ 号/群号、空内容、不支持的会话类型）用 `mq.ErrSkipMessage` 标记，消费者直接提交跳过，不浪费 5 次指数退避、也不污染死信队列。`logos.qqbridge.outbound.count{result,reason}` 区分 ok/failed/skipped
- **快速路径接管必须真实发生**：`qq_direct_bot` 是「这条消息已经由 Bridge 直接调用 Bot」的契约，Chat Service 看到它就会跳过 Bot。此前无条件置位，Bot 客户端未连接或槽位已满时消息会「谁都不处理」（群里 @Bot 永不回复）。现在 `tryDispatchFastReply` 返回是否真正接管，只有接管成功才写标记，且标记随事件一起进 Kafka
- **接管失败要有回收路径**：快速路径返回结构化结果，只有可恢复的失败（Bot 超时、空响应、panic、回复完全发不出去）才发布 `bot_recovery` 补偿事件，并显式撤销 `qq_direct_bot`；Chat Service 对该事件只补跑 Bot，不重复落库与推送。`logos.qqbridge.fastpath.count{result=recovered|failed}` 把「兜底成功」和「彻底没回复」分开，后者配 critical 告警，因为它在消息成功率上完全隐形
- **Bot 回复可去重**：回复 ID 由 `(botID, 源消息ID)` 做 SHA-256 派生，快速路径与慢路径共用同一规则；重投/补偿产生的第二次回复会落到同一主键。发布 outgoing 前再查一次主键做重放守卫，数据库挡住重复写入、守卫挡住重复推送；查询失败时失败开放，避免把正常回复吞掉
- **QQ 并发与限额可配置**：快速路径并发、媒体并发、单文件上限、媒体预算都可通过 `qqbridge.*` / `QQBRIDGE_*` 调整；所有值在构造时归一化到安全区间（0 会变成无缓冲 chan、过大等于没限流，因此必须收敛），非法环境变量保留配置文件值
- **启动重试不泄漏连接池**：PostgreSQL 初始化在 `Ping` 失败时显式关闭已创建的 `*sql.DB`（`gorm.Open` 成功不等于可用），最后一次失败后不再白等退避周期；可选任务（缓存预热）用更小 attempts + 独立 ctx，不阻塞主服务启动
- **缓存懒加载可自愈**：Redis 连接失败不再被 `sync.Once` 永久记住，而是进入 30s 冷却期后重试；冷却内直接降级、不重复连接，避免把缓存故障放大成连接超时风暴。`Mapper` 按需取实例而不是构造时缓存 nil，Redis 恢复后映射写入自动恢复
- **直连与集群共享治理**：`NewDirectClientConnWithGovernance` 给直连模式补上超时/熔断/重试拦截器；此前只有 etcd 模式有治理，而直连的 `WaitForReady(true)` 会在下游不可用时无限等待，导致单机部署比集群部署更易被拖垮
- **重试只给幂等方法**：gRPC 重试拦截器改为白名单策略，只有只读查询与幂等覆盖写会自动重试；`SendMessage`、`Deposit`、`ConsumeModelCall` 等非幂等写在 `Unavailable` 时也只执行一次，避免重复发消息/重复扣费。同时修正 `retryable_codes` 配置从未生效的问题（`RESOURCE_EXHAUSTED` 与 `ResourceExhausted` 写法不同）
- **白名单可增量配置**：`grpc.client.retry_safe_methods` 允许运维追加可重试的只读方法，且只做加法（不能削弱内置白名单）；策略持有配置切片副本，etcd 与直连两条路径共用同一策略，避免「集群能重试、单机不能」

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
- **文档向量化失败语义**：chunk 向量化继承外层预算（不再用 `context.Background()` 把 3 分钟总预算架空），10 路并发；全部失败返回 `ErrVectorizationAllFailed` 并把文档标为 Failed，部分失败保留 Completed 但把 `x/y 未生成向量` 写入 `ErrorMsg`，避免“空壳文档”被当成处理成功
- **孤儿文档回收**：文档处理是进程内异步任务，重启/OOM 会让 Pending/Processing 永久卡住；`StaleDocumentReaper` 启动即回收一次、之后每 5 分钟有界批量（200 条）把超过 30 分钟未更新的文档标为 Failed，并写清失败原因；`logos.document.reap.count` / `failure.count` 分别记录救回数量与回收器健康度
- **MCP 工具集成**：Bot 可调用外部工具（天气查询、代码执行、Web 搜索、计算器等），扩展能力边界
- **MCP 代码执行沙箱**：多层防护——环境变量白名单（服务密钥不泄漏给被执行代码）、超时硬上限 + 进程树终止、输出/代码大小限额、全局并发限制；设置 `LOGOS_CODE_SANDBOX=docker` 时升级为容器级隔离（`--network none --memory 128m`，无网络、只读挂载）
- **工具执行内核**：所有只读检索工具共用一套策略——20s 总超时（覆盖所有重试）、16 路并发上限、同参调用合并、单条 32k/单轮 48k 结果预算；可重试错误按 100→200→400ms 退避，最多 3 次尝试；连续 5 次失败后熔断 30s，冷却后只放一个半开探针。重试、熔断、缓存只对 `ReadOnly`/`Cacheable` 工具生效，MCP 等有副作用工具即使误配 `Retryable` 也不会重复执行；调用方取消不计入熔断，同参等待者也能随自己的 ctx 取消
- **语义缓存**：语义相似的重复问题直接返回缓存答案（Redis + 向量余弦相似度 ≥ 0.90 命中），省一次 LLM 调用、毫秒级响应；Redis 不可用时自动降级直通
- **缓存三件套**：击穿（缓存过期瞬间并发回源）用 `pkg/singleflight` 合并成一次，等待者随自己的 ctx 取消、回源用 `WithoutCancel` + 超时避免被单个客户端拖死；穿透（查不存在的实体）写 60s 空值标记，实体写回时立即清除；雪崩（同时过期）由 TTL 抖动（±10%）把失效时间摊开。`logos.cache.fill.count{result=fill/shared/error}` 能量化拦截比例
- **缓存调用有自己的预算**：Redis 读 200ms、写 500ms，超时/错误即降级回源，不会因为依赖抖动把请求拖到 gRPC 的 30s 超时；`logos.cache.redis.count{op,result}` 把 `miss / timeout / error` 分开统计，避免正常未命中稀释错误率。预算覆盖**全部**方法（批量、Hash、List、Set、ZSet、Pipeline、Ping），不是只包 `Get/Set`——只包一部分等于没有预算，未包装的调用照样会把 goroutine 挂到调用方超时；静态测试会拦住新增方法绕过 `runOp/runErr`
- **长对话上下文压缩**：历史消息按 token 预算装载，超出预算的早期对话滚动摘要为「对话背景」注入（按会话缓存，均摊成本 O(新溢出消息数)）；摘要失败自动降级为截断，不阻塞主流程
- **Prompt 注入防护**：规则引擎检测疑似指令注入（中英文 15 类模式），命中时注入安全护栏而非拒绝（控制误伤）；RAG 检索结果统一加内容边界声明，防止知识库文档中的间接注入
- **用户 URL 抓取防 SSRF**：文档导入的 URL 来自用户，不能直接 `http.Get`；`pkg/safehttp` 做协议白名单（仅 http/https）、拒绝 URL 内凭据、**DNS 解析后逐 IP 校验**（回环/私网/CGNAT/链路本地/云元数据 169.254.169.254/IPv6 ULA 全部拦截）、重定向逐跳复核、下载响应体 100MB 上限。校验和拨号使用同一份解析结果，避免 DNS rebinding 的 TOCTOU
- **多 Bot 协作**：支持配置多个不同人设/能力的 Bot，按场景自动路由或由用户指定
- **Plan-and-Execute 任务规划**：Bot 可开启规划模式（`enable_planner`）——复杂任务先由 Planner 生成显式步骤清单（1-8 步，可展示可审计），逐步交给带工具的 ReAct Agent 执行（单步内工具调用能力完整保留），步骤失败自动重规划一次而非整体失败，最后由 Synthesizer 综合作答；流式模式下规划与步骤进度实时推送到前端
- **记忆管理**：Bot 记住用户偏好与历史交互，跨会话保持上下文；支持 7 种记忆分类（preference/habit/fact/goal/relationship/style/other），手动添加的记忆受保护不被自动清理
- **记忆置信度生命周期**：LLM 抽取的记忆带证据引用（消息 ID，可审计；编造引用整条作废）与归一化置信度（异常值 → 0.5"不知道"）；跨轮次三态合并——确认 +0.05 封顶 0.98（重复确认 ≠ 绝对真理）、补充取更详细值、矛盾 −0.15 保底 0.2 并保留双结论不自动选边；注入 prompt 双档消费：< 0.5 不注入（agent 不会因为标注就不当真）、[0.5, 0.65) 标注「⚠︎证据较少」、类内按置信度降序、截断声明条数
- **消息总结**：一键总结群聊/单聊历史消息，生成要点摘要与待办提取
- **智能回复候选**：根据上下文生成回复候选，用户可一键选用
- **批量推荐**：`BatchGetRecommendations` 固定 8 路有界并发（信号量背压），重复 userID 由单飞合并，避免串行 N 次往返或一次性打满连接池
- **批量向量化**：多模态 Embedding 接口一次只收一条文本，批量只能拆成多次 HTTP；改成 4 路有界并发 + 结果按下标保序 + 首错取消剩余请求，响应体 8MB 上限、错误报文截断 512 字节。串行时 100 条文本就是 100 次往返叠加，无上限并发又会把供应商打限流
- **实时多语言翻译**：支持前端配置翻译模型（DeepSeek/OpenAI 等），不再硬编码
- **消息审核**：接入模型对消息内容进行实时审核与过滤；审核模型不可用或输出无法解析时按**失败关闭**处理，直接拒绝群聊发送而非放行

### 计费管理

- 支持用户提供自己的 API Key，也可使用平台模型
- 完整的计费记录与余额管理
- 多 Provider 计费支持（DeepSeek/OpenAI/Claude 等），根据模型名称自动推断 Provider
- **按真实 token 用量计费**：由 eino 采集模型响应里的 `ResponseMeta.Usage`（prompt/completion tokens），一次请求内的多轮调用（ReAct、Planner、反思）累计后按模型单价结算；供应商未返回 usage 时才回退本地估算，并在交易 metadata 用 `token_source`（`eino` / `estimate` / `client`）标记来源；估算占比经 `logos.llm.token_source.count` 指标统计，超过 20% 触发 `LLMTokenUsageEstimateRateHigh` 告警
- **支付单 + 签名回调 + 幂等入账**：用户只能创建支付单（`POST /billing/payments`），金额上限、支付方式白名单在服务端校验；支付渠道回调 `POST /billing/payment-callback` 走公开路由，但必须通过 HMAC-SHA256 验签（`PAYMENT_CALLBACK_SECRET`，未配置时拒绝所有回调），并校验回调时间戳 ±5 分钟、回调金额与订单金额一致；订单 `pending -> paid` 使用 `WHERE id=? AND status=0` 条件更新，重复/并发回调只有一次能入账，回调 nonce 加唯一索引防重放；过期订单在查询/回调时被条件更新为 `expired`，不会残留脏 pending；回调结果按 `result`（paid/invalid_signature/amount_mismatch/order_expired...）打点到 `logos.payment.callback.count`，超过阈值触发 `PaymentCallbackFailureRateHigh` 告警；原 `Deposit` 仅保留为内部/管理端 RPC，不再对普通用户开放；支付单对当前用户可查列表 `GET /billing/payments?status=&page=&page_size=`（服务端强制按登录身份过滤，`page>=1`、`page_size` 上限 100，status 非法直接 400）；支付方式白名单与单笔金额上下限由 `PAYMENT_ALLOWED_METHODS`（逗号分隔）/ `PAYMENT_MIN_AMOUNT` / `PAYMENT_MAX_AMOUNT` 配置，缺省或非法值回退内置默认（alipay/wechat/stripe/mock，0.01~100000）
- **过期支付单分层回收**：按需回收（查询订单/回调时条件更新为 `expired`）只覆盖被访问过的订单，服务内另有后台回收器（`PaymentOrderReaper`）按周期做有界批量更新，兜住"下单后直接关页面"的 pending 单；批量大小与周期可配置，回收数量打到 `logos.payment.order.expired.count`，失败次数打到 `logos.payment.reap.failure.count` 并触发 `PaymentOrderReapFailing` 告警，`Stop` 会等待当前批次结束再退出
- **计费不随请求取消**：用户中途断开连接/切换会话会取消请求 ctx，计费使用 `context.WithoutCancel` 剥离取消信号并单独限时；流式会话即使因客户端中断提前返回，也按已消耗 token（真实 usage 优先）结算，`sync.Once` 保证一次会话只扣一次
- 交易记录正确展示金额与时间（兼容 proto Timestamp 对象格式）

### 系统监控

- **服务状态面板**：实时展示所有微服务运行状态（UP/DOWN/未上报）
- **双模式连接信息**：同时展示 etcd 服务发现名称和本地直连地址
- **指标/日志/告警**：支持按服务、类型、级别等多维度查询
- **自动采集**：Monitoring 服务启动后自动通过 etcd 采集各服务注册状态。采集用一次 `WithPrefix("/grpc/")` 查询拿全部实例（19 个服务逐个往返会与业务查询争抢连接池），结果按服务名归并后用一条 `INSERT ... ON CONFLICT DO UPDATE` 批量落库（逐条 `FirstOrCreate`+`Updates` 是 38 次往返）。etcd 查询失败时不会把全部服务标成 `DOWN`——那会把「监控失明」误报成「全站宕机」，而是保留上次判定、降级为 `DEGRADED` 并写明原因；查询带 10s 超时，etcd 卡住不会让采集循环停摆
- **监控数据保留策略**：只写不删，监控库最终会被自己的数据撑爆。`RetentionCleaner` 每小时清理一次：metrics/logs 默认保留 7 天、已解决告警保留 30 天（未解决告警不删，避免丢失排查上下文），可用 `MONITORING_*_RETENTION_DAYS` 调整，非法值回退默认。删除按 5000 行一批、单表单轮最多 10 批，用 `id IN (SELECT ... ORDER BY timestamp LIMIT ? FOR UPDATE SKIP LOCKED)` 分批推进：一次性删几十万行会长时间持锁、撑爆 WAL 并阻塞采集写入；`SKIP LOCKED` 让多副本各删各的。`logos.monitoring.retention.deleted{kind,result}` 区分「删了 0 行」和「清理失败」——后者意味着表会重新开始膨胀
- **请求上下文全链路透传**：Gateway 所有接口统一以 `c.Request.Context()` 调用下游 gRPC，客户端断开即取消下游计算（避免继续烧 LLM token），trace 与调用方身份 metadata 随链路传递
- **入口防护**：网关对普通请求体设 2MB 上限（`Content-Length` 超限直接 413，chunked 用 `MaxBytesReader` 兜底），只有白名单上传路由才跳过 multipart 限制，普通接口伪造 `Content-Type` 也不能绕过；CORS 默认通配但不开凭据，需要凭据时用 `CORS_ALLOWED_ORIGINS` 白名单并返回 `Vary: Origin`；OPTIONS 预检放在限流之后短路，避免绕过配额；默认不信任任何代理，`TRUSTED_PROXIES` 显式配置后才采信 `X-Forwarded-For`，防止伪造 IP 绕过限流
- **突发封禁必须真的读**：突发保护会写 `blocked:<ip>`，但旧实现从不读取它——计数窗口一过期，被封的 IP 立刻恢复，所谓「超限后短期封禁」只是往 Redis 写了个没人看的 key。现在请求入口先查封禁键并短路（不再自增计数），封禁时长为窗口的 2 倍；Redis 不可用时退化为单机固定窗口，窗口内超限同样拒绝，不会因为依赖故障变成无限流。封禁写入用 `context.Background()`，避免攻击者「发起即断开」让写操作随请求取消而回滚
- **文件代理防存储型 XSS**：`/api/v1/file/minio/*` 的 `Content-Type` 来自上传时的客户端声明，不能原样回传。代理层只允许图片/音视频/PDF/Office 文档内联展示；`text/*`、`image/svg+xml`、XHTML、JavaScript 等可在同源执行的内容统一降级为 `application/octet-stream` 并加 `Content-Disposition: attachment`，同时始终返回 `X-Content-Type-Options: nosniff`；上游请求带 60s 超时，chunked 响应不再写非法的 `Content-Length: -1`
- **对象存储写接口鉴权**：通用上传/删除/取预签名 URL（`/api/v1/file/upload*`、`DELETE /api/v1/file`、`/api/v1/file/url`）移到 JWT 鉴权组——否则任何人都能往存储写文件、删除已知对象或换取 30 天预签名链接。媒体代理 `GET /api/v1/file/minio/*` 保持公开（`<img>` 无法带 Authorization 头），安全性由不可猜测的 UUID 对象键 + 类型白名单保证；所有对象键入口统一拒绝 `..`、空路径段、反斜杠与控制字符
- **上传守卫**：BodyLimit 跳过 multipart 不等于上传无上限。`pkg/uploadguard` 在解析前限制整个请求体、读取时用 `io.LimitReader` 兜底、写临时文件/对象存储用有界 `Copy`；头像 5MB 且必须通过内容嗅探（HTML 改名成 .jpg 会被拒），聊天媒体 500MB 先看 `header.Size` 再流式上传（不再整份读进内存），文档/通用文件 100MB、批量最多 10 个；扩展名统一净化，避免奇怪字符进入对象键
- **内网服务身份校验**：process 服务不信任 `X-User-ID`，入口独立校验 JWT 后再写入 metadata；service 层拒绝空身份，避免空 userID 被 DAO 解释成「查所有用户」；文档块读取先校验 owner。compose 中 process 仅 `expose` 到服务网络，不发布宿主机端口
- **下游超时与连接池**：网关到 process 的普通转发统一 30s、上传 10min，所有请求透传 `c.Request.Context()`；Transport 设置拨号超时、响应头 30s、空闲连接回收与单 host 上限，避免下游卡死时网关 goroutine/fd 被拖满
- **HTTP 入口默认值**：`pkg/httpserver` 统一 `ReadHeaderTimeout=10s`、`IdleTimeout=120s`、`MaxHeaderBytes=1MB`；process 与 monitoring 看板都监听 SIGTERM 后先停止接收连接，再以 15s 窗口排空在途请求。排空超时会显式 `Close` 强制断开残留连接——`Shutdown` 本身不会强杀，调用方若选择不退出进程，卡住的连接与 goroutine 会一直挂着。monitoring 的监听失败（如端口被占用）只禁用看板并记录错误，不会连带 gRPC 服务退出——看板是旁路能力，不该成为整个服务的单点
- **启动失败不留半成品**：Gateway 的 HTTP/TCP 端口都在启动阶段显式 `net.Listen`，端口占用直接返回错误；HTTP `Serve` 在运行期失败也回到统一停机路径（先排空 WS/TCP/广播器再退出），而不是在 goroutine 里 `log.Fatalf` 跳过所有清理。`run()` 返回错误的设计让 OTel flush、客户端连接关闭在失败路径上同样生效
- **存储初始化可自愈**：`InitMinio` 不再用 `sync.Once` 永久缓存失败——那样首次失败后第二次调用会返回 `(nil, nil)`，调用方当成功用、随后 nil 解引用。改为串行初始化 + 5s 失败负缓存：依赖恢复后自动重试，并发首次调用只探测一次；启动探测（BucketExists/MakeBucket）带 10s 上限，MinIO 不可达不会把启动流程挂死。上传/下载/删除/预签名/存在性检查全部接受调用方 ctx，客户端断开即停止占用连接与临时文件
- **依赖初始化统一语义**：`pkg/lazyinit` 把「成功永久复用 / 失败只冷却一段时间后重试 / 并发只探测一次 / 永不返回零值+nil」收敛成一个原语，MinIO、Elasticsearch、Neo4j、Milvus、Eino 全部接入。`sync.Once` 不分成功失败，一次失败就永久钉死；对 ES/图库/向量库/LLM 这种可能晚于应用启动的依赖，必须允许自愈。Neo4j 驱动在连通性校验失败时会显式 `Close`，避免每次重试泄漏连接与 goroutine
- **消费链路 ctx 不断链**：Kafka handler 只拿到 `*Message`，原先下游写 ES/数据库只能退回 `context.Background()`——进程停机后消费循环已退出，在途写入却还在跑。现在消费循环把生命周期 ctx 挂到 `Message.Context`，ES 实体同步与索引统计都使用它；handler 需要更短预算时再自行派生，既保持既有签名不变，又不让停机失去约束
- **启动重试有总预算**：Milvus 连接原先固定重试 60 次、每次 10s 超时 + 3s 间隔，最坏可挂 13 分钟，K8s 等不到结论就会先杀 Pod。现在改为 2 分钟总预算，单次尝试超时被剩余预算截断，日志明确记录「预算耗尽」而不是无限「重试中」；ES 启动 Ping 与 Neo4j 连通性校验同样各加 10s 上限（go-elasticsearch 默认无请求超时，地址可路由但不应答会永久挂住初始化），Neo4j 校验失败后用 3s 预算关闭驱动，避免重试泄漏连接
- **存活/就绪分离**：`/livez` 只回答「进程还在不在」，永不查依赖；`/readyz` 检查依赖，不可用返回 503 并列出失败项。两者混用会造成真实事故——拿就绪探针当存活探针，数据库抖动会让编排系统把所有副本一起重启；拿存活探针当就绪探针，依赖挂了仍然接流量，用户看到的全是超时
- **gRPC 依赖感知健康**：`pkg/health` 注册中心支持硬依赖与可选依赖；探测并发执行、共享 3s 总预算、2s 结果缓存，并发探针合并成一次回源（失败结果也缓存，避免探针自己把过载的下游打死）。依赖状态变化时同步更新 `grpc_health_v1` 的具名服务与空 service 名（`grpc_health_probe` 默认查后者），状态变化才打日志，正常探测不会刷满日志
- **依赖分级而不是一刀切**：PostgreSQL 是 Chat/User/Billing 等服务的硬依赖，不可用即摘流量；Redis 只是缓存/限流加速器，降级为 `degraded` 但不摘流量（网关限流有单机兜底、用户查询会回源）。把可选依赖也当硬依赖，一次缓存抖动会摘掉整个网关，故障反而被放大
- **探针不制造新连接**：复用已初始化的 `*sql.DB` / `redis.Client` / MinIO client 做 `Ping`/`BucketExists`，不在健康检查里另建连接池；Milvus/ES 用轻量 `ListCollections`/`Ping`，且不打印 INFO 日志，避免每 10s 一次的探针把日志刷满
- **就绪指标与告警**：`logos.health.readiness.count{service,result=ready|not_ready}` 只统计判定结果、不与正常探测混算成功率；`ServiceNotReady` 告警按 service 分组，实例被摘流量时能直接定位是哪个服务、哪类依赖
- **就绪状态驱动服务发现**：健康检查不能只改 K8s 探针结果，还要改 etcd 里的注册 key。否则实例已被判定未就绪，etcd 里仍留着地址，使用 etcd resolver 的客户端会继续把请求打过来。连续 `ReadyFailureThreshold`（默认 2，约 20s）次未就绪才摘除，避免单次依赖抖动让所有副本同时退出服务发现；任意一次恢复就立即重新注册，缩短摘流量时间。摘除时只删 key、保留 lease 与连接，恢复无需重建 etcd 连接；若 lease 已失效则重新 Grant 后再 Put。注册/摘除操作带 5s 超时，etcd 不可达时不会把探测 goroutine 挂死；无依赖探针的服务另有校正循环，首次注册失败或 lease 中途失效也能自愈，不必重启
- **部署探针同步**：K8s 中 gRPC 服务由 `tcpSocket` 改为 `grpc_health_probe`（只连端口无法发现「进程活着但依赖挂了」），并补齐 liveness/readiness 配对；process 是 HTTP 服务，单独使用 `/livez` 与 `/readyz`；滚动更新示例把 `maxUnavailable` 压到 0，新副本通过就绪探针后才下线旧副本
- **对象级授权**：collection 的公开方法统一要求 user_id，单资源读写全部带 owner 条件；跨用户更新、删除、执行任务与读取结果均返回 404，不泄露资源是否存在
- **连接池安全默认值**：`sql.DB` 的零值语义容易踩坑（`MaxOpen=0` 不限、`MaxIdle=0` 不保留空闲连接），初始化时统一归一化并补 `ConnMaxIdleTime`，启动日志打印实际生效值

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
│   ├── httpserver/                   #   HTTP 入口超时与优雅停机
│   ├── jwt/                          #   JWT 认证
│   ├── logger/                       #   Zap 日志
│   ├── mq/                           #   Kafka
│   ├── obs/                          #   OpenTelemetry 可观测性
│   ├── outbox/                       #   Outbox 模式 (事件可靠投递)
│   ├── pagination/                   #   分页参数统一边界（page/pageSize/ES 窗口）
│   ├── push/                         #   离线推送
│   ├── ratelimit/                    #   Redis 限流器
│   ├── register/                     #   Etcd 服务注册
│   ├── safehttp/                     #   用户可控 URL 抓取（SSRF 防护）
│   ├── singleflight/                 #   并发回源合并（防缓存击穿）
│   ├── storage/                      #   MinIO 对象存储
│   ├── structured/                    #   LLM 结构化输出解析与一次修复
│   ├── strutil/                      #   字符串工具
│   ├── uploadguard/                  #   上传大小/类型/扩展名守卫
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
| 请求体上限 | JSON 2MB，multipart 跳过         | 网关中间件；先看 Content-Length，再用 MaxBytesReader 兜底 |
| gRPC 消息上限 | 默认 4MB，最大 64MB            | 服务端/客户端分别读取 `grpc.server` / `grpc.client` 配置，之前只写配置未生效 |

### 事务性发件箱（Outbox）

`pkg/outbox/` 实现与业务同事务写入的 at-least-once 事件投递，投递分三段执行，避免把 Kafka 网络往返放进数据库事务：

1. **短事务领取**：`SELECT ... FOR UPDATE SKIP LOCKED` 取一小块消息，同时把 `next_retry_at` 写成租约截止时间、`lease_owner` 写成实例级 fencing token，提交即释放行锁 —— 多副本部署不会重复领取同一批消息，进程崩溃时租约到期自动回到可领取状态
2. **事务外发送**：逐条 `producer.Send`，网络抖动不再长时间持有行锁与连接池连接
3. **单语句标记**：发送成功置 `sent`，失败按重试次数置回 `pending` 并设置退避；标记失败时消息会在租约到期后重投，属于 at-least-once 语义，消费端需保证幂等

- **失败重试**：30s 起步的指数退避（30s×2ⁿ，上限 32 分钟），最多 5 次
- **死信终态**：重试耗尽进入 failed 终态保留排查，并触发告警日志/告警钩子
- **死信重放**：每 10 分钟自动把 `replay_count < 3` 的死信重置回 pending，也支持人工确认后手工重放，避免毒消息被无限重投
- **自动清理**：已发送消息 24 小时后定期清理
- **租约必须覆盖投递耗时**：一轮 batch 最坏耗时是 `batchSize × sendTimeout`（默认 100 × 10s ≈ 17 分钟），远大于 30s 租约。只在开头领一次租约，后面的消息会在轮次结束前过期、被其他副本重复领取。改为按「单块最坏耗时」推导租约窗口（`size × sendTimeout + 余量`，且不小于 60s），每块开始前续租，把续租及时性变成构造性不变式而不是靠配置运气
- **fencing token 防旧副本覆盖**：租约到期后消息可能被新副本领走，旧副本迟到的 `MarkSent`/`MarkFailed` 会把新副本的领取结果覆盖掉。写回条件加上 `lease_owner`，`RowsAffected=0` 即判定 `ErrLeaseLost`：发送后才发现丢失的记为 `duplicate`（at-least-once 下由消费端幂等兜底），续租阶段发现丢失则整块放弃，不再产生新的重复
- **停机有界等待**：`Stop()` 不再只 cancel，而是等待在途轮次结束，避免「已发送、未落库」被租约到期重投；等待上限 10s，超时返回并交给租约自愈，不会被 K8s `terminationGracePeriodSeconds` 强杀

### Kafka 消费可靠性

- **at-least-once**：消费端用 `FetchMessage` 只拉取不提交，handler 成功后才 `CommitMessages`；失败不提交，下次拉取到同一条消息重投
- **毒丸消息止损**：同一条消息（topic + partition + offset）连续失败 5 次后判定为毒丸，先转存到 `<topic>.dlq`（保留原始 topic/partition/offset/key/error 与原始负载），转存成功后才提交 offset；转存失败则落原始负载日志，保证可人工恢复
- **规范化**：消息按 `chat_id` 等 Key 哈希分区，同一会话有序；消费端通过数据库主键/唯一约束实现幂等
- **幂等键保留期**：`message_idempotency` 只覆盖客户端重试窗口，默认保留 7 天；每小时分批回收，每批 1000 行、单轮最多 10 批，并补 `created_at` 索引，避免表随消息量无限增长和一次删除锁表
- **内部 topic 名不能直接当 Kafka topic**：DAO/gRPC enum/统计使用 `QA_REQUEST` 这类大写下划线内部名，而 Kafka 常量是 `qa_request`。outbox relay 原样把 Topic 字段当 Kafka topic 发，导致 QA 事件被投进一个没人订阅的 topic——`dispatch=sent`，业务却永远不执行。现在由 `kafkaTopicFor` 做唯一映射，避免两套命名再次漂移
- **枚举基数不一致会整体错位一格**：proto 的 `Topic` 从 0 开始（`DATA_COLLECTION = 0`），DAO 旧实现却按 1 开始解析，结果 `DATA_COLLECTION` 写成 `UNKNOWN(0)`、`SYSTEM_EVENT` 永远不可达，每条消息都被投到相邻业务 topic；`Priority` 同样是 0 基 vs 1..4，导致 `NORMAL` 读回来变 `LOW`。现在映射只保留在 DAO/转换函数两处，越界枚举在写库前直接拒绝，`BatchSendMessage` 的响应也回显消息真实 topic，而不是硬编码 `pb.Topic(1)`
- **批量发送要么全入队要么全回滚**：`BatchSendMessage` 原先逐条调用 `SendMessage`，某条失败只打日志后 `continue`，最终仍返回 nil 错误，调用方把部分成功当全部成功；已提交的子集还会在重试时重复入队。现在先整体校验（topic/优先级/批量上限 100），再在**单个事务**里用一条多值 INSERT 写入全部消息与对应 outbox 事件，任一步失败整批回滚，重试不会产生半批数据
- **问答执行不能裸起 goroutine**：handler 里 `go AskQuestion(Background)` 后立即返回 nil，offset 会被提交；LLM 失败或进程崩溃时请求永久丢失，突发流量还会堆出无界 goroutine。现在消费端同步执行并返回错误，复用既有的就地重试/死信路径；单条问答应带 120s 上限，且使用 `Message.Context`，停机时可取消
- **批量确认不能吞错**：`BatchAcknowledgeMessages` 原先逐条 UPDATE，失败只打日志后 `continue`，最后仍返回成功——客户端以为整批确认完成，实际部分消息会重投。现在过滤空/重复 id 后一条 `WHERE id IN (...)` 更新，失败整批返回错误交由上游重试；消费后状态更新失败同样上抛，避免 offset 已提交但消息状态永远停在旧值
- **消费确认改成租约制**：旧实现 `ConsumeMessages` 先查 `PENDING`、返回前就改成 `PROCESSED`，客户端拿到消息后崩溃或超时，这条消息再也不会出现在待处理集合里（at-most-once），并发消费者还可能领到同一批。现在用一条 `WITH ... FOR UPDATE SKIP LOCKED ... UPDATE ... RETURNING` 原子领取，同时写入 `claimed_by / lease_until / attempts`；租约 60s 内必须 ACK，过期后消息重新可领取（at-least-once）；ACK 只接受 `PROCESSED/ACKED` 且属于本消费组的行，迟到确认返回 404 而不是覆盖新消费者
- **统计接口不能 N+1，也不能被空表打挂**：`GetMessageStats` 原先 1 次全局统计 + 7 次按 topic 全表扫描；而且 `SUM(CASE ...)` 在空集返回 `NULL`，扫描进 `int64` 会报错，队列为空时统计页直接 500。现在一条 `GROUP BY topic` 返回全部聚合，`COALESCE(SUM(...), 0)` 把空集归零，服务层再为没有数据的 topic 补零行，客户端表格不会缺列。删除 topic 时订阅清理失败也不再只打日志——事务内任何一步失败都整批回滚
- **状态字符串必须有唯一来源**：Kafka 消费成功后写入的是 `PROCESSED_FROM_KAFKA`，统计 SQL 只累加 `PROCESSED/ACKED`——消息确实处理了，监控却永远显示 0。现在 `PENDING/PROCESSED/ACKED/FAILED/CLEARED` 集中定义在 model 常量里，写入、统计、清理全部复用同一组常量，统计 SQL 用参数绑定而不是再抄一遍字面量
- **路径参数不能把解析失败吞成零值**：`DeleteTopic` 里 `strconv.ParseInt` 的错误被 `_` 丢弃，`DELETE /message/topic/abc` 会退化成 `Topic=0`，在网关层“成功”删掉 `DATA_COLLECTION` 的全部消息与订阅——一次手误就是跨租户数据丢失。现在解析失败、负数、越界枚举都在网关层返回 400，且拒绝时不会发出下游 gRPC 调用
- **成功码不能碰巧等于 HTTP 200**：各 handler 把 gRPC 的 `StatusCode` 直接塞进 `c.JSON`。业务成功码是 `0`，HTTP 写回时靠 gin 的 `WriteHeader` 跳过非正数才“看起来是 200”；任何一层把成功码改成 1 或漏填，整个网关就会返回 500/畸形状态。现在整层统一到三处唯一出口：`BaseResp` 风格走 `mapBaseRespStatusCode`，裸 `int32 code` 风格（bot / billing / MCP / moderation / summary）走 `mapStatusCode` 或 `respondProtoCode`，映射规则是 `0 → 200`、`1 → 500`、`100..599` 直通、其余 500。响应体里的 `code` 与状态行复用同一个值，前端按 `body.code` 判断成功、监控按状态行统计失败率，不会再得到互相矛盾的结论
- **timeout_ms 不能是装饰参数**：`ConsumeMessages` 收了 `timeout_ms` 却从不使用，调用方以为自己在长轮询，实际每次立刻返回空列表——客户端要么高频空转打爆 DB，要么把「暂时没有」误判成「队列为空」直接退出。现在 `timeout_ms > 0` 时在预算内每 200ms 重新领取，拿到消息立即返回、预算耗尽返回空列表（正常超时）；`<= 0` 保持单次尝试语义，超过 30s 收敛到上限。等待用 `timer + select ctx.Done()`，客户端断开立刻退出，不留 goroutine；领取错误仍然上抛，不会被当成「队列为空」
- **分页参数不能静默变成 0**：各列表接口统一写 `page, _ := strconv.Atoi(...)`，`?page=abc` 解析失败后 `page=0`，再算 offset 就得到负数；`?pageSize=1000000000` 则原样透传给数据库/检索后端，一次请求把全表拉进内存。现在网关统一走 `parsePagination` / `parseLimit`：非数字、`0`、负数直接返回 400 且**不发出下游 gRPC 调用**，超过上限的 `pageSize` 收敛到 `pkg/pagination.MaxPageSize`（100），缺省与空值回落到默认页大小；`pageSize` / `page_size` 两种历史写法共享同一套校验，避免只修一条入口。handler 目录的静态守卫会拦住新增的 `strconv.Atoi(c.Query("page"))` 式写法，防止边界再次退化
- **参数名错位比解析失败更隐蔽**：索引接口的路由是 `DELETE /search/indices/:id`、`POST /search/indices/:id/refresh`，handler 却统一读 `c.Param("type")`。读不到值不会报错，`parseBoundedInt` 拿到空串后回落 0，于是 `DELETE /indices/4` 删掉的实际是 `ENTITY` 索引；而 `POST /indices` 根本没有路径参数，被同一个函数判成非法，建索引接口恒定 400。现在拆成两个入口：`parseIndexTypeQuery` 读 `?type=`（缺省 `ENTITY`，0 是合法枚举值，不能用「0 表示没传」判断），`parseIndexTypePath` 读 `:id`（缺失/非数字/越界一律 400）。测试用 fake client 断言「`:id=4` 下发 `DOCUMENT`」「`:id=abc` 返回 400 且下游调用 0 次」，并在静态守卫里锁死 `handler_search.go` 不允许再出现 `c.Param("type")`
- **身份不能来自请求参数**：问答历史、推荐记录、用户统计原先都用 `?user_id=` 决定查谁的数据，解析失败再静默变 0；任何登录用户改一个数字就能读到他人的问答历史与行为画像（水平越权 / IDOR），未认证请求则会退化成「查用户 0」。现在身份只认认证中间件写入的 `user_id`：`requireNumericUserID` 负责转成数据库主键（缺失或非数字 401），`resolveTargetUserID` 允许保留 `?user_id=` 做兼容，但只接受等于当前登录用户，否则 403。头像上传同样绑定登录身份，不再出现「解析失败就往 user 0 写头像」。静态守卫禁止 handler 目录（`handler.go` 除外）直接读 `user_id` 查询参数
- **类型断言省掉 `ok`，缺身份就从「未认证」变成「panic」**：网关 handler 里的 `userID.(string)` 只写了一元断言，中间件没写入 key 或值不是字符串时直接 panic，一个不带身份的请求就能把 handler 打挂；另一种写法 `req.UserId, _ = userID.(string)` 更隐蔽——不 panic，但把身份静默降级成空串，请求继续往下跑。现在统一收敛到 `requireUserID(c)`：key 缺失、类型不符、空串一律 401，且**不发出下游 gRPC 调用**；`handler_guard_test.go` 静态扫描 handler 目录，禁止新增 `, _ := c.Get(`、`, _ = c.Get(` 与裸 `userID.(string)`，把「先断言再判空」变成编译期之后仍能守住的约定
- **「过滤条件缺失」比「过滤条件写错」更危险**：Bot DAO 的 `ListBots` / `ListConversations` / `ListPrompts` / `GetUserMemoriesByUser` 都写成 `if userId != "" { query = query.Where(...) }`，把「可选过滤」当成了默认行为。空身份不是「查不到数据」，而是**不加任何 user_id 条件**——列表接口会返回全库用户的 Bot、会话、提示词与记忆，一次缺身份请求就是全量水平越权。修复分两层：网关侧 `requireUserID` 先 401；服务侧 `resolveCallerUserID` 用 `auth.RequireUserID(ctx)` 再从 metadata 取身份，空身份返回 `ErrUnauthenticated`，并且**在触达 DAO 之前**就返回，测试用 `stubRepo` 断言 `createdBots/convsQueried/memoriesQueried` 全为空
- **请求体里的 `user_id` 只是「声明」，不能当身份**：网关虽然已经把认证身份写进 metadata，下游 handler 仍把 `req.UserId` 直接透传给 service，service 又直接写进归属列。只要 gRPC 端口可达，调用方就能把 `user_id` 换成别人，绕过全部归属校验。现在 `resolveCallerUserID(ctx, claimed)` 以 metadata 为唯一真相：声明为空则忽略；声明与认证身份不一致直接拒绝并打 Warn（既防冒充，也留下攻击痕迹）；归属列一律写认证身份
- **未认证不能伪装成 500**：Bot handler 原先所有错误都写 `resp.Code = 1`，网关 `mapStatusCode` 把 1 映射成 500——用户看到「服务器内部错误」，不会去重新登录，告警也会把认证问题算进服务端故障率。现在服务层用 `auth.ErrMissingUser` 作为唯一哨兵，handler 用 `errors.Is(err, service.ErrUnauthenticated)` 显式返回 401，其余错误才落回 500。**错误码分类不是文案问题，它决定了谁来修**
- **下游塞进 message 的结果要显式解包**：推荐问题 RPC 的返回类型是 `common.BaseResp`，下游把 `{"questions":[...]}` 序列化进 `StatusMessage`，网关只透传 `message`，于是接口 200 但 `data` 恒为空，前端面板永远显示「暂无推荐问题」。同时 handler 读的是不存在的 `c.Param("userId")`，所有请求都在为「用户 0」生成推荐。现在改用登录身份、按 `limit` 截断，并把 `StatusMessage` 解析成前端期望的 `[{question}]`；解析失败返回 502 而不是用空数组伪装成「模型没生成」，避免两类问题在监控上表现一致
- **字符串枚举需要白名单，不能靠 if/else 兜底**：Bot 列表的 `?type=` 是一串 `if "builtin" ... else if "custom" ... else UNSPECIFIED`。拼错（`bulitin`）、大小写不一致（`Builtin`）、误传数字（`1`）都会掉进最后的 else，被当成「不过滤」——接口 200 返回全部 Bot，调用方以为筛了「内置 Bot」。现在 `parseEnumQuery` 要求枚举名命中白名单，未命中直接 400 并列出排序后的合法值；数字不被接受，避免把 proto 的数值编号泄漏成公开 API（调整枚举顺序就会悄悄改变语义）
- **布尔开关的安全语义不能反向降级**：登出接口用 `c.Query("all") == "true" || == "1"` 判断是否全设备登出，`?all=yes` 两条都不命中，静默变成 false —— 用户以为退出了所有设备，实际只退出了当前这台。这类「安全语义刚好反了」的静默降级比报错危险得多。现在 `parseOptionalBoolCompatQuery` 区分「缺省 false」「显式 true/false/1/0」与「非法值 400」，保留前端历史写法但不再猜测意图
- **进程内 handler 也要有自己的参数边界**：网关做了校验不代表内网服务可以假设输入合法，`compose` 里 process 服务曾经直接发布端口。文档列表的 `page, _ := strconv.Atoi(...)` 会把 `?page=abc` 吞成 0（DAO 归一化后固定返回第一页），`status` 解析失败则让指针保持 nil —— 等于**取消过滤**并返回全部状态，`?status=99` 还会原样下传 SQL。现在解析收敛成纯函数 `parseDocumentListQuery`：非数字/非正数/未知状态 400，`page_size` 超限收敛并回显**实际生效**的值（旧实现响应里仍写着 1e9，前端按它算出的页数和服务端真实分页对不上）
- **先改状态、后校验依赖，是最糟的顺序**：采集任务的 `ExecuteTask` 先把任务置为 `RUNNING` 并写库，再去查数据源；`UpdateTask` 的错误被 `_` 丢弃，写库失败也照跑，而数据源被删或查询失败时 `ds=nil`，`doCollect` 里的 `switch ds.Type` 直接 panic。panic 之后没有任何回滚，任务永久停在 `RUNNING`，前端只能看到「一直在采」。现在顺序反过来：先查数据源（查询失败上抛、不存在返回 `ErrNotFound` 保持 404 语义），再改状态且落库失败立即返回。**能被回滚的状态才是状态，改完就忘只是日志**
- **计数器和「成功」必须挂在真正的副作用上**：采集器原先调 `AddKnowledge` 后不看返回值，`CollectedCount`/`ProcessedCount` 照样自增；一条都没写进知识库，任务仍返回 `SUCCESS`，用户在知识库里搜不到任何东西。现在统一走 `addKnowledge`：知识服务未配置返回 `ErrKnowledgeUnavailable`，写入失败直接中断并带上「第 N 条」的定位，计数只在真正落库后自增。**统计口径不是业务结果的旁证，它本身就是承诺**
- **失败结果不落库，等于把故障藏起来**：抽取任务的失败分支只 `logger.Error` 后返回，`CreateResult` 的错误被丢弃，任务状态也没回写 3/4，`EndedAt` 永远是空。结果就是任务卡在 `RUNNING`，接口查不到失败原因，排查只能翻日志。现在失败也要落一条 `Status=4` 的结果并写回任务状态与结束时间；落库本身失败时用 `errors.Join` 把「抽取失败」和「失败结果没存下」两个原因一起返回，避免第二个错误覆盖第一个
- **跨服务的事件字段要对齐，不能靠猜**：采集服务 outbox 里写的是 `data_source_id`，抽取消费端读的却是 `data_id`，类型断言失败被 `_` 吞掉，`dataID` 恒为空串——每个采集事件都会建出一个 `data_id=""` 的抽取任务，任务详情里根本看不出数据来自哪个源。现在按生产方字段读取，缺少 `data_source_id` 直接拒绝建任务，`status != SUCCESS` 的采集事件跳过，`data_type` 由消费端显式写成 `data_source` 而不是从事件里透传。**契约要靠测试锁住：断言消费端建出的任务 `DataID` 等于事件里的 `data_source_id`**
- **限流器不能成为单点故障**：`NewRedisCache` 在 Redis 不可用或重连冷却期会返回 `nil`，而 `checkExtractionRateLimit` 拿到后直接 `IncrBy`，一次 Redis 抖动就把抽取服务打成 panic。限流是保护措施，不该比被保护的业务更脆弱：缓存拿不到时记 Warn 并放行，`Expire` 失败也不再静默。同时把缓存构造抽成包级工厂变量，单测注入内存实现，避免每次跑测试都去连真实 Redis（旧测试要等 30 秒连接超时）
- **类型断言失败被 `_` 吞掉，接口就会用 200 撒一个谎**：抽取结果转换里 `e["id"].(string)` 这种不带 `ok` 的断言，模型少给一个字段就 panic；而 `json.Unmarshal` 的错误被丢弃后，`ListExtractionResults` 会返回 200 + 空数组 —— 调用方无法区分「没抽到东西」和「结果存坏了」。现在所有字段转换收敛到 `parseStringField`/`parseFloatField`/`parseInt32Field`：字段缺失给零值、类型不符带字段名报错，位置字段额外校验 int32 范围与非负；批量列表失败时清空已拼好的半截结果，并用「第 N 条」定位。**可空和坏数据必须能分开，否则前端只能靠猜**
- **`err == nil &&` 会把「查询失败」当成「没有记录」**：记忆写入先按 key 查已有条目，写成 `if err == nil && existing != nil` 后，数据库抖动直接落到新建分支，同一个 key 插出重复行；`known, _ = GetUserMemoriesByUser(...)` 丢掉快照错误，则本轮所有条目都失去相似合并机会。现在单条查询失败计 `Failed` 并跳过、快照失败整轮不写 —— 不确定就不插，比插入重复数据便宜得多。统计结构 `memorySaveStats`（Processed/Saved/Failed/Deduped/Merged）同时供日志和测试断言使用。**写路径上的「读失败」必须显式降级，不能默认成空**
- **解析外部命令输出时，零值是最危险的默认值**：视频探测把 `hours, _ := strconv.Atoi(...)`、`width, _ = strconv.Atoi(...)` 全写成丢错，`Duration: abc:def:ghi` 会返回 0 秒，`1920x1080` 缺失则返回 0×0，接口依然 200。现在时长三段全部解析成功才接受、分秒必须落在 `[0,60)`、`N/A` 与缺行直接报错；分辨率只匹配「以数字开头的独立字段」，跳过 `(avc1 / 0x31637661)` 这类含 `x` 的十六进制编码标识（旧实现 `strings.Split(part, "x")` 会先命中它，得到 `0×31637661`）。探测本身合并成一次 `probeVideo`：ffmpeg 只给 `-i` 时非零退出属于正常现象，只有「完全拿不到输出」才算失败。上层 `VideoParser` 也改为逐项 Warn 而不是整段静默丢弃，抽帧失败仍保留 ASR 文本。**外部输出的解析函数要能脱离二进制单测，否则这类 bug 只能等线上发现**
- **统计值跨层传递要用结构体，不要用 `map[string]interface{}`**：消息统计原先在 DAO 拼 `map[string]interface{}`，handler 用 5 次 `v, _ := m[key].(int64)` 取值。字段名拼错或类型从 `int64` 变成别的，断言失败后每个计数都静默变 0——监控面板把「有积压」显示成「一切正常」，这比直接报错更难发现。现在 DAO → service → handler 全程传递 `[]dao.TopicStats`，字段由编译器保证，未知 topic 显式跳过，查询失败返回 500 且不携带统计。**能用类型系统表达的约束，不要留到值班时靠肉眼核对**
- **「逐条删 + 吞错」是假成功**：清空用户记忆原先先查全部 ID，再循环 `_ = DeleteUserMemoryByID(...)`。N 条记忆要 N+1 次往返；中途失败被 `_` 丢掉，接口仍返回成功，用户以为清空了、库里还留着半截。现在收敛成一条 `DELETE ... WHERE user_id = ? [AND bot_id = ?]`，用 `RowsAffected` 回传真实删除数，失败直接返回 `ErrInternalServer`。**「部分成功」要么以错误形式暴露，要么就让它在一条语句里变成全成功或全失败**
- **一份状态只留一个真相，假回执宁可不发**：已读状态原先同时写 `conversation_participants.last_read_seq` 和逐条 `messages.is_read`，而后者在整个仓库里没有任何读取方，写失败还被 `_` 吞掉；更糟的是事件处理里 `GetMaxSeqByMessageIDs` 失败只打 Warn，继续用 `targetSeq=0` 推进位点（空操作）并把回执发给发送方——发送方看到「已读」，阅读方的未读数却一动不动。现在删除无人读取的 `is_read` 写入路径，位点成为唯一真相；seq 查询失败直接上抛交给消费重试，消息全部失效时不发空回执。**同一事实写两份，迟早会分叉；分叉时又静默，等于给用户两个互相矛盾的答案**

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

- **令牌桶必须保留分数令牌**：补充量如果用 `math.floor(elapsed × rate)` 取整、又把时间基准重置为当前时刻，分数部分就被永久丢弃。高频请求下每次只走过不到一个令牌周期，floor 恒为 0——桶一旦打空就再也不放行，限流从「按速率恢复」退化成「永久拒绝」。现在令牌与时间都不取整，只把已经补进来的量记账，剩余分数留到下次；桶键 TTL 取 `max(1h, 2 × 补满耗时)`，避免空闲用户的桶提前重置为满、凭空多出突发额度

## 监控与可观测性

- **Request-ID 链路贯穿**：Gateway 为每个请求生成/透传 `X-Request-ID`（UUID），响应头回写、日志与 monitoring 上报统一携带，单请求全链路可串联排障；透传值限 64 字符且只允许 ASCII 字母数字与 `-_.`，非法值丢弃并重新生成，避免日志注入与 trace 串链
- **观测旁路可丢弃**：访问日志上报不再「每请求一个 goroutine」，改为固定 4 个 worker 消费容量 4096 的有界队列；队列满立即丢弃并打 `dropped`，每条 RPC 独立 3s 超时，停机最多等 5s。观测系统变慢时不会反向拖垮网关
- **优雅停机**：全部 gRPC 微服务在 SIGTERM 时执行四段式下线——先停就绪探测（防止停机过程中 watcher 又把实例注册回去）→ 健康检查置 NOT_SERVING → 删除 etcd key 并撤销 lease（新流量立即摘除，无需等 lease 过期）→ GracefulStop 等待 in-flight 请求排空（默认 25s 宽限期，`GRPC_SHUTDOWN_GRACE` 可调，小于 K8s terminationGracePeriodSeconds 保证不被 SIGKILL 撕断）；Gateway HTTP 侧并行关闭 TCP/WebSocket/EventBus 并超时兜底

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
| `logos.outbox.pending.count` | Gauge | 无 | 当前 pending 消息数（含退避等待），反映积压规模 |
| `logos.outbox.oldest.pending.seconds` | Gauge | 无 | 最老一条 pending 的等待时长，反映投递延迟 |
| `logos.ratelimit.degraded.count` | Counter | `layer=global/user/ip/burst` | Redis 不可用、切换单机兜底的次数 |
| `logos.llm.parse.count` | Counter | `module`、`result=ok/failed` | AI 结构化输出解析成功/失败数（memory/moderation/summary/extraction） |
| `logos.llm.repair.count` | Counter | `module`、`result=fixed/failed` | 解析失败后一次定向修复的结果，用于判断修复是否值得 |
| `logos.ai.semantic_cache.count` | Counter | `op=lookup/store`、`result=hit/miss/error/unavailable/disabled` | 语义缓存命中与故障，命中率直接对应省下的 LLM 调用 |
| `logos.es.request.count` | Counter | `index`、`operation`、`result=ok/failed/canceled` | Elasticsearch 访问结果，取消与真实失败分开计数 |
| `logos.es.request.duration` | Histogram | `index`、`operation`、`result` | Elasticsearch 访问耗时，可直接算检索 P95 |
| `logos.db.statement.duration` | Histogram | `operation=query/create/update/delete/row`、`table`、`result=ok/failed` | GORM 统一埋点，所有 DAO 自动获得 SQL 耗时分布 |
| `logos.db.slow_query.count` | Counter | `operation`、`table` | 超过 200ms 的 SQL 条数，日志排查单条、指标回答“是否变多” |
| `logos.db.pool.connections` | Gauge | `state=open/in_use/idle` | 连接池使用情况，区分“SQL 慢”和“拿不到连接” |
| `logos.db.pool.wait.count` | Counter | 无 | 等待获取连接的累计次数 |
| `logos.db.pool.wait.duration` | Counter | 无 | 等待获取连接的累计耗时，可直接算每秒等待时长 |
| `logos.retention.cleanup.rows` | Counter | `table`、`result=ok` | 按保留期成功清理的行数（变更日志/幂等键） |
| `logos.retention.cleanup.failures` | Counter | `table`、`result=failed` | 保留期清理失败次数，失败不会中断消息主链路 |
| `logos.cache.fill.count` | Counter | `cache`、`result=fill/shared/error` | 缓存回源合并效果：`shared/(fill+shared)` 即击穿防护的实际拦截比例 |
| `logos.message.idempotency.count` | Counter | `result=created/replayed` | 消息幂等写入结果；`replayed` 占比即客户端重试率 |
| `logos.cache.redis.count` | Counter | `op=get/set/delete/...`、`result=hit/miss/ok/error/timeout/canceled` | Redis 操作结果；`miss` 与 `error` 分开，错误率分母不含正常未命中 |
| `logos.cache.redis.duration` | Histogram | `op`、`result` | 单次 Redis 耗时，可直接算 P95 与 200ms 读预算的余量 |
| `logos.tool.call.count` | Counter | `tool`、`result=ok/error/timeout/canceled/rejected` | 工具单次尝试结果；`rejected` 是熔断器直接拒绝，不代表后端又被调用一次 |
| `logos.tool.retry.count` | Counter | `tool` | 可重试错误触发的重试次数；与 `call.count{result=error}` 对比可判断抖动是否被吸收 |
| `logos.tool.breaker.count` | Counter | `tool`、`event=open/half_open/closed/open_rejected/half_open_rejected` | 熔断状态事件；`open` 突增说明某个工具后端持续不可用 |
| `logos.gateway.log_report.count` | Counter | `result=enqueued/dropped/ok/error` | 访问日志上报结果；`dropped` 是旁路队列的背压信号，主链路不受观测系统拖累 |
| `logos.gateway.log_report.duration` | Histogram | `result=ok/error` | 单条日志上报耗时，用于判断监控服务是否变慢 |
| `logos.document.reap.count` | Counter | 无 | 回收的孤儿文档数（长时间停留在 Pending/Processing） |
| `logos.document.reap.failure.count` | Counter | 无 | 文档孤儿回收失败次数，持续失败会让卡住文档无法自愈 |

配套告警规则见 `deployment/examples/prometheus-rules.yaml` 的 `logos.reliability` 组：

- `KafkaPoisonMessage`（critical）：5 分钟内出现毒丸，需人工检查 `<topic>.dlq` 并回放
- `KafkaConsumeFailureRateHigh`（warning）：消费失败率 > 5%，通常意味着下游 DB/Redis 异常
- `OutboxDeadLetter`（critical）：域事件已停止投递，需人工确认后调用 `ReplayDeadLetters`
- `OutboxDispatchFailureRateHigh`（warning）：投递失败率 > 10%，通常意味着 Kafka 抖动
- `OutboxBacklogHigh`（warning）：pending > 1000 持续 10 分钟，成功率高但吞吐不足
- `OutboxDeliveryLagHigh`（critical）：最老 pending 等待 > 5 分钟，域事件已出现业务延迟
- `RateLimiterDegraded`（warning）：Redis 限流不可用，已降级为单机兜底，全局配额不再准确
- `LLMParseFailureRateHigh`（warning）：LLM 输出解析失败率 > 20%，业务已在静默降级为空结果
- `LLMRepairIneffective`（warning）：修复失败率 > 50%，说明 prompt/schema 需要调整而不是继续重试
- `SemanticCacheUnavailable`（warning）：缓存查询错误/不可用占比 > 10%，命中率下降会直接推高 LLM 成本
- `ElasticsearchFailureRateHigh`（warning）：真实失败率 > 5%，分母已排除 `canceled`，避免被断连流量稀释
- `ElasticsearchSearchLatencyHigh`（warning）：检索 P95 > 500ms，通常是索引膨胀或分片不均
- `DBSlowQuerySurge`（warning）：按 `operation/table` 的慢 SQL 速率 > 1 条/秒持续 10 分钟，说明查询计划或数据量发生变化
- `DBPoolSaturation`（warning）：连接池 `in_use/open > 90%` 持续 10 分钟，请求可能已经开始排队
- `DBPoolWaitHigh`（warning）：连接等待累计耗时增速 > 0.5 秒/秒持续 5 分钟，说明瓶颈在连接池而不是 SQL
- `RetentionCleanupFailing`（warning）：1 小时内任意表清理失败，过期数据会持续堆积，需检查数据库连接与锁等待
- `CacheFillFailureHigh`（warning）：缓存回源失败率 > 20%，缓存不可用时会直接放大成接口错误
- `MessageIdempotencyReplaySurge`（warning）：幂等回放占比 > 30%，消息没有重复，但客户端/网关可能在放大重试
- `RedisCacheErrorRateHigh`（warning）：Redis 超时/错误占比 > 5%，缓存已降级为回源
- `RedisCacheLatencyHigh`（warning）：Redis P95 > 100ms，接近 200ms 读预算，继续恶化会触发超时降级
- `ToolCircuitOpen`（warning）：5 分钟内某工具熔断器打开，模型工具链路已主动停止打后端
- `ToolCallRejectedRateHigh`（warning）：熔断拒绝占该工具调用 > 20% 持续 5 分钟，需检查检索/图谱后端
- `GatewayLogReportDropping`（warning）：访问日志开始丢弃，说明有界队列已满、monitoring 服务跟不上请求量
- `GatewayLogReportFailing`（warning）：日志上报失败率 > 10%，观测链路自身故障，需先恢复 monitoring 服务
- `DocumentReapFailing`（warning）：文档孤儿回收持续失败，重启后残留的 Pending/Processing 不会自愈

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
| `LOGOS_CODE_SANDBOX`          | MCP 代码执行沙箱模式            | process                 |
| `CORS_ALLOWED_ORIGINS`        | 允许跨域携带凭据的来源（逗号分隔）      | 空（通配、不允许凭据）             |
| `TRUSTED_PROXIES`             | 可信反向代理 IP/CIDR（逗号分隔）    | 空（不信任任何代理）              |

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
