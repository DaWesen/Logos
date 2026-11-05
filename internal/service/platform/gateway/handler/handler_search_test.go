package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	pbCommon "Logos/proto_gen/common"
	pb "Logos/proto_gen/search"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
)

// fakeSearchClient 记录网关下发的索引类型。索引操作是不可逆的：
// 删错索引不会报错，只会让另一份数据消失，所以测试必须断言「发给谁」。
type fakeSearchClient struct {
	pb.SearchServiceClient

	calls int
	op    string
	got   *pb.GetByIndexTypeReq
}

func (f *fakeSearchClient) CreateIndex(_ context.Context, in *pb.GetByIndexTypeReq, _ ...grpc.CallOption) (*pbCommon.BaseResp, error) {
	f.calls++
	f.op = "create"
	f.got = in
	return &pbCommon.BaseResp{StatusCode: 200}, nil
}

func (f *fakeSearchClient) DeleteIndex(_ context.Context, in *pb.GetByIndexTypeReq, _ ...grpc.CallOption) (*pbCommon.BaseResp, error) {
	f.calls++
	f.op = "delete"
	f.got = in
	return &pbCommon.BaseResp{StatusCode: 200}, nil
}

func (f *fakeSearchClient) RefreshIndex(_ context.Context, in *pb.GetByIndexTypeReq, _ ...grpc.CallOption) (*pbCommon.BaseResp, error) {
	f.calls++
	f.op = "refresh"
	f.got = in
	return &pbCommon.BaseResp{StatusCode: 200}, nil
}

// 路由与生产保持一致：建索引用查询参数，删/刷新用 :id 路径参数。
func newSearchIndexRouter(client pb.SearchServiceClient) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &Handler{SearchClient: client}
	r := gin.New()
	r.POST("/search/indices", h.CreateIndex)
	r.DELETE("/search/indices/:id", h.DeleteIndex)
	r.POST("/search/indices/:id/refresh", h.RefreshIndex)
	return r
}

func doSearchIndex(t *testing.T, r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// POST /indices 没有 :type 路由参数，旧实现读 c.Param("type") 得到空串，
// parseBoundedInt 判定非法 → 建索引接口恒定 400，功能整体不可用。
func TestCreateIndex_DefaultsToEntityWhenTypeAbsent(t *testing.T) {
	client := &fakeSearchClient{}
	rec := doSearchIndex(t, newSearchIndexRouter(client), http.MethodPost, "/search/indices")

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP 状态 = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if client.calls != 1 {
		t.Fatalf("下游调用次数 = %d, want 1", client.calls)
	}
	if client.got.GetType() != pb.IndexType_ENTITY {
		t.Fatalf("缺省索引类型 = %v, want ENTITY", client.got.GetType())
	}
}

func TestCreateIndex_UsesQueryType(t *testing.T) {
	client := &fakeSearchClient{}
	rec := doSearchIndex(t, newSearchIndexRouter(client), http.MethodPost, "/search/indices?type=4")

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP 状态 = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if client.got.GetType() != pb.IndexType_DOCUMENT {
		t.Fatalf("索引类型 = %v, want DOCUMENT", client.got.GetType())
	}
}

// 这是本轮修掉的核心缺陷：路由参数名是 :id，旧实现却读 c.Param("type")，
// 空串被当成 0，`DELETE /indices/4` 实际删的是 ENTITY 索引。
func TestDeleteIndex_UsesPathIDNotEntity(t *testing.T) {
	client := &fakeSearchClient{}
	rec := doSearchIndex(t, newSearchIndexRouter(client), http.MethodDelete, "/search/indices/4")

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP 状态 = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if client.got.GetType() != pb.IndexType_DOCUMENT {
		t.Fatalf("删除的索引类型 = %v, want DOCUMENT（旧实现会删 ENTITY）", client.got.GetType())
	}
}

func TestRefreshIndex_UsesPathID(t *testing.T) {
	client := &fakeSearchClient{}
	rec := doSearchIndex(t, newSearchIndexRouter(client), http.MethodPost, "/search/indices/2/refresh")

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP 状态 = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if client.got.GetType() != pb.IndexType_QUESTION {
		t.Fatalf("刷新的索引类型 = %v, want QUESTION", client.got.GetType())
	}
}

// 路径参数非法时必须 400 且不下发：回落成 0 会让「删错对象」变成默认行为。
func TestIndexPath_RejectsInvalidID(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"删除非数字", http.MethodDelete, "/search/indices/abc"},
		{"删除越界", http.MethodDelete, "/search/indices/9"},
		{"删除负数", http.MethodDelete, "/search/indices/-1"},
		{"刷新非数字", http.MethodPost, "/search/indices/abc/refresh"},
		{"刷新越界", http.MethodPost, "/search/indices/9/refresh"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeSearchClient{}
			rec := doSearchIndex(t, newSearchIndexRouter(client), tc.method, tc.path)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("HTTP 状态 = %d, want 400, body = %s", rec.Code, rec.Body.String())
			}
			if client.calls != 0 {
				t.Fatalf("非法索引类型不应调用下游，实际调用 %d 次（op=%s）", client.calls, client.op)
			}
		})
	}
}

func TestCreateIndex_RejectsInvalidQueryType(t *testing.T) {
	for _, query := range []string{"?type=abc", "?type=9", "?type=-1"} {
		t.Run(query, func(t *testing.T) {
			client := &fakeSearchClient{}
			rec := doSearchIndex(t, newSearchIndexRouter(client), http.MethodPost, "/search/indices"+query)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("HTTP 状态 = %d, want 400, body = %s", rec.Code, rec.Body.String())
			}
			if client.calls != 0 {
				t.Fatalf("非法枚举不应调用下游，实际调用 %d 次", client.calls)
			}
		})
	}
}
