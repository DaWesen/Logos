package gateway

import (
	"testing"

	"github.com/google/uuid"
)

func TestDeriveMessageID(t *testing.T) {
	base := DeriveMessageID("1001", "req-1")

	if _, err := uuid.Parse(base); err != nil {
		t.Fatalf("派生 ID 不是合法 UUID: %v", err)
	}
	if again := DeriveMessageID("1001", "req-1"); again != base {
		t.Fatalf("同一 (sender, request_id) 必须得到同一 ID: %s != %s", base, again)
	}
	if other := DeriveMessageID("1001", "req-2"); other == base {
		t.Fatal("不同 request_id 不应得到同一 ID")
	}
	if other := DeriveMessageID("1002", "req-1"); other == base {
		t.Fatal("不同 sender 不应得到同一 ID")
	}
}
