package dao

import (
	"context"
	"time"

	"Logos/internal/service/ai/extraction/model"
	"Logos/pkg/logger"

	"gorm.io/gorm"
)

type ExtractionRepository interface {
	CreateTask(ctx context.Context, task *model.ExtractionTask) error
	GetTask(ctx context.Context, id string) (*model.ExtractionTask, error)
	UpdateTask(ctx context.Context, task *model.ExtractionTask) error
	DeleteTask(ctx context.Context, id string) error
	ListTasks(ctx context.Context) ([]*model.ExtractionTask, error)
	ListTasksByStatus(ctx context.Context, status int) ([]*model.ExtractionTask, error)

	CreateResult(ctx context.Context, result *model.ExtractionResult) error
	GetResult(ctx context.Context, id string) (*model.ExtractionResult, error)
	ListResultsByTaskID(ctx context.Context, taskID string) ([]*model.ExtractionResult, error)
}

type extractionRepository struct {
	db *gorm.DB
}

func NewExtractionRepository(db *gorm.DB) ExtractionRepository {
	return &extractionRepository{db: db}
}

func (r *extractionRepository) CreateTask(ctx context.Context, task *model.ExtractionTask) error {
	logger.Info("创建抽取任务",
		logger.StringField("data_id", task.DataID),
		logger.IntField("type", task.Type))

	result := r.db.WithContext(ctx).Create(task)
	if result.Error != nil {
		logger.Error("创建抽取任务失败",
			logger.StringField("data_id", task.DataID),
			logger.ErrorField(result.Error))
		return result.Error
	}

	logger.Info("创建抽取任务成功",
		logger.StringField("id", task.ID))

	return nil
}

func (r *extractionRepository) GetTask(ctx context.Context, id string) (*model.ExtractionTask, error) {
	var task model.ExtractionTask
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&task)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		logger.Error("查询抽取任务失败",
			logger.StringField("id", id),
			logger.ErrorField(result.Error))
		return nil, result.Error
	}
	return &task, nil
}

func (r *extractionRepository) UpdateTask(ctx context.Context, task *model.ExtractionTask) error {
	logger.Info("更新抽取任务",
		logger.StringField("id", task.ID))

	result := r.db.WithContext(ctx).Save(task)
	if result.Error != nil {
		logger.Error("更新抽取任务失败",
			logger.StringField("id", task.ID),
			logger.ErrorField(result.Error))
		return result.Error
	}

	return nil
}

func (r *extractionRepository) DeleteTask(ctx context.Context, id string) error {
	logger.Info("删除抽取任务",
		logger.StringField("id", id))

	result := r.db.WithContext(ctx).Delete(&model.ExtractionTask{}, "id = ?", id)
	if result.Error != nil {
		logger.Error("删除抽取任务失败",
			logger.StringField("id", id),
			logger.ErrorField(result.Error))
		return result.Error
	}

	r.db.WithContext(ctx).Where("task_id = ?", id).Delete(&model.ExtractionResult{})

	return nil
}

func (r *extractionRepository) ListTasks(ctx context.Context) ([]*model.ExtractionTask, error) {
	var tasks []*model.ExtractionTask
	result := r.db.WithContext(ctx).Order("created_at desc").Find(&tasks)
	if result.Error != nil {
		logger.Error("列出抽取任务失败", logger.ErrorField(result.Error))
		return nil, result.Error
	}
	return tasks, nil
}

func (r *extractionRepository) ListTasksByStatus(ctx context.Context, status int) ([]*model.ExtractionTask, error) {
	var tasks []*model.ExtractionTask
	result := r.db.WithContext(ctx).Where("status = ?", status).Order("created_at asc").Find(&tasks)
	if result.Error != nil {
		logger.Error("按状态列出抽取任务失败",
			logger.IntField("status", status),
			logger.ErrorField(result.Error))
		return nil, result.Error
	}
	return tasks, nil
}

func (r *extractionRepository) CreateResult(ctx context.Context, result *model.ExtractionResult) error {
	logger.Info("创建抽取结果",
		logger.StringField("task_id", result.TaskID))

	dbResult := r.db.WithContext(ctx).Create(result)
	if dbResult.Error != nil {
		logger.Error("创建抽取结果失败",
			logger.StringField("task_id", result.TaskID),
			logger.ErrorField(dbResult.Error))
		return dbResult.Error
	}

	taskUpdates := map[string]interface{}{
		"status":     result.Status,
		"ended_at":   time.Now(),
		"updated_at": time.Now(),
	}
	r.db.WithContext(ctx).Model(&model.ExtractionTask{}).Where("id = ?", result.TaskID).Updates(taskUpdates)

	return nil
}

func (r *extractionRepository) GetResult(ctx context.Context, id string) (*model.ExtractionResult, error) {
	var result model.ExtractionResult
	dbResult := r.db.WithContext(ctx).Where("id = ?", id).First(&result)
	if dbResult.Error != nil {
		if dbResult.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		logger.Error("查询抽取结果失败",
			logger.StringField("id", id),
			logger.ErrorField(dbResult.Error))
		return nil, dbResult.Error
	}
	return &result, nil
}

func (r *extractionRepository) ListResultsByTaskID(ctx context.Context, taskID string) ([]*model.ExtractionResult, error) {
	var results []*model.ExtractionResult
	dbResult := r.db.WithContext(ctx).Where("task_id = ?", taskID).Order("created_at desc").Find(&results)
	if dbResult.Error != nil {
		logger.Error("列出抽取结果失败",
			logger.StringField("task_id", taskID),
			logger.ErrorField(dbResult.Error))
		return nil, dbResult.Error
	}
	return results, nil
}
