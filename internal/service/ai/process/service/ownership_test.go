package service

import (
	"context"
	"errors"
	"testing"

	"Logos/internal/service/ai/process/model"
)

// ownerScopedRepo 记录 service 实际传给 DAO 的 userID，
// 用来验证「空身份不能退化成查全表」。
type ownerScopedRepo struct {
	fakeProcessRepo
	lastListUserID  string
	lastOwnerUserID string
	lastDeleteUser  string
	chunksRequested bool
}

func (r *ownerScopedRepo) ListDocuments(ctx context.Context, userID string, status *int, page, pageSize int) ([]*model.Document, int64, error) {
	r.lastListUserID = userID
	return nil, 0, nil
}

func (r *ownerScopedRepo) GetDocumentWithOwnerCheck(ctx context.Context, id, userID string) (*model.Document, error) {
	r.lastOwnerUserID = userID
	if r.doc == nil || r.doc.UserID != userID {
		return nil, nil
	}
	return r.doc, nil
}

func (r *ownerScopedRepo) DeleteDocument(ctx context.Context, id, userID string) error {
	r.lastDeleteUser = userID
	return nil
}

func (r *ownerScopedRepo) GetChunksByDocumentID(ctx context.Context, docID string) ([]*model.DocumentChunk, error) {
	r.chunksRequested = true
	return nil, nil
}

// TestListDocuments_RequiresIdentity 没有身份时必须在 service 层拒绝，
// 不能把空 userID 传给 DAO——DAO 会把它解释成「不按用户过滤」。
func TestListDocuments_RequiresIdentity(t *testing.T) {
	repo := &ownerScopedRepo{}
	svc := &ProcessService{repo: repo}

	if _, _, err := svc.ListDocuments(context.Background(), nil, 1, 20); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("空身份应返回 ErrUnauthenticated，实际 %v", err)
	}
	if repo.lastListUserID != "" {
		t.Fatalf("未认证请求不应触达 DAO，实际 userID=%q", repo.lastListUserID)
	}
}

func TestListDocuments_PassesCallerIdentity(t *testing.T) {
	repo := &ownerScopedRepo{}
	svc := &ProcessService{repo: repo}

	if _, _, err := svc.ListDocuments(userCtx("user-1"), nil, 1, 20); err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if repo.lastListUserID != "user-1" {
		t.Fatalf("DAO 收到 userID=%q，期望 user-1", repo.lastListUserID)
	}
}

func TestDeleteDocument_RequiresIdentity(t *testing.T) {
	repo := &ownerScopedRepo{}
	svc := &ProcessService{repo: repo}

	if err := svc.DeleteDocument(context.Background(), "doc-1"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("空身份应返回 ErrUnauthenticated，实际 %v", err)
	}
	if repo.lastDeleteUser != "" {
		t.Fatalf("未认证删除不应触达 DAO，实际 userID=%q", repo.lastDeleteUser)
	}
}

// TestGetChunks_ChecksOwnership 只按 document_id 查 chunks 会泄漏内容：
// 必须先确认文档属于当前用户，再读子表。
func TestGetChunks_ChecksOwnership(t *testing.T) {
	repo := &ownerScopedRepo{}
	repo.doc = &model.Document{ID: "doc-1", UserID: "user-1"}
	svc := &ProcessService{repo: repo}

	if _, err := svc.GetChunks(userCtx("user-2"), "doc-1"); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("非 owner 读取 chunks 应返回 ErrDocumentNotFound，实际 %v", err)
	}
	if repo.lastOwnerUserID != "user-2" {
		t.Fatalf("归属校验应使用调用方身份，实际 %q", repo.lastOwnerUserID)
	}
	if repo.chunksRequested {
		t.Fatal("归属校验失败后不应继续读取 chunks")
	}
}

func TestGetChunks_OwnerCanRead(t *testing.T) {
	repo := &ownerScopedRepo{}
	repo.doc = &model.Document{ID: "doc-1", UserID: "user-1"}
	svc := &ProcessService{repo: repo}

	if _, err := svc.GetChunks(userCtx("user-1"), "doc-1"); err != nil {
		t.Fatalf("owner 读取 chunks 失败: %v", err)
	}
	if !repo.chunksRequested {
		t.Fatal("归属校验通过后应读取 chunks")
	}
}
