package handler

import (
	"context"
	"errors"
	"testing"

	"Logos/internal/service/messaging/contact/model"
	"Logos/internal/service/messaging/contact/service"
	pb "Logos/proto_gen/contact"

	"gorm.io/gorm"
)

type stubContactService struct {
	service.ContactService
	friendship *model.Friendship
	err        error
}

func (s *stubContactService) GetFriendship(_ context.Context, _, _ string) (*model.Friendship, error) {
	return s.friendship, s.err
}

func checkFriendship(t *testing.T, stub *stubContactService) *pb.CheckFriendshipResponse {
	t.Helper()
	resp, err := NewContactServiceImpl(stub).CheckFriendship(context.Background(), &pb.CheckFriendshipRequest{
		UserId:   "1",
		FriendId: "2",
	})
	if err != nil {
		t.Fatalf("CheckFriendship 返回 gRPC 错误: %v", err)
	}
	return resp
}

func TestCheckFriendshipMissingRecordIsNotAnError(t *testing.T) {
	resp := checkFriendship(t, &stubContactService{err: gorm.ErrRecordNotFound})
	if resp.Code != 200 || resp.IsFriend || resp.IsBlocked {
		t.Fatalf("无关系记录应返回 200/false/false，实际 %+v", resp)
	}
}

// 数据库故障不能伪装成「没有关系」：客户端会把它缓存成「未拉黑」。
func TestCheckFriendshipDatabaseErrorIsReported(t *testing.T) {
	resp := checkFriendship(t, &stubContactService{err: errors.New("db down")})
	if resp.Code != 500 {
		t.Fatalf("数据库故障应返回 500，实际 %+v", resp)
	}
	if resp.IsFriend || resp.IsBlocked {
		t.Fatalf("故障响应不得携带 false/false 的业务结论，实际 %+v", resp)
	}
}

func TestCheckFriendshipReportsBlocked(t *testing.T) {
	resp := checkFriendship(t, &stubContactService{
		friendship: &model.Friendship{Status: model.FriendshipStatusBlocked},
	})
	if resp.Code != 200 || !resp.IsBlocked || resp.IsFriend {
		t.Fatalf("拉黑关系应返回 200/blocked=true，实际 %+v", resp)
	}
}
