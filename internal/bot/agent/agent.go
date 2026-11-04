package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"Logos/internal/bot/toolbudget"
	"Logos/pkg/eino"
	"Logos/pkg/logger"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// ErrEmptyResponse Agent 走完整个 ReAct 循环却没产出任何正文。
//
// 这是一个必须显式上抛的失败态：调用方（Bot 服务）会把返回内容原样存库并计费，
// 空字符串 + nil error 会被当成「回答成功」，用户看到一条空白消息，监控上却是 100% 成功。
var ErrEmptyResponse = errors.New("agent 未产出任何内容")

// ErrEmptyRequest 组装后的消息里没有任何对话轮次（只有 system prompt 或全空）。
// 大多数供应商对「只有 system 的消息」直接 400，提前拦下能把错误定位到调用方，
// 而不是变成一条看起来像模型故障的上游报错。
var ErrEmptyRequest = errors.New("请求中没有有效的对话消息")

// agentEventIter 事件源的最小抽象（adk.AsyncIterator 天然满足）。
// 单独抽出来只为一件事：让「事件 → 增量输出」这段纯逻辑能脱离真实 LLM 单测，
// 不用起网络请求、也不用 sleep 等流式返回。
type agentEventIter interface {
	Next() (*adk.AgentEvent, bool)
}

// runSource 一次运行的事件源工厂。非流式与流式各持一个，测试可整体替换。
type runSource func(ctx context.Context, messages []*schema.Message) agentEventIter

type BotAgent interface {
	Chat(ctx context.Context, message string) (string, error)
	ChatStream(ctx context.Context, message string, onChunk func(string) error) error
	ChatWithHistory(ctx context.Context, messages []*schema.Message) (string, error)
	ChatStreamWithHistory(ctx context.Context, messages []*schema.Message, onChunk func(string) error) error
	GetName() string
	GetDescription() string
	GetSystemPrompt() string
}

type BaseBotAgent struct {
	id           string
	name         string
	description  string
	systemPrompt string
	// run / stream 是事件源工厂而非 *adk.Runner：运行结果由调用方逐事件消费，
	// 保留 Runner 只会把「怎么拿事件」和「怎么消费事件」焊死，无法单测消费逻辑。
	run    runSource
	stream runSource
}

type AgentConfig struct {
	ID                string
	Name              string
	Description       string
	SystemPrompt      string
	ChatModel         model.BaseChatModel
	Tools             []tool.BaseTool
	KnowledgeBaseInfo string
	MaxIterations     int
}

func NewBaseBotAgent(cfg *AgentConfig, einoManager *eino.EinoManager) (BotAgent, error) {
	if einoManager == nil {
		return nil, fmt.Errorf("eino manager is nil")
	}

	chatModel := cfg.ChatModel
	if chatModel == nil {
		if !einoManager.HasChatModel() {
			logger.Error("ChatModel 未初始化，无法创建 Agent",
				logger.StringField("agentID", cfg.ID),
				logger.StringField("agentName", cfg.Name))
			return nil, fmt.Errorf("chat model not initialized - please configure API key in config.yaml or bot settings")
		}
		chatModel = einoManager.GetChatModel()
	}

	systemPrompt := cfg.SystemPrompt

	if len(cfg.Tools) > 0 {
		toolRules := "# 工具调用规则（最高优先级）\n" +
			"当你决定使用任何工具时，必须通过 function_call 机制直接调用，禁止仅用文字表达\"我来查一下\"\"让我搜索\"等意图而不实际调用。\n" +
			"你必须在同一轮回复中完成：调用工具 → 获取结果 → 基于结果回复用户。绝对不要在未实际调用工具的情况下告诉用户你将要使用工具。\n\n"
		systemPrompt = toolRules + systemPrompt
	}

	if cfg.KnowledgeBaseInfo != "" {
		systemPrompt = systemPrompt + cfg.KnowledgeBaseInfo
	}

	if len(cfg.Tools) > 0 {
		systemPrompt = systemPrompt + "\n\n【重要提醒】你必须使用可用工具来完成任务。如果需要查询信息、操作数据，立即调用对应的工具，不要只用文字回复而不调用工具。"
	}

	agentTools := cfg.Tools
	if agentTools == nil {
		agentTools = []tool.BaseTool{}
	}

	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 10
	}

	logger.Info("创建 Bot Agent",
		logger.StringField("id", cfg.ID),
		logger.StringField("name", cfg.Name),
		logger.IntField("tools_count", len(agentTools)),
		logger.IntField("max_iterations", maxIter),
		logger.BoolField("has_knowledge_base", cfg.KnowledgeBaseInfo != ""))

	agent, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{
		Name:        cfg.Name,
		Description: cfg.Description,
		Instruction: systemPrompt,
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: agentTools,
				UnknownToolsHandler: func(ctx context.Context, toolName, toolInput string) (string, error) {
					logger.Warn("LLM调用了未注册的工具",
						logger.StringField("tool", toolName),
						logger.StringField("input", toolInput))
					return fmt.Sprintf("工具 %s 暂时不可用，请使用其他可用工具完成任务。", toolName), nil
				},
			},
		},
		MaxIterations: maxIter,
	})
	if err != nil {
		logger.Error("创建 ChatModelAgent 失败", logger.ErrorField(err))
		return nil, err
	}

	runner := adk.NewRunner(context.Background(), adk.RunnerConfig{Agent: agent})
	streamRunner := adk.NewRunner(context.Background(), adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: true,
	})

	return &BaseBotAgent{
		id:           cfg.ID,
		name:         cfg.Name,
		description:  cfg.Description,
		systemPrompt: systemPrompt,
		run: func(ctx context.Context, messages []*schema.Message) agentEventIter {
			return runner.Run(ctx, messages)
		},
		stream: func(ctx context.Context, messages []*schema.Message) agentEventIter {
			return streamRunner.Run(ctx, messages)
		},
	}, nil
}

func (a *BaseBotAgent) Chat(ctx context.Context, message string) (string, error) {
	return a.chatWithMessages(ctx, a.buildMessages(message, nil))
}

func (a *BaseBotAgent) ChatStream(ctx context.Context, message string, onChunk func(string) error) error {
	return a.chatStreamWithMessages(ctx, a.buildMessages(message, nil), onChunk)
}

func (a *BaseBotAgent) ChatWithHistory(ctx context.Context, messages []*schema.Message) (string, error) {
	return a.chatWithMessages(ctx, a.buildMessages("", messages))
}

func (a *BaseBotAgent) ChatStreamWithHistory(ctx context.Context, messages []*schema.Message, onChunk func(string) error) error {
	return a.chatStreamWithMessages(ctx, a.buildMessages("", messages), onChunk)
}

func (a *BaseBotAgent) GetName() string         { return a.name }
func (a *BaseBotAgent) GetDescription() string  { return a.description }
func (a *BaseBotAgent) GetSystemPrompt() string { return a.systemPrompt }

func (a *BaseBotAgent) chatWithMessages(ctx context.Context, messages []*schema.Message) (string, error) {
	if a.run == nil {
		return "", fmt.Errorf("agent runner not initialized")
	}
	if !hasConversationTurn(messages) {
		return "", ErrEmptyRequest
	}

	logger.Info("Bot chat request",
		logger.StringField("bot", a.name),
		logger.IntField("messages_count", len(messages)))

	// 每一轮对话挂一份新的工具结果预算：ReAct 循环里模型可能并行调用多个工具，
	// 单条结果的硬上限挡不住「N 条加起来撑爆上下文」。轮次结束即失效，
	// 不会跨轮累积，也避免把长对话的旧额度算到新一轮。
	ctx, turn := toolbudget.WithTurn(ctx, toolbudget.DefaultTurnBudget)

	iter := a.run(ctx, messages)

	var (
		lastValidContent string
		firstErr         error
		eventCount       int
	)

	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		eventCount++

		if event == nil {
			continue
		}

		if event.Err != nil {
			// 模型报错不能只打日志：整轮都失败时若不返回 error，
			// 调用方会拿到 ("", nil) 并把这当成一条成功的空回答。
			if firstErr == nil {
				firstErr = event.Err
			}
			logger.Error("Agent事件错误",
				logger.IntField("event_index", eventCount),
				logger.ErrorField(event.Err))
			continue
		}

		if event.Action != nil && event.Action.Exit {
			break
		}

		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}

		if event.Output.MessageOutput.Role != schema.Assistant {
			closeMessageStream(event.Output.MessageOutput)
			continue
		}

		msg, _, err := adk.GetMessage(event)
		// GetMessage 对流式事件会 Copy(2) 后留一份未消费的副本给自己，这里用完即关，
		// 否则每轮 dialog 都会挂着一个没人读、也没人关的管道。
		if event.Output.MessageOutput.IsStreaming {
			closeMessageStream(event.Output.MessageOutput)
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			logger.Warn("获取事件消息失败",
				logger.IntField("event_index", eventCount),
				logger.ErrorField(err))
			continue
		}

		if msg == nil {
			continue
		}

		eino.RecordUsageFrom(ctx, msg)

		if len(msg.ToolCalls) > 0 {
			// 透传 Agent 的中间决策（调用了什么工具）给上层观察者
			notifyToolCalls(ctx, msg.ToolCalls)
			logger.Debug("跳过中间Assistant消息",
				logger.IntField("event_index", eventCount),
				logger.IntField("tool_calls", len(msg.ToolCalls)))
			continue
		}

		if msg.Content != "" {
			lastValidContent = msg.Content
		}
	}

	logger.Info("Bot chat response",
		logger.StringField("bot", a.name),
		logger.IntField("response_len", len(lastValidContent)),
		logger.IntField("events", eventCount),
		logger.IntField("tool_result_runes", turn.Used()))

	if lastValidContent == "" {
		return "", a.emptyResultError(ctx, firstErr)
	}

	return lastValidContent, nil
}

// emptyResultError 把「没有任何正文」的收尾统一成可区分的错误：
// 先看是否有事件级错误（那才是根因），再看是否调用方已取消，最后才是模型确实什么都没说。
func (a *BaseBotAgent) emptyResultError(ctx context.Context, firstErr error) error {
	if firstErr != nil {
		return fmt.Errorf("agent 运行失败: %w", firstErr)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("agent 运行被中止: %w", err)
	}
	return ErrEmptyResponse
}

func (a *BaseBotAgent) chatStreamWithMessages(ctx context.Context, messages []*schema.Message, onChunk func(string) error) error {
	if a.stream == nil {
		return fmt.Errorf("agent stream runner not initialized")
	}
	if !hasConversationTurn(messages) {
		return ErrEmptyRequest
	}

	logger.Info("Bot stream chat request",
		logger.StringField("bot", a.name),
		logger.IntField("messages_count", len(messages)))

	// 与 Chat 一致：一轮一份预算，流式只是把回答边收边推，工具结果同样进 prompt。
	ctx, turn := toolbudget.WithTurn(ctx, toolbudget.DefaultTurnBudget)

	iter := a.stream(ctx, messages)

	var (
		emitted    strings.Builder
		firstErr   error
		eventCount int
	)

	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		eventCount++

		if event == nil {
			continue
		}

		if event.Err != nil {
			if firstErr == nil {
				firstErr = event.Err
			}
			logger.Error("Agent流式事件错误", logger.ErrorField(event.Err))
			continue
		}

		if event.Action != nil && event.Action.Exit {
			break
		}

		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}

		mv := event.Output.MessageOutput

		if mv.Role != schema.Assistant {
			// 工具结果等非助手消息不推给用户，但它的流必须关掉：
			// 不消费也不关闭时，上游广播管道会一直挂着这个读者，事件源越跑越慢甚至阻塞。
			closeMessageStream(mv)
			continue
		}

		if mv.IsStreaming && mv.MessageStream != nil {
			chunks, recvErr, chunkErr := forwardMessageStream(ctx, mv.MessageStream, onChunk, &emitted)
			if chunkErr != nil {
				// onChunk 报错基本只有一种原因：客户端已断开，继续跑没有意义
				return chunkErr
			}
			if recvErr != nil {
				if firstErr == nil {
					firstErr = recvErr
				}
				logger.Error("读取流式chunk失败", logger.ErrorField(recvErr))
			}
			notifyToolCallsFromChunks(ctx, chunks)
			continue
		}

		msg := mv.Message
		if msg == nil {
			continue
		}

		eino.RecordUsageFrom(ctx, msg)

		if len(msg.ToolCalls) > 0 {
			// 透传 Agent 的中间决策（调用了什么工具）给上层观察者
			notifyToolCalls(ctx, msg.ToolCalls)
			continue
		}

		if msg.Content == "" {
			continue
		}
		emitted.WriteString(msg.Content)
		if err := onChunk(msg.Content); err != nil {
			return err
		}
	}

	logger.Info("Bot stream chat response",
		logger.StringField("bot", a.name),
		logger.IntField("response_len", emitted.Len()),
		logger.IntField("events", eventCount),
		logger.IntField("tool_result_runes", turn.Used()))

	if emitted.Len() == 0 {
		return a.emptyResultError(ctx, firstErr)
	}

	return nil
}

// forwardMessageStream 边收边推：这是「真流式」与「假流式」的分界线。
//
// 旧实现先调 adk.GetMessage（内部 ConcatMessageStream 会把整条流读完），
// 再回放拷贝出来的 chunk——首字延迟等于整段生成时间，用户盯着空白等十几秒，
// 最后一次性刷出全部文字。这里直接消费原始流，收到一段推一段。
//
// 代价：如果本轮最后被判定为工具调用，工具前的「我先查一下」这类过渡文本
// 已经推给用户、无法撤回。之所以接受：工具调用本身已经有 observer 通道
// 单独提示，过渡文本对用户是有效的过程信息，比「全程空白」体验更好。
func forwardMessageStream(
	ctx context.Context,
	stream adk.MessageStream,
	onChunk func(string) error,
	emitted *strings.Builder,
) (chunks []*schema.Message, recvErr error, chunkErr error) {
	defer stream.Close()

	buf := newChunkBuffer(onChunk)

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			// 尾部不足一个合并窗口的残余必须推出，否则回答最后一截会丢失。
			if err := buf.Flush(); err != nil {
				return chunks, nil, err
			}
			return chunks, nil, nil
		}
		if err != nil {
			// 出错时尽力把已收到的内容交付，避免部分成功只剩前半句。
			if flushErr := buf.Flush(); flushErr != nil {
				return chunks, err, flushErr
			}
			return chunks, err, nil
		}
		if chunk == nil {
			continue
		}

		chunks = append(chunks, chunk)
		eino.RecordUsageFrom(ctx, chunk)

		if chunk.Content == "" {
			continue
		}
		emitted.WriteString(chunk.Content)
		if err := buf.Add(chunk.Content); err != nil {
			return chunks, nil, err
		}
	}
}

// notifyToolCallsFromChunks 流式消息的 tool_calls 是分片下发的（带 index 的增量），
// 只有拼接后才知道完整的调用列表；不拼接就会把半截参数当工具调用推给上层。
func notifyToolCallsFromChunks(ctx context.Context, chunks []*schema.Message) {
	if len(chunks) == 0 {
		return
	}
	msg, err := schema.ConcatMessages(chunks)
	if err != nil || msg == nil || len(msg.ToolCalls) == 0 {
		return
	}
	notifyToolCalls(ctx, msg.ToolCalls)
}

// closeMessageStream 关闭一个不再消费的消息流（幂等，nil 安全）。
func closeMessageStream(mv *adk.MessageVariant) {
	if mv == nil || mv.MessageStream == nil {
		return
	}
	mv.MessageStream.Close()
}

func (a *BaseBotAgent) buildMessages(currentMessage string, history []*schema.Message) []*schema.Message {
	messages := make([]*schema.Message, 0, len(history)+2)

	// 历史先按预算裁剪，再拼 system prompt：被省略的条数要写进提示，
	// 否则模型会以为「用户只说过这些」，对早期内容给出确定的错误回答。
	trimmed, omitted := trimHistory(history, historyRuneBudget)
	systemPrompt := a.systemPrompt
	if omitted > 0 {
		systemPrompt += historyNotice(omitted)
	}

	if systemPrompt != "" {
		messages = append(messages, schema.SystemMessage(systemPrompt))
	}

	// 历史来自调用方组装，必须在这里做防御：
	//   - nil 元素会被 SDK 直接解引用 → panic，且发生在真正请求时，难以定位；
	//   - content 为空且没有 tool_calls 的消息会被供应商拒绝（400），
	//     但带 tool_calls 的空内容 assistant 消息是合法的，必须保留。
	for _, m := range trimmed {
		if m == nil {
			continue
		}
		if m.Content == "" && len(m.ToolCalls) == 0 {
			continue
		}
		messages = append(messages, m)
	}

	if currentMessage != "" {
		messages = append(messages, schema.UserMessage(currentMessage))
	}

	return messages
}

// hasConversationTurn 判断消息里是否至少有一条非 system 内容。
func hasConversationTurn(messages []*schema.Message) bool {
	for _, m := range messages {
		if m != nil && m.Role != schema.System {
			return true
		}
	}
	return false
}
