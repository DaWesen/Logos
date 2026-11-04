package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Logos/internal/service/ai/process/dao"
	"Logos/internal/service/ai/process/model"
	"Logos/pkg/auth"

	"google.golang.org/grpc/metadata"
)

type fakeVector struct {
	id string
}

func (v *fakeVector) GetID() string { return v.id }

// fakeVectorService 可配置失败/阻塞/并发观测，用来验证 processVectorization 的边界。
type fakeVectorService struct {
	failContains  string
	blockUntilCtx bool
	delay         time.Duration

	inFlight    atomic.Int64
	maxInFlight atomic.Int64
	calls       atomic.Int64
	ctxErrs     atomic.Int64
}

func (s *fakeVectorService) Vectorize(ctx context.Context, text, collectionID string, metadata map[string]string) (VectorModel, error) {
	s.calls.Add(1)
	cur := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		old := s.maxInFlight.Load()
		if cur <= old || s.maxInFlight.CompareAndSwap(old, cur) {
			break
		}
	}

	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			s.ctxErrs.Add(1)
			return nil, ctx.Err()
		}
	}

	if s.blockUntilCtx {
		<-ctx.Done()
		s.ctxErrs.Add(1)
		return nil, ctx.Err()
	}
	if s.failContains != "" && strings.Contains(text, s.failContains) {
		return nil, errors.New("upstream vectorize failed")
	}
	return &fakeVector{id: "vec-" + metadata["chunk_id"]}, nil
}

func (s *fakeVectorService) BatchVectorize(ctx context.Context, texts []string, collectionID string, metadataList []map[string]string) ([]VectorModel, error) {
	return nil, errors.New("not implemented")
}

func (s *fakeVectorService) GetCollectionInfo(ctx context.Context, id string) (*CollectionInfo, error) {
	return &CollectionInfo{ID: id}, nil
}

// fakeProcessRepo 只实现测试需要的部分，其余方法返回零值。
type fakeProcessRepo struct {
	mu      sync.Mutex
	chunks  map[string]*model.DocumentChunk
	updated []*model.DocumentChunk
	doc     *model.Document

	reapCalls  atomic.Int64
	reapCutoff time.Time
	reapLimit  int
	reapReason string
	reapCount  int64
	reapErr    error
}

func newFakeProcessRepo() *fakeProcessRepo {
	return &fakeProcessRepo{chunks: map[string]*model.DocumentChunk{}}
}

func (r *fakeProcessRepo) CreateDocument(ctx context.Context, doc *model.Document, userID string) error {
	r.doc = doc
	return nil
}
func (r *fakeProcessRepo) UpdateDocument(ctx context.Context, doc *model.Document) error {
	r.doc = doc
	return nil
}
func (r *fakeProcessRepo) GetDocument(ctx context.Context, id string) (*model.Document, error) {
	return r.doc, nil
}
func (r *fakeProcessRepo) GetDocumentWithOwnerCheck(ctx context.Context, id, userID string) (*model.Document, error) {
	return r.doc, nil
}
func (r *fakeProcessRepo) GetDocumentByHash(ctx context.Context, hash string) (*model.Document, error) {
	return nil, nil
}
func (r *fakeProcessRepo) ListDocuments(ctx context.Context, userID string, status *int, page, pageSize int) ([]*model.Document, int64, error) {
	return nil, 0, nil
}
func (r *fakeProcessRepo) DeleteDocument(ctx context.Context, id, userID string) error { return nil }
func (r *fakeProcessRepo) CreateChunk(ctx context.Context, chunk *model.DocumentChunk) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chunks[chunk.ID] = chunk
	return nil
}
func (r *fakeProcessRepo) UpdateChunk(ctx context.Context, chunk *model.DocumentChunk) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updated = append(r.updated, chunk)
	return nil
}
func (r *fakeProcessRepo) GetChunksByDocumentID(ctx context.Context, docID string) ([]*model.DocumentChunk, error) {
	return nil, nil
}
func (r *fakeProcessRepo) DeleteChunksByDocumentID(ctx context.Context, docID string) error {
	return nil
}
func (r *fakeProcessRepo) GetChunkCountByDocumentID(ctx context.Context, docID string) (int64, error) {
	return 0, nil
}
func (r *fakeProcessRepo) GetChunkCountsByDocumentIDs(ctx context.Context, docIDs []string) (map[string]int64, error) {
	return nil, nil
}
func (r *fakeProcessRepo) FailStaleDocuments(ctx context.Context, cutoff time.Time, limit int, reason string) (int64, error) {
	r.reapCalls.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reapCutoff = cutoff
	r.reapLimit = limit
	r.reapReason = reason
	return r.reapCount, r.reapErr
}

func (r *fakeProcessRepo) reapSnapshot() (time.Time, int, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reapCutoff, r.reapLimit, r.reapReason
}

func (r *fakeProcessRepo) WithTransaction(ctx context.Context, fn func(dao.ProcessRepository) error) error {
	return fn(r)
}

func newTestChunks(n int) []*model.DocumentChunk {
	chunks := make([]*model.DocumentChunk, n)
	for i := range chunks {
		chunks[i] = &model.DocumentChunk{
			ID:         "chunk-" + strings.Repeat("x", i+1),
			DocumentID: "doc-1",
			Content:    "content",
			ChunkType:  model.ChunkTypeText,
		}
	}
	return chunks
}

func newVectorizationService(repo dao.ProcessRepository, vs VectorService) *ProcessService {
	return &ProcessService{repo: repo, vectorService: vs}
}

func testDoc() *model.Document {
	return &model.Document{ID: "doc-1", VectorCollectionID: "col-1"}
}

func userCtx(userID string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		auth.UserIDKey, userID,
		auth.RoleKey, "user",
	))
}

// TestProcessVectorization_AllFailed 全部失败必须返回错误，
// 否则调用方会把「没有向量的文档」标成 Completed。
func TestProcessVectorization_AllFailed(t *testing.T) {
	vs := &fakeVectorService{failContains: "content"}
	svc := newVectorizationService(newFakeProcessRepo(), vs)

	stats, err := svc.processVectorization(context.Background(), testDoc(), newTestChunks(3))
	if err == nil {
		t.Fatal("全部失败应返回错误")
	}
	if !errors.Is(err, ErrVectorizationAllFailed) {
		t.Fatalf("错误应可识别为 ErrVectorizationAllFailed，实际 %v", err)
	}
	if stats.Succeeded != 0 || stats.Failed != 3 || stats.Total != 3 {
		t.Fatalf("stats = %+v，期望 succeeded=0 failed=3 total=3", stats)
	}
}

// TestProcessVectorization_PartialFailure 部分失败不致命，但统计必须准确，
// 调用方才能把「部分可检索」写进文档状态。
func TestProcessVectorization_PartialFailure(t *testing.T) {
	vs := &fakeVectorService{failContains: "please fail"}
	repo := newFakeProcessRepo()
	svc := newVectorizationService(repo, vs)

	chunks := newTestChunks(4)
	chunks[1].Content = "please fail me"
	stats, err := svc.processVectorization(context.Background(), testDoc(), chunks)
	if err != nil {
		t.Fatalf("部分失败不应返回致命错误: %v", err)
	}
	if stats.Succeeded != 3 || stats.Failed != 1 || stats.Total != 4 {
		t.Fatalf("stats = %+v，期望 succeeded=3 failed=1 total=4", stats)
	}
	if len(repo.updated) != 3 {
		t.Fatalf("成功 chunk 应写回向量 ID，实际更新 %d 条", len(repo.updated))
	}
	for _, chunk := range repo.updated {
		if chunk.VectorID == nil || *chunk.VectorID == "" {
			t.Fatalf("chunk %s 未写回向量 ID", chunk.ID)
		}
	}
}

// TestProcessVectorization_InheritsContext 每个 chunk 必须继承外层 ctx；
// 如果退回 context.Background()，20ms 超时的测试会一直等到 60s chunk 预算。
func TestProcessVectorization_InheritsContext(t *testing.T) {
	vs := &fakeVectorService{blockUntilCtx: true}
	svc := newVectorizationService(newFakeProcessRepo(), vs)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := svc.processVectorization(ctx, testDoc(), newTestChunks(12))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("ctx 超时应返回错误")
	}
	if elapsed > time.Second {
		t.Fatalf("耗时 %v，说明 chunk 没有继承外层 ctx", elapsed)
	}
	if vs.ctxErrs.Load() == 0 {
		t.Fatal("至少应有 chunk 观察到 ctx 取消")
	}
}

// TestProcessVectorization_BoundedConcurrency 并发度必须固定：
// 既不能串行，也不能无上限打满向量服务。
func TestProcessVectorization_BoundedConcurrency(t *testing.T) {
	vs := &fakeVectorService{delay: 10 * time.Millisecond}
	svc := newVectorizationService(newFakeProcessRepo(), vs)

	if _, err := svc.processVectorization(context.Background(), testDoc(), newTestChunks(30)); err != nil {
		t.Fatalf("向量化失败: %v", err)
	}

	max := vs.maxInFlight.Load()
	if max > vectorizationConcurrency {
		t.Fatalf("最大并发 = %d，超过上限 %d", max, vectorizationConcurrency)
	}
	if max < 2 {
		t.Fatalf("最大并发 = %d，实际是串行执行", max)
	}
}

// TestProcessURL_RejectsForbiddenURLBeforeCreate 非法 URL 必须在创建文档前拒绝，
// 否则库里会留下一条注定失败的 pending 记录。
func TestProcessURL_RejectsForbiddenURLBeforeCreate(t *testing.T) {
	repo := newFakeProcessRepo()
	svc := newVectorizationService(repo, nil)

	if _, err := svc.ProcessURL(userCtx("user-1"), "http://169.254.169.254/latest/meta-data", "col-1"); err == nil {
		t.Fatal("元数据地址应被拒绝")
	}
	if repo.doc != nil {
		t.Fatal("非法 URL 不应创建文档记录")
	}

	if _, err := svc.ProcessURL(userCtx("user-1"), "http://127.0.0.1/admin", "col-1"); err == nil {
		t.Fatal("回环地址应被拒绝")
	}
	if repo.doc != nil {
		t.Fatal("非法 URL 不应创建文档记录")
	}
}
