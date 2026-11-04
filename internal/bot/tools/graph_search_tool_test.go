package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestMain 指向仓库内的最小配置夹具：图谱工具会打日志，而 logger 初始化要读 config，
// 仓库里的 config/config.yaml 被 .gitignore 排除、CI 上不存在，否则直接 panic。
func TestMain(m *testing.M) {
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "..", "..", "..", "config", "testdata", "config.yaml")
	if abs, err := filepath.Abs(fixture); err == nil {
		fixture = abs
	}
	os.Setenv("LOGOS_CONFIG_PATH", fixture)
	os.Exit(m.Run())
}

type fakeGraphService struct {
	searchResult []*GraphEntity
	searchErr    error
	sub          *GraphSubgraph
	paths        []*GraphEntityPath
}

func (f *fakeGraphService) SearchEntities(context.Context, string, *string, string, int, int) ([]*GraphEntity, error) {
	return f.searchResult, f.searchErr
}

func (f *fakeGraphService) GetRelatedEntities(context.Context, string, string) ([]*GraphEntity, error) {
	return nil, nil
}

func (f *fakeGraphService) GetSubgraph(context.Context, string, int, string) (*GraphSubgraph, error) {
	return f.sub, nil
}

func (f *fakeGraphService) GetEntityPaths(context.Context, string, string, int, string) ([]*GraphEntityPath, error) {
	return f.paths, nil
}

func (f *fakeGraphService) GetGraphStats(context.Context, string) (*GraphStatsInfo, error) {
	return nil, nil
}

func invokeGraphTool(t *testing.T, svc GraphService, args string) (string, error) {
	t.Helper()
	tl, err := NewGraphSearchTool(svc, "col")
	if err != nil {
		t.Fatalf("NewGraphSearchTool: %v", err)
	}
	return tl.InvokableRun(context.Background(), args)
}

// TestGraphSearch_PropertyOrderStable 属性来自 map，输出必须排序且可复现。
func TestGraphSearch_PropertyOrderStable(t *testing.T) {
	svc := &fakeGraphService{searchResult: []*GraphEntity{{
		ID: "e1", Name: "实体", Type: "concept",
		Properties: map[string]string{"zeta": "1", "alpha": "2", "mid": "3"},
	}}}

	first, err := invokeGraphTool(t, svc, `{"keyword":"实体"}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	second, err := invokeGraphTool(t, svc, `{"keyword":"实体"}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if first != second {
		t.Fatal("两次调用输出不一致：属性遍历顺序未排序")
	}

	iAlpha := strings.Index(first, "alpha: 2")
	iMid := strings.Index(first, "mid: 3")
	iZeta := strings.Index(first, "zeta: 1")
	if iAlpha < 0 || iMid < 0 || iZeta < 0 || !(iAlpha < iMid && iMid < iZeta) {
		t.Fatalf("属性未按字典序输出: %q", first)
	}
}

// TestGraphSearch_OutputBudget 30 个长描述实体不能让输出无限膨胀。
func TestGraphSearch_OutputBudget(t *testing.T) {
	entities := make([]*GraphEntity, 0, 30)
	for i := 0; i < 30; i++ {
		entities = append(entities, &GraphEntity{
			ID:          "e" + string(rune('0'+i%10)),
			Name:        strings.Repeat("名称", 5),
			Type:        "concept",
			Description: strings.Repeat("很长的描述", 400), // 远超单条预算
		})
	}

	out, err := invokeGraphTool(t, &fakeGraphService{searchResult: entities}, `{"keyword":"x","top_k":30}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if n := utf8.RuneCountInString(out); n > graphOutputRuneBudget {
		t.Fatalf("输出 %d 字符，超过预算 %d", n, graphOutputRuneBudget)
	}
	if !strings.Contains(out, "结果已截断") {
		t.Fatalf("截断后必须显式告知模型，got %q", out)
	}
	if !utf8.ValidString(out) {
		t.Fatal("输出包含非法 UTF-8：截断切在字符中间")
	}
}

// TestGraphSearch_ErrorPropagates 服务故障必须返回 error，
// 不能变成「未找到」之类的文本让模型继续换词重试。
func TestGraphSearch_ErrorPropagates(t *testing.T) {
	_, err := invokeGraphTool(t, &fakeGraphService{searchErr: errors.New("connection refused")}, `{"keyword":"x"}`)
	if err == nil {
		t.Fatal("服务错误应返回 error")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("错误原因丢失: %v", err)
	}
}

// TestGraphExplore_UsesActualCounts 展示条数与实际切片一致，避免模型误判还有内容没看到。
func TestGraphExplore_UsesActualCounts(t *testing.T) {
	sub := &GraphSubgraph{
		Nodes:     []*GraphEntity{{ID: "a", Name: "A", Type: "t"}, {ID: "b", Name: "B", Type: "t"}},
		Edges:     []*GraphRelation{{SourceID: "a", TargetID: "b", Type: "rel"}},
		NodeCount: 99,
		EdgeCount: 42,
	}

	svc := &fakeGraphService{sub: sub}
	tl, err := NewGraphExploreTool(svc, "col")
	if err != nil {
		t.Fatalf("NewGraphExploreTool: %v", err)
	}
	out, err := tl.InvokableRun(context.Background(), `{"entity_id":"a"}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !strings.Contains(out, "节点数: 2, 关系数: 1") {
		t.Fatalf("应使用实际切片长度，got %q", out)
	}
}

// TestGraphPaths_Capped 路径条数由服务端决定，展示必须设上限。
func TestGraphPaths_Capped(t *testing.T) {
	paths := make([]*GraphEntityPath, 0, 15)
	for i := 0; i < 15; i++ {
		paths = append(paths, &GraphEntityPath{
			Entities: []*GraphEntity{{Name: "A", Type: "t"}, {Name: "B", Type: "t"}},
			Edges:    []*GraphRelation{{Type: "rel"}},
			Length:   1,
		})
	}

	tl, err := NewGraphPathTool(&fakeGraphService{paths: paths}, "col")
	if err != nil {
		t.Fatalf("NewGraphPathTool: %v", err)
	}
	out, err := tl.InvokableRun(context.Background(), `{"source_id":"a","target_id":"b"}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if strings.Count(out, "--- 路径") != graphPathDisplayLimit {
		t.Fatalf("应只展示 %d 条路径", graphPathDisplayLimit)
	}
	if !strings.Contains(out, "仅展示前 10 / 15 条路径") {
		t.Fatalf("缺少截断提示: %q", out)
	}
}

// TestGraphSearch_PropertyCountCapped 属性数量同样由外部数据决定，需要单独上限。
func TestGraphSearch_PropertyCountCapped(t *testing.T) {
	props := make(map[string]string, 20)
	for i := 0; i < 20; i++ {
		props["k"+strings.Repeat("0", 1)+string(rune('a'+i))] = "v"
	}

	out, err := invokeGraphTool(t, &fakeGraphService{searchResult: []*GraphEntity{{
		ID: "e", Name: "n", Type: "t", Properties: props,
	}}}, `{"keyword":"x"}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !strings.Contains(out, "其余 12 个属性已省略") {
		t.Fatalf("属性未按上限省略: %q", out)
	}
}
