package dao

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestPurgeMessageIdempotencyBefore_BoundedDelete 幂等表清理必须是有限批次的 DELETE：
// 全量 DELETE 会长时间锁表，DELETE ... LIMIT 又不是 PostgreSQL 语法。
func TestPurgeMessageIdempotencyBefore_BoundedDelete(t *testing.T) {
	db := dryRunChatDB(t)
	repo := NewChatRepository(db).(*chatRepositoryImpl)

	query := repo.purgeMessageIdempotencyBeforeQuery(
		context.Background(), time.Now().Add(-7*24*time.Hour), 1000)
	sql := strings.ToUpper(query.Statement.SQL.String())

	for _, want := range []string{"DELETE FROM MESSAGE_IDEMPOTENCY", "CREATED_AT <", "LIMIT"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("清理 SQL 缺少 %q: %s", want, sql)
		}
	}
}
