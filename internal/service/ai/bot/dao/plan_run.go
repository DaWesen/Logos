package dao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"Logos/internal/bot/planner"
	"Logos/internal/service/ai/bot/model"

	"gorm.io/gorm"
)

// PlanRunStore 用 GORM 实现 planner.RunStore。
//
// planner 包只定义接口、不 import gorm；落库细节全部收在这里，
// 单测可以用内存实现替换，不必为了续跑逻辑起一个数据库。
type PlanRunStore struct {
	db *gorm.DB
}

// NewPlanRunStore 创建运行记录仓库。
func NewPlanRunStore(db *gorm.DB) *PlanRunStore {
	return &PlanRunStore{db: db}
}

func (s *PlanRunStore) CreateRun(ctx context.Context, run *planner.RunRecord) error {
	if run == nil {
		return errors.New("运行记录为空")
	}
	blob, err := marshalPlan(run.Plan)
	if err != nil {
		return err
	}
	row := &model.PlanRun{
		ID:             run.ID,
		BotID:          run.BotID,
		ConversationID: run.ConversationID,
		UserID:         run.UserID,
		Goal:           run.Goal,
		GoalHash:       run.GoalHash,
		Status:         string(run.Status),
		Revision:       run.Revision,
		PlanJSON:       blob,
		Error:          run.Error,
	}
	return s.db.WithContext(ctx).Create(row).Error
}

// UpdateRun 增量更新运行记录。
//
// revision 条件更新是刻意的：patch 之后主循环会写回新计划，
// 如果某个旧请求晚到，它不能让 revision 倒退、把新计划覆盖成旧计划。
func (s *PlanRunStore) UpdateRun(ctx context.Context, runID string, patch planner.RunPatch) error {
	updates := map[string]any{}
	if patch.Status != nil {
		updates["status"] = string(*patch.Status)
	}
	if patch.Error != nil {
		updates["error"] = *patch.Error
	}
	if patch.Revision != nil {
		updates["revision"] = *patch.Revision
	}
	if patch.Plan != nil {
		blob, err := marshalPlan(patch.Plan)
		if err != nil {
			return err
		}
		updates["plan_json"] = blob
	}
	if len(updates) == 0 {
		return nil
	}
	updates["updated_at"] = time.Now()

	query := s.db.WithContext(ctx).Model(&model.PlanRun{}).Where("id = ?", runID)
	if patch.Revision != nil {
		query = query.Where("revision <= ?", *patch.Revision)
	}
	result := query.Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("运行记录 %s 不存在或 revision 已更新", runID)
	}
	return nil
}

func (s *PlanRunStore) AppendStep(ctx context.Context, rec *planner.StepRecord) error {
	if rec == nil {
		return errors.New("步骤记录为空")
	}
	facts, err := json.Marshal(rec.Facts)
	if err != nil {
		return err
	}
	violations, err := json.Marshal(rec.Violations)
	if err != nil {
		return err
	}
	row := &model.PlanStepRecord{
		ID:         rec.ID,
		RunID:      rec.RunID,
		StepID:     rec.StepID,
		Revision:   rec.Revision,
		Attempt:    rec.Attempt,
		Status:     string(rec.Status),
		Effect:     string(rec.Effect),
		InputHash:  rec.InputHash,
		Output:     rec.Output,
		Facts:      model.JSONBlob(facts),
		Violations: model.JSONBlob(violations),
		TokensIn:   rec.TokensIn,
		TokensOut:  rec.TokensOut,
		DurationMs: rec.DurationMs,
		Error:      rec.Error,
		StartedAt:  rec.StartedAt,
		FinishedAt: rec.FinishedAt,
	}
	return s.db.WithContext(ctx).Create(row).Error
}

// FindStepOutput 查同一次运行里输入指纹一致的已完成副作用记录。
//
// 只要 input_hash 相同，就说明"这次重试要做的还是同一件事"，
// 直接复用结果，不再调用工具；不同 revision 也允许命中，
// 因为重规划通常只改下游，副作用步骤的意图并没有变。
func (s *PlanRunStore) FindStepOutput(ctx context.Context, runID string, stepID int, inputHash string) (*planner.StepRecord, error) {
	var row model.PlanStepRecord
	err := s.db.WithContext(ctx).
		Where("run_id = ? AND step_id = ? AND input_hash = ? AND status = ?", runID, stepID, inputHash, string(planner.StatusDone)).
		Order("revision DESC, attempt DESC, finished_at DESC").
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return stepRecordFromRow(&row)
}

func (s *PlanRunStore) GetRun(ctx context.Context, runID string) (*planner.RunRecord, error) {
	var row model.PlanRun
	err := s.db.WithContext(ctx).Where("id = ?", runID).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return runRecordFromRow(&row)
}

func (s *PlanRunStore) LatestRun(ctx context.Context, conversationID string) (*planner.RunRecord, error) {
	if conversationID == "" {
		return nil, nil
	}
	var row model.PlanRun
	err := s.db.WithContext(ctx).
		Where("conversation_id = ?", conversationID).
		Order("updated_at DESC").
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return runRecordFromRow(&row)
}

func (s *PlanRunStore) ListSteps(ctx context.Context, runID string) ([]*planner.StepRecord, error) {
	var rows []model.PlanStepRecord
	if err := s.db.WithContext(ctx).
		Where("run_id = ?", runID).
		Order("step_id, revision, attempt, finished_at").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*planner.StepRecord, 0, len(rows))
	for i := range rows {
		rec, err := stepRecordFromRow(&rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

func marshalPlan(plan *planner.Plan) (model.JSONBlob, error) {
	if plan == nil {
		return nil, nil
	}
	blob, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	return model.JSONBlob(blob), nil
}

func runRecordFromRow(row *model.PlanRun) (*planner.RunRecord, error) {
	var plan *planner.Plan
	if len(row.PlanJSON) > 0 && string(row.PlanJSON) != "null" {
		plan = &planner.Plan{}
		if err := json.Unmarshal(row.PlanJSON, plan); err != nil {
			return nil, fmt.Errorf("计划快照解析失败: %w", err)
		}
		plan.Revision = row.Revision
	}
	return &planner.RunRecord{
		ID:             row.ID,
		BotID:          row.BotID,
		ConversationID: row.ConversationID,
		UserID:         row.UserID,
		Goal:           row.Goal,
		GoalHash:       row.GoalHash,
		Status:         planner.RunStatus(row.Status),
		Revision:       row.Revision,
		Plan:           plan,
		Error:          row.Error,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}, nil
}

func stepRecordFromRow(row *model.PlanStepRecord) (*planner.StepRecord, error) {
	var facts []planner.Fact
	if len(row.Facts) > 0 {
		if err := json.Unmarshal(row.Facts, &facts); err != nil {
			return nil, fmt.Errorf("步骤 facts 解析失败: %w", err)
		}
	}
	var violations []planner.Violation
	if len(row.Violations) > 0 {
		if err := json.Unmarshal(row.Violations, &violations); err != nil {
			return nil, fmt.Errorf("步骤 violations 解析失败: %w", err)
		}
	}
	return &planner.StepRecord{
		ID:         row.ID,
		RunID:      row.RunID,
		StepID:     row.StepID,
		Revision:   row.Revision,
		Attempt:    row.Attempt,
		Status:     planner.StepStatus(row.Status),
		Effect:     planner.EffectLevel(row.Effect),
		InputHash:  row.InputHash,
		Output:     row.Output,
		Facts:      facts,
		Violations: violations,
		TokensIn:   row.TokensIn,
		TokensOut:  row.TokensOut,
		DurationMs: row.DurationMs,
		Error:      row.Error,
		StartedAt:  row.StartedAt,
		FinishedAt: row.FinishedAt,
	}, nil
}
