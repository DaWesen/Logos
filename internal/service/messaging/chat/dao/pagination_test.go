package dao

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"Logos/internal/service/messaging/chat/model"
	"Logos/pkg/pagination"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// dryRunChatDB 构造只生成 SQL、不建立连接的 GORM 实例，用于断言查询形态。
func dryRunChatDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		postgres.New(postgres.Config{DSN: "postgres://test:test@127.0.0.1:5432/logos?sslmode=disable"}),
		&gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true},
	)
	if err != nil {
		t.Fatalf("初始化 DryRun GORM 失败: %v", err)
	}
	return db
}

// TestSearchMessages_NormalizesPage 消息搜索曾经只在 service 层兜底，
// 内部调用 DAO 时会漏掉：pageSize=0 生成 LIMIT 0（永远搜不到东西），
// 负 page 生成负 OFFSET。DAO 自己也必须收敛一次。
func TestSearchMessages_NormalizesPage(t *testing.T) {
	db := dryRunChatDB(t)

	repo := NewChatRepository(db).(*chatRepositoryImpl)
	stmt := repo.messageSearchQuery(
		context.Background(), "user-1", "conv-1", model.ChatTypeGroup,
		"", time.Time{}, time.Time{}, 0, 2, 0,
	).Find(&[]model.Message{}).Statement
	listSQL := strings.ToUpper(stmt.SQL.String())
	if !strings.Contains(listSQL, "LIMIT") || !strings.Contains(listSQL, "OFFSET") {
		t.Fatalf("消息搜索必须在 SQL 侧分页，实际: %s", listSQL)
	}
	if strings.Contains(listSQL, "OFFSET -") || strings.Contains(listSQL, "LIMIT 0") {
		t.Fatalf("分页参数未兜底，生成了非法分页: %s", listSQL)
	}

	// 超大 pageSize 必须被截断到 100，offset 按截断后的页大小计算，
	// 否则第 3 页会跳到 1500 而不是 200。
	stmt = repo.messageSearchQuery(
		context.Background(), "user-1", "conv-1", model.ChatTypeGroup,
		"", time.Time{}, time.Time{}, 0, 3, 500,
	).Find(&[]model.Message{}).Statement
	if !containsVar(stmt.Vars, 200) || !containsVar(stmt.Vars, 100) {
		t.Fatalf("pageSize 未截断到 100 / offset 计算错误, vars=%v", stmt.Vars)
	}
}

func containsVar(vars []interface{}, want int) bool {
	for _, v := range vars {
		if got, ok := v.(int); ok && got == want {
			return true
		}
	}
	return false
}

// TestGetConversationList_PaginatesInSQL 会话列表分页必须由数据库完成：
// 早期实现先把用户全部会话拉进内存再切片，会话多时每次翻页都白读整表。
func TestGetConversationList_PaginatesInSQL(t *testing.T) {
	db := dryRunChatDB(t)

	repo := NewChatRepository(db).(*chatRepositoryImpl)
	page, pageSize := pagination.Normalize(2, 20)
	stmt := repo.conversationListQuery(context.Background(), "user-1", page, pageSize).
		Find(&[]model.Conversation{}).Statement
	listSQL := stmt.SQL.String()

	if !strings.Contains(strings.ToUpper(listSQL), "LIMIT") || !strings.Contains(strings.ToUpper(listSQL), "OFFSET") {
		t.Fatalf("会话列表必须在 SQL 侧分页，实际: %s", listSQL)
	}
	if !strings.Contains(listSQL, "conversation_participants") {
		t.Fatalf("会话列表应 JOIN 参与者表按用户过滤，实际: %s", listSQL)
	}
}

// TestGetGroupMembers_NormalizesPage 群成员分页同样不能把负 offset 交给数据库。
func TestGetGroupMembers_NormalizesPage(t *testing.T) {
	db := dryRunChatDB(t)

	var sqls []string
	if err := db.Callback().Query().After("gorm:query").Register("test:capture_sql", func(tx *gorm.DB) {
		sqls = append(sqls, tx.Statement.SQL.String())
	}); err != nil {
		t.Fatalf("注册 SQL 捕获回调失败: %v", err)
	}

	repo := NewChatRepository(db)
	if _, _, err := repo.GetGroupMembers(context.Background(), "group-1", -1, 0); err != nil {
		t.Fatalf("GetGroupMembers 失败: %v", err)
	}

	for _, s := range sqls {
		if strings.Contains(strings.ToUpper(s), "OFFSET") && strings.Contains(s, "-") {
			t.Fatalf("分页参数未兜底，生成了负 offset: %s", s)
		}
	}
}

// TestConversationCursor_RoundTrip 游标必须可逆，且保留 updated_at 的纳秒精度：
// 截断精度会让同一会话在翻页边界上重复出现。
func TestConversationCursor_RoundTrip(t *testing.T) {
	wantTime := time.Date(2026, 11, 3, 12, 34, 56, 123456789, time.FixedZone("CST", 8*3600))
	wantID := "private_1001_1002"

	cursor := encodeConversationCursor(wantTime, wantID)
	if cursor == "" {
		t.Fatal("游标不应为空")
	}
	if strings.Contains(cursor, wantID) {
		t.Fatalf("游标不应明文暴露内部字段: %s", cursor)
	}

	got, err := decodeConversationCursor(cursor)
	if err != nil {
		t.Fatalf("解析游标失败: %v", err)
	}
	if !got.UpdatedAt.Equal(wantTime) {
		t.Fatalf("updated_at = %v, want %v", got.UpdatedAt, wantTime)
	}
	if got.ID != wantID {
		t.Fatalf("id = %q, want %q", got.ID, wantID)
	}
}

// TestDecodeConversationCursor_RejectsInvalid 非法游标必须报错，
// 不能静默回退第一页——否则客户端会反复拉同一页，表现为「加载更多」卡死。
func TestDecodeConversationCursor_RejectsInvalid(t *testing.T) {
	cases := []string{
		"",
		"not-base64!!",
		base64.RawURLEncoding.EncodeToString([]byte("no-separator")),
		base64.RawURLEncoding.EncodeToString([]byte("2026-11-03|")),
		base64.RawURLEncoding.EncodeToString([]byte("not-a-time|conv-1")),
		base64.RawURLEncoding.EncodeToString([]byte("0001-01-01T00:00:00Z|conv-1")),
	}
	for _, c := range cases {
		if _, err := decodeConversationCursor(c); !errors.Is(err, ErrInvalidConversationCursor) {
			t.Fatalf("decodeConversationCursor(%q) err = %v, want ErrInvalidConversationCursor", c, err)
		}
	}
}

// TestGetConversationListByCursor_UsesKeyset 游标分页必须满足三个条件：
// 用 (updated_at, id) 做锚点、按同样顺序排序、不再出现 OFFSET/COUNT。
func TestGetConversationListByCursor_UsesKeyset(t *testing.T) {
	db := dryRunChatDB(t)

	var sqls []string
	if err := db.Callback().Query().After("gorm:query").Register("test:capture_cursor_sql", func(tx *gorm.DB) {
		sqls = append(sqls, tx.Statement.SQL.String())
	}); err != nil {
		t.Fatalf("注册 SQL 捕获回调失败: %v", err)
	}

	repo := NewChatRepository(db)
	cursor := encodeConversationCursor(time.Now().Add(-time.Minute), "conv-42")

	if _, _, _, err := repo.GetConversationListByCursor(context.Background(), "user-1", cursor, 20); err != nil {
		t.Fatalf("GetConversationListByCursor 失败: %v", err)
	}
	if len(sqls) == 0 {
		t.Fatal("没有捕获到查询 SQL")
	}

	listSQL := strings.ToUpper(strings.Join(sqls, "\n"))
	if strings.Contains(listSQL, "OFFSET") {
		t.Fatalf("keyset 分页不应生成 OFFSET: %s", listSQL)
	}
	if strings.Contains(listSQL, "COUNT(") {
		t.Fatalf("keyset 分页不应每页 COUNT 全量: %s", listSQL)
	}
	if !strings.Contains(listSQL, "(CONVERSATIONS.UPDATED_AT, CONVERSATIONS.ID) <") {
		t.Fatalf("keyset 分页缺少 (updated_at, id) 锚点: %s", listSQL)
	}
	if !strings.Contains(listSQL, "ORDER BY") {
		t.Fatalf("keyset 分页必须显式排序: %s", listSQL)
	}
}
