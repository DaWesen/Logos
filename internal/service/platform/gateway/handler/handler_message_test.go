package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	pbCommon "Logos/proto_gen/common"
	pb "Logos/proto_gen/message"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
)

type fakeMessageClient struct {
	pb.MessageServiceClient
	deleteCalls int
	gotTopic    pb.Topic
}

func (f *fakeMessageClient) DeleteTopic(_ context.Context, req *pb.GetByTopicReq, _ ...grpc.CallOption) (*pbCommon.BaseResp, error) {
	f.deleteCalls++
	f.gotTopic = req.Topic
	return &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}, nil
}

// 路径参数解析失败曾返回 0：/topics/abc 会被当成 DATA_COLLECTION，
// 请求不但没被拒绝，还会真的删除一个业务主题。
func TestParseTopicParam_RejectsInvalidAndOutOfRange(t *testing.T) {
	cases := []struct {
		raw  string
		want pb.Topic
		ok   bool
	}{
		{"0", pb.Topic_DATA_COLLECTION, true},
		{"6", pb.Topic_SYSTEM_EVENT, true},
		{"abc", 0, false},
		{"", 0, false},
		{"-1", 0, false},
		{"7", 0, false},
		{"1.5", 0, false},
		{"999999999999999999999", 0, false},
	}

	for _, tc := range cases {
		got, ok := parseTopicParam(tc.raw)
		if ok != tc.ok {
			t.Fatalf("parseTopicParam(%q) ok = %v, want %v", tc.raw, ok, tc.ok)
		}
		if ok && got != tc.want {
			t.Fatalf("parseTopicParam(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestDeleteTopic_InvalidParamDoesNotCallDownstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	client := &fakeMessageClient{}
	h := &Handler{MessageClient: client}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/message/topic/abc", nil)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Params = gin.Params{{Key: "topic", Value: "abc"}}

	h.DeleteTopic(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if client.deleteCalls != 0 {
		t.Fatalf("非法参数不应调用下游 DeleteTopic，实际调用 %d 次", client.deleteCalls)
	}
}

func TestDeleteTopic_ValidParamPassesTopicThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)

	client := &fakeMessageClient{}
	h := &Handler{MessageClient: client}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/message/topic/6", nil)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Params = gin.Params{{Key: "topic", Value: "6"}}

	h.DeleteTopic(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if client.deleteCalls != 1 || client.gotTopic != pb.Topic_SYSTEM_EVENT {
		t.Fatalf("下游调用 %d 次, topic = %v, want 1 次 SYSTEM_EVENT", client.deleteCalls, client.gotTopic)
	}
}
