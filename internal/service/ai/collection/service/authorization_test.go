package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"Logos/internal/service/ai/collection/dao"
	"Logos/internal/service/ai/collection/model"
	"Logos/pkg/auth"

	"google.golang.org/grpc/metadata"
)

// authzRepo 只实现授权路径用到的方法，其余通过嵌入接口保持可编译。
// 未实现的方法一旦被调用会 nil panic，正好能暴露越权路径。
type authzRepo struct {
	dao.CollectionRepository

	ds     *model.DataSource
	task   *model.CollectionTask
	result *model.CollectionResult

	listDSUserID    string
	listTaskUserID  string
	ownerDSUserID   string
	ownerTaskUserID string

	updatedDS     bool
	deletedDS     bool
	createdTask   bool
	updatedTask   bool
	deletedTask   bool
	listedResults bool
}

func (r *authzRepo) GetDataSourceWithOwnerCheck(ctx context.Context, id, userID string) (*model.DataSource, error) {
	r.ownerDSUserID = userID
	if r.ds == nil || r.ds.UserID != userID {
		return nil, nil
	}
	return r.ds, nil
}

func (r *authzRepo) ListDataSources(ctx context.Context, userID string) ([]*model.DataSource, error) {
	r.listDSUserID = userID
	return nil, nil
}

func (r *authzRepo) UpdateDataSource(ctx context.Context, ds *model.DataSource) error {
	r.updatedDS = true
	return nil
}

func (r *authzRepo) DeleteDataSource(ctx context.Context, id string) error {
	r.deletedDS = true
	return nil
}

func (r *authzRepo) CreateTask(ctx context.Context, task *model.CollectionTask) error {
	r.createdTask = true
	return nil
}

func (r *authzRepo) GetTaskWithOwnerCheck(ctx context.Context, id, userID string) (*model.CollectionTask, error) {
	r.ownerTaskUserID = userID
	if r.task == nil || r.task.UserID != userID {
		return nil, nil
	}
	return r.task, nil
}

func (r *authzRepo) UpdateTask(ctx context.Context, task *model.CollectionTask) error {
	r.updatedTask = true
	return nil
}

func (r *authzRepo) DeleteTask(ctx context.Context, id string) error {
	r.deletedTask = true
	return nil
}

func (r *authzRepo) ListTasks(ctx context.Context, userID string) ([]*model.CollectionTask, error) {
	r.listTaskUserID = userID
	return nil, nil
}

func (r *authzRepo) GetResult(ctx context.Context, id string) (*model.CollectionResult, error) {
	return r.result, nil
}

func (r *authzRepo) ListResultsByTaskID(ctx context.Context, taskID string) ([]*model.CollectionResult, error) {
	r.listedResults = true
	return nil, nil
}

func collectionUserCtx(userID string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(auth.UserIDKey, userID))
}

func TestCollection_ListDataSourcesRequiresIdentity(t *testing.T) {
	repo := &authzRepo{}
	svc := &collectionServiceImpl{repo: repo}

	if _, err := svc.ListDataSources(context.Background()); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("空身份应返回 ErrUnauthenticated，实际 %v", err)
	}
	if repo.listDSUserID != "" {
		t.Fatalf("未认证请求不应触达 DAO，实际 userID=%q", repo.listDSUserID)
	}
}

func TestCollection_ListDataSourcesPassesIdentity(t *testing.T) {
	repo := &authzRepo{}
	svc := &collectionServiceImpl{repo: repo}

	if _, err := svc.ListDataSources(collectionUserCtx("user-1")); err != nil {
		t.Fatalf("ListDataSources: %v", err)
	}
	if repo.listDSUserID != "user-1" {
		t.Fatalf("DAO userID = %q", repo.listDSUserID)
	}
}

func TestCollection_UpdateDataSourceRejectsOtherUser(t *testing.T) {
	repo := &authzRepo{ds: &model.DataSource{ID: "ds-1", UserID: "user-1"}}
	svc := &collectionServiceImpl{repo: repo}
	name := "hijacked"

	if _, err := svc.UpdateDataSource(collectionUserCtx("user-2"), "ds-1", &name, nil, nil, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("跨用户更新应返回 ErrNotFound，实际 %v", err)
	}
	if repo.updatedDS {
		t.Fatal("归属校验失败后不应执行更新")
	}
}

func TestCollection_DeleteDataSourceRejectsOtherUser(t *testing.T) {
	repo := &authzRepo{ds: &model.DataSource{ID: "ds-1", UserID: "user-1"}}
	svc := &collectionServiceImpl{repo: repo}

	if err := svc.DeleteDataSource(collectionUserCtx("user-2"), "ds-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("跨用户删除应返回 ErrNotFound，实际 %v", err)
	}
	if repo.deletedDS {
		t.Fatal("归属校验失败后不应执行删除")
	}
}

func TestCollection_CreateTaskRejectsForeignDataSource(t *testing.T) {
	repo := &authzRepo{ds: &model.DataSource{ID: "ds-1", UserID: "user-1"}}
	svc := &collectionServiceImpl{repo: repo}

	if _, err := svc.CreateTask(collectionUserCtx("user-2"), "ds-1", "task", 1, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("跨用户创建任务应返回 ErrNotFound，实际 %v", err)
	}
	if repo.createdTask {
		t.Fatal("归属校验失败后不应创建任务")
	}
}

func TestCollection_ExecuteTaskRejectsOtherUser(t *testing.T) {
	repo := &authzRepo{task: &model.CollectionTask{
		ID: "task-1", DataSourceID: "ds-1", UserID: "user-1", Status: "PENDING",
	}}
	svc := &collectionServiceImpl{repo: repo}

	if _, err := svc.ExecuteTask(collectionUserCtx("user-2"), "task-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("跨用户执行任务应返回 ErrNotFound，实际 %v", err)
	}
	if repo.updatedTask {
		t.Fatal("归属校验失败后不应更新任务状态")
	}
}

func TestCollection_GetResultChecksTaskOwner(t *testing.T) {
	repo := &authzRepo{
		result: &model.CollectionResult{ID: "result-1", TaskID: "task-1", CreatedAt: time.Now()},
		task:   &model.CollectionTask{ID: "task-1", UserID: "user-1"},
	}
	svc := &collectionServiceImpl{repo: repo}

	if _, err := svc.GetCollectionResult(collectionUserCtx("user-2"), "result-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("跨用户读取结果应返回 ErrNotFound，实际 %v", err)
	}
}

func TestCollection_ListResultsChecksTaskOwner(t *testing.T) {
	repo := &authzRepo{task: &model.CollectionTask{ID: "task-1", UserID: "user-1"}}
	svc := &collectionServiceImpl{repo: repo}

	if _, err := svc.ListCollectionResults(collectionUserCtx("user-2"), "task-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("跨用户读取结果列表应返回 ErrNotFound，实际 %v", err)
	}
	if repo.listedResults {
		t.Fatal("归属校验失败后不应查询结果列表")
	}
}
