package dao

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"Logos/internal/service/ai/collection/model"
	"Logos/pkg/logger"

	"gorm.io/gorm"
)

type CollectionRepository interface {
	AddDataSource(ctx context.Context, ds *model.DataSource) error
	GetDataSource(ctx context.Context, id string) (*model.DataSource, error)
	UpdateDataSource(ctx context.Context, ds *model.DataSource) error
	DeleteDataSource(ctx context.Context, id string) error
	ListDataSources(ctx context.Context) ([]*model.DataSource, error)

	CreateTask(ctx context.Context, task *model.CollectionTask) error
	GetTask(ctx context.Context, id string) (*model.CollectionTask, error)
	UpdateTask(ctx context.Context, task *model.CollectionTask) error
	DeleteTask(ctx context.Context, id string) error
	ListTasks(ctx context.Context) ([]*model.CollectionTask, error)

	CreateResult(ctx context.Context, result *model.CollectionResult) error
	GetResult(ctx context.Context, id string) (*model.CollectionResult, error)
	ListResultsByTaskID(ctx context.Context, taskID string) ([]*model.CollectionResult, error)
}

type collectionRepository struct {
	db *gorm.DB
}

func NewCollectionRepository(db *gorm.DB) CollectionRepository {
	return &collectionRepository{db: db}
}

func (r *collectionRepository) AddDataSource(ctx context.Context, ds *model.DataSource) error {
	logger.Info("添加数据源",
		logger.StringField("name", ds.Name),
		logger.IntField("type", ds.Type))

	result := r.db.WithContext(ctx).Create(ds)
	if result.Error != nil {
		logger.Error("添加数据源失败",
			logger.StringField("name", ds.Name),
			logger.ErrorField(result.Error))
		return result.Error
	}
	return nil
}

func (r *collectionRepository) GetDataSource(ctx context.Context, id string) (*model.DataSource, error) {
	var ds model.DataSource
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&ds)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, result.Error
	}
	return &ds, nil
}

func (r *collectionRepository) UpdateDataSource(ctx context.Context, ds *model.DataSource) error {
	logger.Info("更新数据源",
		logger.StringField("id", ds.ID))

	result := r.db.WithContext(ctx).Save(ds)
	return result.Error
}

func (r *collectionRepository) DeleteDataSource(ctx context.Context, id string) error {
	logger.Info("删除数据源",
		logger.StringField("id", id))

	r.db.WithContext(ctx).Delete(&model.CollectionTask{}, "data_source_id = ?", id)
	return r.db.WithContext(ctx).Delete(&model.DataSource{}, "id = ?", id).Error
}

func (r *collectionRepository) ListDataSources(ctx context.Context) ([]*model.DataSource, error) {
	var list []*model.DataSource
	result := r.db.WithContext(ctx).Order("created_at desc").Find(&list)
	if result.Error != nil {
		return nil, result.Error
	}
	return list, nil
}

func (r *collectionRepository) CreateTask(ctx context.Context, task *model.CollectionTask) error {
	logger.Info("创建采集任务",
		logger.StringField("name", task.Name))

	return r.db.WithContext(ctx).Create(task).Error
}

func (r *collectionRepository) GetTask(ctx context.Context, id string) (*model.CollectionTask, error) {
	var t model.CollectionTask
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&t)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, result.Error
	}
	return &t, nil
}

func (r *collectionRepository) UpdateTask(ctx context.Context, task *model.CollectionTask) error {
	logger.Info("更新采集任务",
		logger.StringField("id", task.ID))
	return r.db.WithContext(ctx).Save(task).Error
}

func (r *collectionRepository) DeleteTask(ctx context.Context, id string) error {
	logger.Info("删除采集任务",
		logger.StringField("id", id))

	r.db.WithContext(ctx).Delete(&model.CollectionResult{}, "task_id = ?", id)
	return r.db.WithContext(ctx).Delete(&model.CollectionTask{}, "id = ?", id).Error
}

func (r *collectionRepository) ListTasks(ctx context.Context) ([]*model.CollectionTask, error) {
	var list []*model.CollectionTask
	result := r.db.WithContext(ctx).Order("created_at desc").Find(&list)
	if result.Error != nil {
		return nil, result.Error
	}
	return list, nil
}

func (r *collectionRepository) CreateResult(ctx context.Context, result *model.CollectionResult) error {
	logger.Info("创建采集结果",
		logger.StringField("task_id", result.TaskID))

	err := r.db.WithContext(ctx).Create(result).Error
	if err == nil {
		now := time.Now()
		r.db.WithContext(ctx).Model(&model.CollectionTask{}).
			Where("id = ?", result.TaskID).
			Updates(map[string]interface{}{
				"status":        result.Status,
				"last_run_time": now,
				"updated_at":    now,
			})
	}
	return err
}

func (r *collectionRepository) GetResult(ctx context.Context, id string) (*model.CollectionResult, error) {
	var rslt model.CollectionResult
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&rslt)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, result.Error
	}
	return &rslt, nil
}

func (r *collectionRepository) ListResultsByTaskID(ctx context.Context, taskID string) ([]*model.CollectionResult, error) {
	var results []*model.CollectionResult
	err := r.db.WithContext(ctx).Where("task_id = ?", taskID).Order("created_at desc").Find(&results).Error
	return results, err
}

func MarshalConfig(m map[string]string) string {
	if m == nil {
		return "{}"
	}
	data, _ := json.Marshal(m)
	return string(data)
}

func UnmarshalConfig(s string) map[string]string {
	if s == "" || s == "{}" {
		return make(map[string]string)
	}
	var m map[string]string
	json.Unmarshal([]byte(s), &m)
	return m
}

func DataFormatString(f int) string {
	switch f {
	case 1:
		return "JSON"
	case 2:
		return "CSV"
	case 3:
		return "XML"
	case 4:
		return "TXT"
	case 5:
		return "PDF"
	case 6:
		return "WORD"
	case 7:
		return "EXCEL"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", f)
	}
}
