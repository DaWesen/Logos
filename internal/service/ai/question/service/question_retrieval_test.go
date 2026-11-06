package service

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type blockingKnowledge struct {
	entered chan<- string
	release <-chan struct{}
}

func (b blockingKnowledge) SearchEntities(ctx context.Context, _ string) ([]string, error) {
	b.entered <- "knowledge_graph"
	select {
	case <-b.release:
		return []string{"knowledge evidence"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type blockingSearch struct {
	entered chan<- string
	release <-chan struct{}
}

func (b blockingSearch) Search(ctx context.Context, _, _ string) ([]string, error) {
	b.entered <- "search"
	select {
	case <-b.release:
		return []string{"search evidence"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type blockingVector struct {
	entered chan<- string
	release <-chan struct{}
}

func (b blockingVector) SearchSimilar(ctx context.Context, _ string, _ int) ([]string, error) {
	b.entered <- "vector"
	select {
	case <-b.release:
		return []string{"vector evidence"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// 三个检索源必须同时在跑：如果还是串行实现，第二个来源永远等不到
// 第一个释放，下面收集三个 entered 就会超时失败。
func TestAskQuestion_RetrievalsRunInParallel(t *testing.T) {
	entered := make(chan string, 3)
	release := make(chan struct{})
	svc := &qaServiceImpl{
		qaRepo:           &stubQARepo{},
		knowledgeService: blockingKnowledge{entered: entered, release: release},
		searchService:    blockingSearch{entered: entered, release: release},
		vectorService:    blockingVector{entered: entered, release: release},
		chat:             stubChat{answer: "answer"},
	}

	type result struct {
		answer  string
		sources []string
		err     error
	}
	done := make(chan result, 1)
	go func() {
		answer, _, sources, _, _, _, err := svc.AskQuestion(context.Background(), "q", 7, nil, "")
		done <- result{answer: answer, sources: sources, err: err}
	}()

	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		select {
		case source := <-entered:
			seen[source] = true
		case <-time.After(2 * time.Second):
			close(release)
			t.Fatalf("检索没有并行执行，已进入的来源 = %v", seen)
		}
	}
	close(release)

	out := <-done
	if out.err != nil {
		t.Fatalf("并行检索后问答不应失败: %v", out.err)
	}
	if out.answer != "answer" {
		t.Fatalf("answer = %q, want answer", out.answer)
	}
	if len(out.sources) != 3 {
		t.Fatalf("三个来源都应有证据, sources = %v", out.sources)
	}
}

// 单个检索超时只丢这一条证据，回答仍应成功，且 sources 不能谎报。
func TestAskQuestion_RetrievalTimeoutDegradesToPartialContext(t *testing.T) {
	svc := &qaServiceImpl{
		qaRepo:           &stubQARepo{},
		knowledgeService: staticKnowledge{items: []string{"kg evidence"}},
		searchService:    slowSearch{delay: time.Minute},
		vectorService:    staticVector{items: []string{"vector evidence"}},
		chat:             stubChat{answer: "grounded answer"},
		retrievalTimeout: 50 * time.Millisecond,
	}

	started := time.Now()
	answer, _, sources, _, _, degraded, err := svc.AskQuestion(context.Background(), "q", 7, nil, "")
	elapsed := time.Since(started)

	if err != nil {
		t.Fatalf("单源超时不应让整次问答失败: %v", err)
	}
	if answer != "grounded answer" || degraded {
		t.Fatalf("应返回正常回答, got %q degraded=%v", answer, degraded)
	}
	if contains(sources, "search") {
		t.Fatalf("超时来源不应出现在 sources 里, sources = %v", sources)
	}
	if !contains(sources, "knowledge_graph") || !contains(sources, "vector") {
		t.Fatalf("可用来源应保留, sources = %v", sources)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("单源超时没有生效，耗时 %v", elapsed)
	}
}

type staticKnowledge struct{ items []string }

func (s staticKnowledge) SearchEntities(context.Context, string) ([]string, error) {
	return s.items, nil
}

type staticVector struct{ items []string }

func (s staticVector) SearchSimilar(context.Context, string, int) ([]string, error) {
	return s.items, nil
}

type slowSearch struct{ delay time.Duration }

func (s slowSearch) Search(ctx context.Context, _, _ string) ([]string, error) {
	select {
	case <-time.After(s.delay):
		return []string{"late evidence"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// 即使某个 SDK 完全不看 ctx，等待方也只能等到整体预算耗尽，
// 不能被一个坏依赖永久拖住。
func TestAskQuestion_BadDependencyCannotBlockBeyondBudget(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	svc := &qaServiceImpl{
		qaRepo:           &stubQARepo{},
		knowledgeService: ignoringKnowledge{release: release},
		chat:             stubChat{answer: "answer without evidence"},
		answerTimeout:    120 * time.Millisecond,
		retrievalTimeout: 40 * time.Millisecond,
	}

	started := time.Now()
	answer, _, sources, _, _, degraded, err := svc.AskQuestion(context.Background(), "q", 7, nil, "")
	elapsed := time.Since(started)

	if err != nil {
		t.Fatalf("坏依赖不应让问答失败: %v", err)
	}
	if answer != "answer without evidence" || degraded {
		t.Fatalf("没有证据也应返回模型回答, got %q degraded=%v", answer, degraded)
	}
	if len(sources) != 0 {
		t.Fatalf("忽略 ctx 的依赖不应被算作有效来源, sources = %v", sources)
	}
	if elapsed > time.Second {
		t.Fatalf("等待方被坏依赖拖住了，耗时 %v", elapsed)
	}
}

type ignoringKnowledge struct{ release <-chan struct{} }

func (i ignoringKnowledge) SearchEntities(context.Context, string) ([]string, error) {
	<-i.release
	return []string{"too late"}, nil
}

type blockingChat struct{}

func (blockingChat) HasChatModel() bool { return true }

func (blockingChat) Chat(ctx context.Context, _ []string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

// 模型挂住时必须在预算内返回降级答案，并且把降级状态落库，
// 而不是把调用方一直吊着。
func TestAskQuestion_ModelTimeoutReturnsDegraded(t *testing.T) {
	repo := &stubQARepo{}
	svc := &qaServiceImpl{
		qaRepo:        repo,
		chat:          blockingChat{},
		answerTimeout: 60 * time.Millisecond,
	}

	started := time.Now()
	answer, confidence, _, _, _, degraded, err := svc.AskQuestion(context.Background(), "q", 7, nil, "")
	elapsed := time.Since(started)

	if err != nil {
		t.Fatalf("模型超时应降级而不是报错: %v", err)
	}
	if !degraded || confidence != 0.5 {
		t.Fatalf("超时应标记降级, degraded=%v confidence=%v", degraded, confidence)
	}
	if !strings.Contains(answer, "try again later") {
		t.Fatalf("应返回兜底文案, got %q", answer)
	}
	if elapsed > time.Second {
		t.Fatalf("模型超时预算没有生效，耗时 %v", elapsed)
	}
	if len(repo.created) != 1 || !repo.created[0].Degraded {
		t.Fatalf("超时答案应带降级标记落库: %+v", repo.created)
	}
}

func TestAssembleContext_DedupTruncateAndBudget(t *testing.T) {
	long := strings.Repeat("长", contextItemMaxRunes+500)
	items := []contextItem{
		{source: "knowledge_graph", text: "alpha"},
		{source: "vector", text: "  alpha  "},
		{source: "vector", text: "beta\n\nwith   spaces"},
		{source: "search", text: long},
		{source: "knowledge_graph", text: ""},
		{source: "search", text: "gamma"},
	}

	kept, sources, stats := assembleContext(items)
	if stats.duplicated != 1 {
		t.Fatalf("重复条目统计 = %d, want 1", stats.duplicated)
	}
	if stats.dropped != 1 {
		t.Fatalf("空条目应被丢弃, dropped = %d", stats.dropped)
	}
	if kept[0] != "alpha" || kept[1] != "beta with spaces" {
		t.Fatalf("应折叠空白并保序, kept = %v", kept[:2])
	}
	if utf8.RuneCountInString(kept[2]) > contextItemMaxRunes+1 {
		t.Fatalf("单条超长信息未截断, len = %d", utf8.RuneCountInString(kept[2]))
	}
	if !strings.HasSuffix(kept[2], "…") {
		t.Fatal("截断后应带省略标记")
	}
	total := 0
	for _, item := range kept {
		total += utf8.RuneCountInString(item)
	}
	if total > contextBudgetRunes {
		t.Fatalf("参考信息总量 %d 超出预算 %d", total, contextBudgetRunes)
	}
	if strings.Join(sources, ",") != "knowledge_graph,vector,search" {
		t.Fatalf("来源应按首次命中保序去重, sources = %v", sources)
	}
}

// 检索命中不等于进入 prompt：如果某个来源的条目全部被去重丢掉，
// 就不能把它报成有效来源。
func TestAssembleContext_DoesNotReportSourceWhoseItemsWereDropped(t *testing.T) {
	items := []contextItem{
		{source: "knowledge_graph", text: "the same evidence"},
		{source: "search", text: "  the   same evidence "},
		{source: "vector", text: ""},
	}

	kept, sources, stats := assembleContext(items)
	if len(kept) != 1 {
		t.Fatalf("只应保留一条证据, kept = %v", kept)
	}
	if stats.duplicated != 1 || stats.dropped != 1 {
		t.Fatalf("统计异常: %+v", stats)
	}
	if len(sources) != 1 || sources[0] != "knowledge_graph" {
		t.Fatalf("只有真正进入 prompt 的来源才算数, sources = %v", sources)
	}
}

func TestBuildPrompt_NoEvidenceInstructsNoGuessing(t *testing.T) {
	svc := &qaServiceImpl{}
	messages := svc.buildPrompt("who are you", nil)
	prompt := messages[len(messages)-1]
	if !strings.Contains(prompt, "No reference information") {
		t.Fatalf("无证据时应显式约束模型, prompt = %q", prompt)
	}
	if !strings.Contains(prompt, "who are you") {
		t.Fatalf("prompt 应包含用户问题, got %q", prompt)
	}
}

func TestPersistContextSurvivesParentCancellation(t *testing.T) {
	svc := &qaServiceImpl{persistTimeout: 50 * time.Millisecond}
	parent, cancel := context.WithCancel(context.Background())
	cancel()

	persistCtx, persistCancel := svc.persistContext(parent)
	defer persistCancel()

	if err := persistCtx.Err(); err != nil {
		t.Fatalf("落库上下文不应继承父级取消: %v", err)
	}
	deadline, ok := persistCtx.Deadline()
	if !ok {
		t.Fatal("落库上下文仍需有独立超时，避免写库永久挂起")
	}
	if time.Until(deadline) > time.Second {
		t.Fatalf("落库预算异常: %v", time.Until(deadline))
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
