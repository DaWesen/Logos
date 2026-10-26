package service

import (
	"testing"
	"time"

	"Logos/internal/service/messaging/chat/model"
)

func TestCheckMuted(t *testing.T) {
	now := time.Now()
	future := now.Add(10 * time.Minute)
	past := now.Add(-time.Minute)

	tests := []struct {
		name    string
		member  *model.GroupMember
		blocked bool
	}{
		{
			name:    "未禁言放行",
			member:  &model.GroupMember{MuteType: model.MuteTypeNone},
			blocked: false,
		},
		{
			name:    "永久禁言拦截",
			member:  &model.GroupMember{MuteType: model.MuteTypePermanent},
			blocked: true,
		},
		{
			name:    "临时禁言未到期拦截",
			member:  &model.GroupMember{MuteType: model.MuteTypeTemporary, MuteUntil: &future},
			blocked: true,
		},
		{
			name:    "临时禁言已到期放行",
			member:  &model.GroupMember{MuteType: model.MuteTypeTemporary, MuteUntil: &past},
			blocked: false,
		},
		{
			name:    "临时禁言缺少截止时间按仍生效处理",
			member:  &model.GroupMember{MuteType: model.MuteTypeTemporary},
			blocked: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkMuted(tt.member, now)
			if tt.blocked && err == nil {
				t.Fatalf("应拦截发送，实际放行")
			}
			if !tt.blocked && err != nil {
				t.Fatalf("应放行发送，实际拦截: %v", err)
			}
		})
	}
}
