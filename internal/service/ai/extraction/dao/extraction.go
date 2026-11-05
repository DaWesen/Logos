package dao

import (
	"context"
	"errors"
	"time"

	"Logos/internal/service/ai/extraction/model"
	"Logos/pkg/logger"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const defaultScheduledTaskLimit = 100

type ExtractionRepository interface {
	CreateTask(ctx context.Context, task *model.ExtractionTask) error
	GetTask(ctx context.Context, id string) (*model.ExtractionTask, error)
	UpdateTask(ctx context.Context, task *model.ExtractionTask) error
	DeleteTask(ctx context.Context, id string) error
	ListTasks(ctx context.Context) ([]*model.ExtractionTask, error)
	ListTasksByStatus(ctx context.Context, status int) ([]*model.ExtractionTask, error)
	ListDueTasks(ctx context.Context, now time.Time, limit int) ([]*model.ExtractionTask, error)
	ClaimDueTask(ctx context.Context, taskID string, now time.Time) (bool, error)
	MarkTaskRunning(ctx context.Context, taskID string, now time.Time) (bool, error)
	ListStaleRunningTasks(ctx context.Context, cutoff time.Time, limit int) ([]*model.ExtractionTask, error)
	FailStaleTask(ctx context.Context, taskID string, cutoff time.Time, reason string) (bool, error)

	CreateResult(ctx context.Context, result *model.ExtractionResult) error
	GetResult(ctx context.Context, id string) (*model.ExtractionResult, error)
	ListResultsByTaskID(ctx context.Context, taskID string) ([]*model.ExtractionResult, error)

	WithTransaction(ctx context.Context, fn func(txRepo ExtractionRepository) error) error
	DB() *gorm.DB
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

// ListDueTasks 返回「已到计划时间、仍待执行」的任务，按计划时间升序取前 limit 条。
//
// 没有计划时间的任务不在扫描范围内：它们仍然只能由调用方显式触发。
// 排序用 scheduled_at 而不是 created_at，保证先到期的先执行；
// (status, scheduled_at) 复合索引让这条查询走索引区间扫描，而不是全表排序。
func (r *extractionRepository) ListDueTasks(ctx context.Context, now time.Time, limit int) ([]*model.ExtractionTask, error) {
	if limit <= 0 {
		limit = defaultScheduledTaskLimit
	}

	var tasks []*model.ExtractionTask
	result := r.db.WithContext(ctx).
		Where("status = ? AND scheduled_at IS NOT NULL AND scheduled_at <= ?", model.TaskStatusPending, now).
		Order("scheduled_at asc").
		Limit(limit).
		Find(&tasks)
	if result.Error != nil {
		logger.Error("查询到期抽取任务失败", logger.ErrorField(result.Error))
		return nil, result.Error
	}
	return tasks, nil
}

// ClaimDueTask 原子领取一个已到期的待执行任务，返回是否真的抢到执行权。
//
// 多实例同时扫描到同一条任务时，只有一条 UPDATE 能命中 status = 待执行，
// 其余实例拿到 RowsAffected = 0 直接跳过，同一个计划任务不会被重复执行。
func (r *extractionRepository) ClaimDueTask(ctx context.Context, taskID string, now time.Time) (bool, error) {
	result := r.db.WithContext(ctx).
		Model(&model.ExtractionTask{}).
		Where("id = ? AND status = ? AND scheduled_at IS NOT NULL AND scheduled_at <= ?",
			taskID, model.TaskStatusPending, now).
		Updates(map[string]interface{}{
			"status":     model.TaskStatusRunning,
			"started_at": now,
			"updated_at": now,
		})
	if result.Error != nil {
		logger.Error("领取到期抽取任务失败",
			logger.StringField("id", taskID),
			logger.ErrorField(result.Error))
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// MarkTaskRunning 用条件更新把任务置为运行中，返回是否抢到执行权。
//
// 「先 GetTask 再 Save」在并发下不是互斥：两个请求都读到待执行，就会各跑一遍，
// 同一个任务被调用两次模型、写出两份结果。这里把状态判断下沉到 WHERE，
// 由数据库保证只有一方能从「非运行中」切到「运行中」。
// 条件只排除运行中，因此成功/失败/取消的任务仍可手动重跑。
func (r *extractionRepository) MarkTaskRunning(ctx context.Context, taskID string, now time.Time) (bool, error) {
	result := r.db.WithContext(ctx).
		Model(&model.ExtractionTask{}).
		Where("id = ? AND status <> ?", taskID, model.TaskStatusRunning).
		Updates(map[string]interface{}{
			"status":     model.TaskStatusRunning,
			"started_at": now,
			"ended_at":   nil,
			"updated_at": now,
		})
	if result.Error != nil {
		logger.Error("更新抽取任务状态失败",
			logger.StringField("id", taskID),
			logger.ErrorField(result.Error))
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// ListStaleRunningTasks 返回 started_at 早于 cutoff 且仍停留在运行中的任务。
func (r *extractionRepository) ListStaleRunningTasks(ctx context.Context, cutoff time.Time, limit int) ([]*model.ExtractionTask, error) {
	if limit <= 0 {
		limit = defaultScheduledTaskLimit
	}

	var tasks []*model.ExtractionTask
	result := r.db.WithContext(ctx).
		Where("status = ? AND started_at IS NOT NULL AND started_at < ?", model.TaskStatusRunning, cutoff).
		Order("started_at asc").
		Limit(limit).
		Find(&tasks)
	if result.Error != nil {
		logger.Error("查询卡死的抽取任务失败", logger.ErrorField(result.Error))
		return nil, result.Error
	}
	return tasks, nil
}

// FailStaleTask 把一条超时的运行中任务置为失败，并补一条带原因的失败结果。
//
// 任务被领取后如果进程崩溃，状态会永远停在运行中：调度器只扫待执行，
// 用户也无法重复触发（运行中不允许并发执行）。这里按 started_at 兜底回收。
// 更新带 status = 运行中 条件，任务若刚好完成则 RowsAffected = 0，
// 不会把成功任务改写成失败，也不会给它补假结果。
func (r *extractionRepository) FailStaleTask(ctx context.Context, taskID string, cutoff time.Time, reason string) (bool, error) {
	now := time.Now()
	failed := false

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task model.ExtractionTask
		if err := tx.Where("id = ?", taskID).First(&task).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if task.Status != model.TaskStatusRunning || task.StartedAt == nil || !task.StartedAt.Before(cutoff) {
			return nil
		}

		update := tx.Model(&model.ExtractionTask{}).
			Where("id = ? AND status = ?", taskID, model.TaskStatusRunning).
			Updates(map[string]interface{}{
				"status":     model.TaskStatusFailed,
				"ended_at":   now,
				"updated_at": now,
			})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected == 0 {
			return nil
		}

		result := &model.ExtractionResult{
			ID:        uuid.NewString(),
			TaskID:    taskID,
			Status:    model.ResultStatusFailed,
			ErrorMsg:  &reason,
			StartTime: task.StartedAt.Unix(),
			EndTime:   now.Unix(),
		}
		if err := tx.Create(result).Error; err != nil {
			return err
		}

		failed = true
		return nil
	})
	if err != nil {
		logger.Error("回收卡死的抽取任务失败",
			logger.StringField("id", taskID),
			logger.ErrorField(err))
		return false, err
	}
	return failed, nil
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

func (r *extractionRepository) WithTransaction(ctx context.Context, fn func(txRepo ExtractionRepository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txRepo := &extractionRepository{db: tx}
		return fn(txRepo)
	})
}

func (r *extractionRepository) DB() *gorm.DB {
	return r.db
}
