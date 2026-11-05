package dao

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"Logos/internal/service/ai/vector/model"

	"github.com/milvus-io/milvus/client/v2/entity"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestCollectionEmbedder(t *testing.T) {
	tests := []struct {
		name        string
		collection  *model.VectorCollection
		wantNil     bool
		wantErrText string
	}{
		{
			name:       "集合为空",
			collection: nil,
			wantNil:    true,
		},
		{
			name:       "未配置嵌入模型时使用全局模型",
			collection: &model.VectorCollection{ID: "col_1"},
			wantNil:    true,
		},
		{
			name: "只有模型没有密钥",
			collection: &model.VectorCollection{
				ID:        "col_1",
				Embedding: model.ModelConfig{Model: "text-embedding-3-small"},
			},
			wantErrText: "配置不完整",
		},
		{
			name: "只有密钥没有模型",
			collection: &model.VectorCollection{
				ID:        "col_1",
				Embedding: model.ModelConfig{APIKey: "sk-test"},
			},
			wantErrText: "配置不完整",
		},
		{
			name: "模型与密钥齐全",
			collection: &model.VectorCollection{
				ID: "col_1",
				Embedding: model.ModelConfig{
					Model:   "text-embedding-3-small",
					BaseURL: "https://example.invalid/v1",
					APIKey:  "sk-test",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			embedder, err := collectionEmbedder(tt.collection)
			if tt.wantErrText != "" {
				if err == nil {
					t.Fatalf("应返回错误，实际得到 embedder=%v", embedder)
				}
				if !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("错误信息应包含 %q，实际 %v", tt.wantErrText, err)
				}
				if embedder != nil {
					t.Fatalf("配置非法时不应返回 embedder")
				}
				return
			}
			if err != nil {
				t.Fatalf("不应返回错误，实际 %v", err)
			}
			if tt.wantNil && embedder != nil {
				t.Fatalf("应回退全局模型，实际返回了自定义 embedder")
			}
			if !tt.wantNil && embedder == nil {
				t.Fatalf("应创建自定义 embedder，实际为 nil")
			}
		})
	}
}

func TestToFloat32Vectors(t *testing.T) {
	tests := []struct {
		name        string
		vecs        [][]float64
		want        int
		wantErrText string
		wantDim     int
	}{
		{
			name:    "正常转换",
			vecs:    [][]float64{{0.5, 0.25}, {1, 0}},
			want:    2,
			wantDim: 2,
		},
		{
			name:    "空批次",
			vecs:    [][]float64{},
			want:    0,
			wantDim: 0,
		},
		{
			name:        "条数少于输入",
			vecs:        [][]float64{{0.5}},
			want:        2,
			wantErrText: "条数",
		},
		{
			name:        "返回空向量",
			vecs:        [][]float64{{}},
			want:        1,
			wantErrText: "空向量",
		},
		{
			name:        "维度不一致",
			vecs:        [][]float64{{0.5, 0.25}, {0.5}},
			want:        2,
			wantErrText: "维度",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toFloat32Vectors(tt.vecs, tt.want)
			if tt.wantErrText != "" {
				if err == nil {
					t.Fatalf("应返回错误，实际得到 %v", got)
				}
				if !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("错误信息应包含 %q，实际 %v", tt.wantErrText, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("不应返回错误，实际 %v", err)
			}
			if len(got) != tt.want {
				t.Fatalf("向量条数应为 %d，实际 %d", tt.want, len(got))
			}
			for i, vec := range got {
				if len(vec) != tt.wantDim {
					t.Fatalf("第 %d 条向量维度应为 %d，实际 %d", i, tt.wantDim, len(vec))
				}
			}
		})
	}
}

func TestToFloat32VectorsPreservesValues(t *testing.T) {
	got, err := toFloat32Vectors([][]float64{{0.5, -0.25}}, 1)
	if err != nil {
		t.Fatalf("不应返回错误，实际 %v", err)
	}
	if got[0][0] != 0.5 || got[0][1] != -0.25 {
		t.Fatalf("数值转换结果不正确: %v", got[0])
	}
}

func TestCollectionDimension(t *testing.T) {
	tests := []struct {
		name        string
		collection  *entity.Collection
		want        int
		wantErrText string
	}{
		{
			name: "集合为 nil",
		},
		{
			name:       "没有 schema",
			collection: &entity.Collection{},
		},
		{
			name: "没有向量字段",
			collection: &entity.Collection{Schema: &entity.Schema{
				Fields: []*entity.Field{{Name: "id"}},
			}},
		},
		{
			name: "向量字段缺少 dim",
			collection: &entity.Collection{Schema: &entity.Schema{
				Fields: []*entity.Field{{Name: "vector", TypeParams: map[string]string{}}},
			}},
			wantErrText: "dim",
		},
		{
			name: "维度不是整数",
			collection: &entity.Collection{Schema: &entity.Schema{
				Fields: []*entity.Field{{Name: "vector", TypeParams: map[string]string{"dim": "1O24"}}},
			}},
			wantErrText: "不是合法整数",
		},
		{
			name: "维度非正数",
			collection: &entity.Collection{Schema: &entity.Schema{
				Fields: []*entity.Field{{Name: "vector", TypeParams: map[string]string{"dim": "0"}}},
			}},
			wantErrText: "正数",
		},
		{
			name: "正常维度",
			collection: &entity.Collection{Schema: &entity.Schema{
				Fields: []*entity.Field{{Name: "vector", TypeParams: map[string]string{"dim": "1024"}}},
			}},
			want: 1024,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := collectionDimension(tt.collection)
			if tt.wantErrText != "" {
				if err == nil {
					t.Fatalf("应返回错误，实际维度 %d", got)
				}
				if !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("错误信息应包含 %q，实际 %v", tt.wantErrText, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("不应返回错误，实际 %v", err)
			}
			if got != tt.want {
				t.Fatalf("维度应为 %d，实际 %d", tt.want, got)
			}
		})
	}
}

func TestCollectionNeedsReindex(t *testing.T) {
	existing := &model.VectorCollectionRecord{
		IndexType:  int(model.HNSW),
		Dimension:  1024,
		Parameters: map[string]string{"M": "16"},
	}

	tests := []struct {
		name string
		coll *model.VectorCollection
		want bool
	}{
		{
			name: "只改名称不重建索引",
			coll: &model.VectorCollection{
				Name:       "new name",
				IndexType:  model.HNSW,
				Dimension:  1024,
				Parameters: map[string]string{"M": "16"},
			},
			want: false,
		},
		{
			name: "缺少参数视为变更",
			coll: &model.VectorCollection{
				IndexType: model.HNSW,
				Dimension: 1024,
			},
			want: true,
		},
		{
			name: "索引类型变化",
			coll: &model.VectorCollection{
				IndexType:  model.IVF_FLAT,
				Dimension:  1024,
				Parameters: map[string]string{"M": "16"},
			},
			want: true,
		},
		{
			name: "维度变化",
			coll: &model.VectorCollection{
				IndexType:  model.HNSW,
				Dimension:  768,
				Parameters: map[string]string{"M": "16"},
			},
			want: true,
		},
		{
			name: "索引参数变化",
			coll: &model.VectorCollection{
				IndexType:  model.HNSW,
				Dimension:  1024,
				Parameters: map[string]string{"M": "32"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := collectionNeedsReindex(existing, tt.coll); got != tt.want {
				t.Fatalf("是否需要重建索引应为 %v，实际 %v", tt.want, got)
			}
		})
	}

	// nil 与空 map 在 JSON 层面等价，不应该触发一次无意义的重建。
	emptyExisting := &model.VectorCollectionRecord{IndexType: int(model.FLAT), Dimension: 768}
	emptyRequest := &model.VectorCollection{
		IndexType:  model.FLAT,
		Dimension:  768,
		Parameters: map[string]string{},
	}
	if collectionNeedsReindex(emptyExisting, emptyRequest) {
		t.Fatal("nil 与空参数 map 应视为同一份配置")
	}
}

func TestCollectionRecordRoundTripPreservesParameters(t *testing.T) {
	coll := &model.VectorCollection{
		ID:         "col-1",
		Name:       "知识库",
		IndexType:  model.HNSW,
		Dimension:  1024,
		Parameters: map[string]string{"M": "16", "efConstruction": "200"},
		Embedding:  model.ModelConfig{Model: "bge-m3", BaseURL: "http://embedding"},
	}

	got := recordToModel(collectionToRecord(coll))
	if !maps.Equal(got.Parameters, coll.Parameters) {
		t.Fatalf("参数往返后应保持一致，期望 %v，实际 %v", coll.Parameters, got.Parameters)
	}
	if got.Embedding != coll.Embedding {
		t.Fatalf("嵌入配置往返后应保持一致，期望 %+v，实际 %+v", coll.Embedding, got.Embedding)
	}

	empty := recordToModel(collectionToRecord(&model.VectorCollection{ID: "col-2"}))
	if empty.Parameters != nil {
		t.Fatalf("未设置参数时应保持 nil，实际 %v", empty.Parameters)
	}
}

// sqlRecorder / stubConnPool 让 GORM 在 DryRun 下只生成 SQL，不连接数据库。
type sqlRecorder struct {
	mu   sync.Mutex
	sqls []string
}

func (r *sqlRecorder) LogMode(gormlogger.LogLevel) gormlogger.Interface { return r }

func (r *sqlRecorder) Info(context.Context, string, ...interface{}) {}

func (r *sqlRecorder) Warn(context.Context, string, ...interface{}) {}

func (r *sqlRecorder) Error(context.Context, string, ...interface{}) {}

func (r *sqlRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sqls = append(r.sqls, sql)
}

func (r *sqlRecorder) joined() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.sqls, "\n")
}

type stubConnPool struct{}

func (stubConnPool) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, errors.New("dry-run 测试不应执行 prepare")
}

func (stubConnPool) ExecContext(context.Context, string, ...interface{}) (sql.Result, error) {
	return nil, errors.New("dry-run 测试不应执行 SQL")
}

func (stubConnPool) QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error) {
	return nil, errors.New("dry-run 测试不应执行查询")
}

func (stubConnPool) QueryRowContext(context.Context, string, ...interface{}) *sql.Row {
	panic("dry-run 测试不应执行 QueryRow")
}

func newVectorDryRunRepository(t *testing.T) (*vectorRepository, *sqlRecorder) {
	t.Helper()

	recorder := &sqlRecorder{}
	db, err := gorm.Open(
		postgres.New(postgres.Config{Conn: stubConnPool{}}),
		&gorm.Config{DryRun: true, DisableAutomaticPing: true, Logger: recorder},
	)
	if err != nil {
		t.Fatalf("初始化 DryRun 数据库失败: %v", err)
	}
	return &vectorRepository{db: db}, recorder
}

func TestSaveCollectionMetadataPersistsParameters(t *testing.T) {
	repo, recorder := newVectorDryRunRepository(t)
	coll := &model.VectorCollection{
		ID:         "col-1",
		Name:       "知识库",
		ModelType:  model.Custom,
		IndexType:  model.HNSW,
		Dimension:  1024,
		Size:       42,
		Parameters: map[string]string{"M": "16"},
		Embedding:  model.ModelConfig{Model: "bge-m3"},
	}

	if err := repo.saveCollectionMetadata(context.Background(), coll); err != nil {
		t.Fatalf("DryRun 更新不应失败: %v", err)
	}

	got := recorder.joined()
	for _, want := range []string{
		`UPDATE "vector_collections"`,
		`"parameters"`,
		`{"M":"16"}`,
		`"embedding_model"`,
		`WHERE id = 'col-1'`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("SQL 缺少 %q，实际:\n%s", want, got)
		}
	}
	// 归属、数量和创建时间不属于更新接口，不能被一次重命名覆盖。
	for _, forbidden := range []string{`"user_id"`, `"size"`, `"created_at"`} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("SQL 不应写入 %q，实际:\n%s", forbidden, got)
		}
	}
}

// newVectorFailingDBRepository 返回一个「每次查询都失败」的仓库，用来模拟
// 数据库故障。GORM 不做 DryRun，Find/Count 会真正走 stubConnPool 并拿到错误。
func newVectorFailingDBRepository(t *testing.T) *vectorRepository {
	t.Helper()

	db, err := gorm.Open(
		postgres.New(postgres.Config{Conn: stubConnPool{}}),
		&gorm.Config{DisableAutomaticPing: true},
	)
	if err != nil {
		t.Fatalf("初始化故障数据库失败: %v", err)
	}
	return &vectorRepository{db: db}
}

func TestListCollectionsRefusesOwnerlessFallback(t *testing.T) {
	repo := newVectorFailingDBRepository(t)

	collections, err := repo.ListCollections(context.Background(), "alice")
	if err == nil {
		t.Fatalf("数据库故障时不能回退成 Milvus 全量列表，实际返回 %d 条", len(collections))
	}
	if collections != nil {
		t.Fatalf("失败时不应返回半截结果: %v", collections)
	}
	if !strings.Contains(err.Error(), "查询集合列表失败") {
		t.Fatalf("错误应说明数据库查询失败，实际: %v", err)
	}
}

func TestListCollectionsWithoutStoreReturnsError(t *testing.T) {
	cases := map[string]*vectorRepository{
		"数据库故障且 Milvus 未连接": newVectorFailingDBRepository(t),
		"两套存储都未初始化":         {},
	}

	for name, repo := range cases {
		t.Run(name, func(t *testing.T) {
			collections, err := repo.ListCollections(context.Background(), "")
			if err == nil {
				t.Fatalf("读不到集合时应报错而不是返回空列表，实际 %d 条", len(collections))
			}
			if collections != nil {
				t.Fatalf("失败时不应返回半截结果: %v", collections)
			}
		})
	}
}

func TestParseCollectionSize(t *testing.T) {
	tests := []struct {
		name    string
		stats   map[string]string
		want    int64
		wantErr string
	}{
		{name: "正常值", stats: map[string]string{"row_count": "42"}, want: 42},
		{name: "带空白", stats: map[string]string{"row_count": " 7 "}, want: 7},
		{name: "缺少字段", stats: map[string]string{"other": "1"}, wantErr: "row_count"},
		{name: "空 map", stats: map[string]string{}, wantErr: "row_count"},
		{name: "数字后缀垃圾", stats: map[string]string{"row_count": "12abc"}, wantErr: "解析"},
		{name: "负数", stats: map[string]string{"row_count": "-1"}, wantErr: "负数"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCollectionSize(tt.stats)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("应返回错误，实际得到 %d", got)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("错误信息应包含 %q，实际: %v", tt.wantErr, err)
				}
				if got != 0 {
					t.Fatalf("解析失败时应返回 0，实际 %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("不应失败: %v", err)
			}
			if got != tt.want {
				t.Fatalf("期望 %d，实际 %d", tt.want, got)
			}
		})
	}
}

func TestSearchAndTextSearchReportMissingMilvus(t *testing.T) {
	repo := &vectorRepository{}

	if _, err := repo.Search(context.Background(), "col-1", []float64{0.1}, 5, 0, nil); err == nil {
		t.Fatal("Milvus 未连接时 Search 必须报错，不能返回空结果")
	} else if !strings.Contains(err.Error(), "Milvus未连接") {
		t.Fatalf("错误应说明 Milvus 未连接，实际: %v", err)
	}

	if _, err := repo.TextSearch(context.Background(), "col-1", "hello", 5, 0, nil); err == nil {
		t.Fatal("Milvus 未连接时 TextSearch 必须报错，不能返回空结果")
	} else if !strings.Contains(err.Error(), "Milvus未连接") {
		t.Fatalf("错误应说明 Milvus 未连接，实际: %v", err)
	}
}
