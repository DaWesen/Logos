package offline

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"Logos/internal/service/messaging/types"
	"Logos/pkg/push"
)

func TestTruncateBody_RuneSafe(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		maxRunes int
		want     string
	}{
		{"短文本不截断", "你好", 100, "你好"},
		{"ASCII 恰好等于上限", strings.Repeat("a", 100), 100, strings.Repeat("a", 100)},
		{"ASCII 超限", strings.Repeat("a", 101), 100, strings.Repeat("a", 100) + "..."},
		// 原实现按字节切：100 字节只能保留 33 个汉字，且可能切在多字节中间。
		{"中文按字符截断", strings.Repeat("中", 101), 100, strings.Repeat("中", 100) + "..."},
		{"emoji 不被切成半个", "🎉🎉🎉", 2, "🎉🎉..."},
		{"上限为 0", "任何内容", 0, ""},
		{"上限为负数", "任何内容", -1, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateBody(tc.input, tc.maxRunes)
			if got != tc.want {
				t.Fatalf("truncateBody(%q, %d) = %q, want %q", tc.input, tc.maxRunes, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("截断结果不是合法 UTF-8: %q", got)
			}
		})
	}
}

func TestSummarizePushResults(t *testing.T) {
	success := push.PushResult{Success: true}
	permanent := push.PushResult{Failure: push.PushFailure{Kind: push.PushFailurePermanent}}
	retryable := push.PushResult{Failure: push.PushFailure{Kind: push.PushFailureRetryable}}
	unknown := push.PushResult{Failure: push.PushFailure{Kind: push.PushFailureUnknown}}
	// 网络层失败不会携带分类，Kind 为空。
	networkErr := push.PushResult{Error: "dial tcp: i/o timeout"}

	cases := []struct {
		name    string
		results []push.PushResult
		wantErr bool
	}{
		{"空结果视为无需重投", nil, false},
		{"全部成功不重投", []push.PushResult{success, success}, false},
		{"永久失败不重投", []push.PushResult{permanent}, false},
		{"永久失败与成功混合不重投", []push.PushResult{success, permanent}, false},
		{"可重试失败需要重投", []push.PushResult{retryable}, true},
		{"未知失败需要重投", []push.PushResult{unknown}, true},
		{"网络层失败需要重投", []push.PushResult{networkErr}, true},
		{"永久+可重试混合需要重投", []push.PushResult{permanent, retryable}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := summarizePushResults(tc.results)
			if tc.wantErr {
				if !errors.Is(err, ErrOfflinePushRetry) {
					t.Fatalf("期望 ErrOfflinePushRetry, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("期望 nil, got %v", err)
			}
		})
	}
}

func TestDedupeRecipientIDs(t *testing.T) {
	t.Run("无重复时复用入参切片", func(t *testing.T) {
		ids := []string{"u1", "u2", "u3"}
		got := dedupeRecipientIDs(ids)
		if len(got) != 3 {
			t.Fatalf("长度 = %d, want 3", len(got))
		}
		if &got[0] != &ids[0] {
			t.Fatal("无重复场景发生了不必要的拷贝")
		}
	})

	t.Run("空输入返回 nil", func(t *testing.T) {
		if got := dedupeRecipientIDs(nil); got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	})

	t.Run("去重并按首次出现顺序保留", func(t *testing.T) {
		ids := []string{"u2", "u1", "u2", "u3", "u1"}
		got := dedupeRecipientIDs(ids)
		want := []string{"u2", "u1", "u3"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	})

	t.Run("剔除空 ID 与空白 ID", func(t *testing.T) {
		ids := []string{"", "u1", "   ", "u1", "u2"}
		got := dedupeRecipientIDs(ids)
		want := []string{"u1", "u2"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	})

	t.Run("全部为空时返回空切片", func(t *testing.T) {
		got := dedupeRecipientIDs([]string{"", " ", "\t"})
		if len(got) != 0 {
			t.Fatalf("got %v, want empty", got)
		}
	})

	t.Run("去除首尾空白", func(t *testing.T) {
		got := dedupeRecipientIDs([]string{" u1 ", "u1"})
		if len(got) != 1 || got[0] != "u1" {
			t.Fatalf("got %v, want [u1]", got)
		}
	})
}

func TestOfflineRecipients(t *testing.T) {
	t.Run("群成员包含发送者时不推给自己", func(t *testing.T) {
		event := &types.MessageEvent{
			SenderID:     "u1",
			RecipientIDs: []string{"u1", "u2", "u3"},
		}
		got := offlineRecipients(event, nil)
		want := []string{"u2", "u3"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	})

	t.Run("QQ 桥接必须回显给发送者", func(t *testing.T) {
		event := &types.MessageEvent{
			SenderID:     "u1",
			RecipientIDs: []string{"u1", "u2"},
			Channel:      "qq",
			Metadata:     map[string]string{"qq_bound_user": "true"},
		}
		got := offlineRecipients(event, nil)
		if len(got) != 2 || got[0] != "u1" || got[1] != "u2" {
			t.Fatalf("got %v, want [u1 u2]", got)
		}
	})

	t.Run("在线用户不推", func(t *testing.T) {
		event := &types.MessageEvent{
			SenderID:     "u1",
			RecipientIDs: []string{"u1", "u2", "u3"},
		}
		got := offlineRecipients(event, []string{"u2"})
		if len(got) != 1 || got[0] != "u3" {
			t.Fatalf("got %v, want [u3]", got)
		}
	})

	t.Run("重复接收者只推一次", func(t *testing.T) {
		event := &types.MessageEvent{
			SenderID:     "u9",
			RecipientIDs: []string{"u2", "u2", "", "u3"},
		}
		got := offlineRecipients(event, nil)
		if len(got) != 2 || got[0] != "u2" || got[1] != "u3" {
			t.Fatalf("got %v, want [u2 u3]", got)
		}
	})

	t.Run("nil 事件不 panic", func(t *testing.T) {
		if got := offlineRecipients(nil, nil); got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	})
}
