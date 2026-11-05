package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"Logos/internal/service/ai/process/model"
)

// dtoRepo 记录块数查询是否被调用，并可分别注入单条/批量统计失败。
type dtoRepo struct {
	fakeProcessRepo
	docs       []*model.Document
	total      int64
	chunkCount int64
	countErr   error
	counts     map[string]int64
	countsErr  error
}

func (r *dtoRepo) GetDocumentWithOwnerCheck(ctx context.Context, id, userID string) (*model.Document, error) {
	for _, doc := range r.docs {
		if doc.ID == id {
			return doc, nil
		}
	}
	return nil, nil
}

func (r *dtoRepo) ListDocuments(ctx context.Context, userID string, status *int, page, pageSize int) ([]*model.Document, int64, error) {
	return r.docs, r.total, nil
}

func (r *dtoRepo) GetChunkCountByDocumentID(ctx context.Context, docID string) (int64, error) {
	return r.chunkCount, r.countErr
}

func (r *dtoRepo) GetChunkCountsByDocumentIDs(ctx context.Context, docIDs []string) (map[string]int64, error) {
	if r.countsErr != nil {
		return nil, r.countsErr
	}
	if r.counts == nil {
		return map[string]int64{}, nil
	}
	return r.counts, nil
}

func dtoTestDoc(id string) *model.Document {
	return &model.Document{ID: id, UserID: "user-1", FileName: id + ".md"}
}

func TestGetDocumentDTO_ChunkCountFailureReturnsError(t *testing.T) {
	repo := &dtoRepo{
		docs:     []*model.Document{dtoTestDoc("doc-1")},
		countErr: errors.New("count query timeout"),
	}
	svc := &ProcessService{repo: repo}

	dto, err := svc.GetDocumentDTO(userCtx("user-1"), "doc-1")
	if err == nil {
		t.Fatal("块数查询失败时不应返回 chunk_count=0 的假成功")
	}
	if !strings.Contains(err.Error(), "查询文档块数量失败") {
		t.Fatalf("错误应带查询阶段，实际 %v", err)
	}
	if dto != nil {
		t.Fatalf("失败时不应返回半成品 DTO，实际 %+v", dto)
	}
}

func TestListDocumentDTOs_ChunkCountFailureReturnsError(t *testing.T) {
	repo := &dtoRepo{
		docs:      []*model.Document{dtoTestDoc("doc-1"), dtoTestDoc("doc-2")},
		total:     2,
		countsErr: errors.New("count query timeout"),
	}
	svc := &ProcessService{repo: repo}

	dtos, _, err := svc.ListDocumentDTOs(userCtx("user-1"), nil, 1, 20)
	if err == nil {
		t.Fatal("批量块数查询失败时不应把整页 chunk_count 都返回 0")
	}
	if !strings.Contains(err.Error(), "查询文档块数量失败") {
		t.Fatalf("错误应带查询阶段，实际 %v", err)
	}
	if dtos != nil {
		t.Fatalf("失败时不应返回半成品列表，实际 %+v", dtos)
	}
}

func TestGetDocumentDTO_ReturnsChunkCount(t *testing.T) {
	repo := &dtoRepo{
		docs:       []*model.Document{dtoTestDoc("doc-1")},
		chunkCount: 7,
	}
	svc := &ProcessService{repo: repo}

	dto, err := svc.GetDocumentDTO(userCtx("user-1"), "doc-1")
	if err != nil {
		t.Fatalf("GetDocumentDTO: %v", err)
	}
	if dto == nil || dto.ChunkCount != 7 {
		t.Fatalf("应返回真实块数 7，实际 %+v", dto)
	}
}

func TestListDocumentDTOs_MapsCountsByDocument(t *testing.T) {
	repo := &dtoRepo{
		docs:   []*model.Document{dtoTestDoc("doc-1"), dtoTestDoc("doc-2")},
		total:  2,
		counts: map[string]int64{"doc-2": 4},
	}
	svc := &ProcessService{repo: repo}

	dtos, total, err := svc.ListDocumentDTOs(userCtx("user-1"), nil, 1, 20)
	if err != nil {
		t.Fatalf("ListDocumentDTOs: %v", err)
	}
	if total != 2 || len(dtos) != 2 {
		t.Fatalf("total/len = %d/%d，期望 2/2", total, len(dtos))
	}
	if dtos[0].ChunkCount != 0 || dtos[1].ChunkCount != 4 {
		t.Fatalf("块数映射错误：doc-1=%d doc-2=%d", dtos[0].ChunkCount, dtos[1].ChunkCount)
	}
}

func TestGetDocumentDTO_MissingDocumentReturnsNil(t *testing.T) {
	svc := &ProcessService{repo: &dtoRepo{}}

	dto, err := svc.GetDocumentDTO(userCtx("user-1"), "missing")
	if err != nil {
		t.Fatalf("文档不存在不是查询错误: %v", err)
	}
	if dto != nil {
		t.Fatalf("不存在时应返回 nil，实际 %+v", dto)
	}
}

func TestReprocessDocument_MissingReturnsDocumentNotFound(t *testing.T) {
	svc := &ProcessService{repo: &dtoRepo{}}

	err := svc.ReprocessDocument(userCtx("user-1"), "missing")
	if !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("不存在文档应返回 ErrDocumentNotFound，实际 %v", err)
	}
}
