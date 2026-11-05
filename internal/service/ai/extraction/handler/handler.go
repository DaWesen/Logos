package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"Logos/internal/service/ai/extraction/service"
	pbCommon "Logos/proto_gen/common"
	pb "Logos/proto_gen/extraction"
)

type KnowledgeExtractionServiceImpl struct {
	pb.UnimplementedKnowledgeExtractionServiceServer
	ExtractionService service.ExtractionService
}

// CreateTask implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) CreateTask(ctx context.Context, req *pb.CreateExtractionTaskReq) (*pb.ExtractionTaskResp, error) {
	resp := &pb.ExtractionTaskResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	var scheduledTime *string
	if req.ScheduledTime != nil {
		scheduledTime = req.ScheduledTime
	}

	task, err := s.ExtractionService.CreateTask(ctx,
		int32(req.Type),
		req.DataId,
		req.DataType,
		req.Parameters,
		scheduledTime,
	)
	if err != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = err.Error()
		return resp, nil
	}

	resp.Task = extractionToProto(task)
	return resp, nil
}

// UpdateTask implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) UpdateTask(ctx context.Context, req *pb.UpdateExtractionTaskReq) (*pb.ExtractionTaskResp, error) {
	resp := &pb.ExtractionTaskResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	var taskType *int32
	if req.Type != nil {
		t := int32(*req.Type)
		taskType = &t
	}

	task, updateErr := s.ExtractionService.UpdateTask(ctx,
		req.Id,
		taskType,
		req.Parameters,
		req.ScheduledTime,
	)
	if updateErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = updateErr.Error()
		return resp, nil
	}

	resp.Task = extractionToProto(task)
	return resp, nil
}

// DeleteTask implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) DeleteTask(ctx context.Context, req *pb.GetByIdReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}

	if delErr := s.ExtractionService.DeleteTask(ctx, req.Id); delErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = delErr.Error()
	}
	return resp, nil
}

// GetTask implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) GetTask(ctx context.Context, req *pb.GetByIdReq) (*pb.ExtractionTaskResp, error) {
	resp := &pb.ExtractionTaskResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	task, getErr := s.ExtractionService.GetTask(ctx, req.Id)
	if getErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = getErr.Error()
		return resp, nil
	}
	if task == nil {
		resp.BaseResp.StatusCode = 404
		resp.BaseResp.StatusMessage = "任务不存在"
		return resp, nil
	}

	resp.Task = extractionToProto(task)
	return resp, nil
}

// ListTasks implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) ListTasks(ctx context.Context, req *pb.EmptyReq) (*pb.BatchExtractionTaskResp, error) {
	resp := &pb.BatchExtractionTaskResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	tasks, listErr := s.ExtractionService.ListTasks(ctx)
	if listErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = listErr.Error()
		return resp, nil
	}

	for _, t := range tasks {
		resp.Tasks = append(resp.Tasks, extractionToProto(t))
	}
	return resp, nil
}

// ExecuteTask implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) ExecuteTask(ctx context.Context, req *pb.ExecuteExtractionTaskReq) (*pb.ExtractionResultResp, error) {
	resp := &pb.ExtractionResultResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	result, execErr := s.ExtractionService.ExecuteTask(ctx, req.TaskId)
	if execErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = execErr.Error()
	}

	protoResult, convertErr := resultToProto(result)
	if convertErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = convertErr.Error()
		return resp, nil
	}
	resp.Result = protoResult
	return resp, nil
}

// CancelTask implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) CancelTask(ctx context.Context, req *pb.CancelByTaskIdReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}

	if cancelErr := s.ExtractionService.CancelTask(ctx, req.TaskId); cancelErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = cancelErr.Error()
	}
	return resp, nil
}

// GetExtractionResult implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) GetExtractionResult(ctx context.Context, req *pb.GetByIdReq) (*pb.ExtractionResultResp, error) {
	resp := &pb.ExtractionResultResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	result, getErr := s.ExtractionService.GetExtractionResult(ctx, req.Id)
	if getErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = getErr.Error()
		return resp, nil
	}
	if result == nil {
		resp.BaseResp.StatusCode = 404
		resp.BaseResp.StatusMessage = "结果不存在"
		return resp, nil
	}

	protoResult, convertErr := resultToProto(result)
	if convertErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = convertErr.Error()
		return resp, nil
	}
	resp.Result = protoResult
	return resp, nil
}

// ListExtractionResults implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) ListExtractionResults(ctx context.Context, req *pb.GetByTaskIdReq) (*pb.BatchExtractionResultResp, error) {
	resp := &pb.BatchExtractionResultResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	results, listErr := s.ExtractionService.ListExtractionResults(ctx, req.TaskId)
	if listErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = listErr.Error()
		return resp, nil
	}

	for i, r := range results {
		protoResult, convertErr := resultToProto(r)
		if convertErr != nil {
			resp.BaseResp.StatusCode = 500
			resp.BaseResp.StatusMessage = fmt.Sprintf("第 %d 条结果: %v", i+1, convertErr)
			resp.Results = nil
			return resp, nil
		}
		resp.Results = append(resp.Results, protoResult)
	}
	return resp, nil
}

// ExtractFromText implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) ExtractFromText(ctx context.Context, req *pb.TextExtractionReq) (*pb.TextExtractionResp, error) {
	resp := &pb.TextExtractionResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	entities, relations, triples, keyphrases, extractErr := s.ExtractionService.ExtractFromText(
		ctx,
		req.Text,
		int32(req.Type),
		req.Parameters,
	)
	if extractErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = extractErr.Error()
		return resp, nil
	}

	protoEntities, err := entitiesToProto(entities)
	if err != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = fmt.Sprintf("实体结果格式错误: %v", err)
		return resp, nil
	}
	protoRelations, err := relationsToProto(relations)
	if err != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = fmt.Sprintf("关系结果格式错误: %v", err)
		return resp, nil
	}
	protoTriples, err := triplesToProto(triples)
	if err != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = fmt.Sprintf("三元组结果格式错误: %v", err)
		return resp, nil
	}

	resp.Entities = protoEntities
	resp.Relations = protoRelations
	resp.Triples = protoTriples

	if len(keyphrases) > 0 {
		resp.Keyphrases = keyphrases
	}

	return resp, nil
}

func extractionToProto(t interface {
	GetID() string
	GetType() int
	GetDataID() string
	GetDataType() string
	GetStatus() int
	GetParameters() string
	GetCreatedAt() time.Time
	GetUpdatedAt() time.Time
}) *pb.ExtractionTask {
	parameters := make(map[string]string)
	if t.GetParameters() != "" {
		json.Unmarshal([]byte(t.GetParameters()), &parameters)
	}

	return &pb.ExtractionTask{
		Id:         t.GetID(),
		Type:       pb.ExtractionTaskType(t.GetType()),
		DataId:     t.GetDataID(),
		DataType:   t.GetDataType(),
		Status:     pb.TaskStatus(t.GetStatus()),
		Parameters: parameters,
		CreatedAt:  t.GetCreatedAt().Unix(),
		UpdatedAt:  t.GetUpdatedAt().Unix(),
	}
}

func resultToProto(r interface {
	GetID() string
	GetTaskID() string
	GetStatus() int
	GetEntities() string
	GetRelations() string
	GetTriples() string
	GetSummary() *string
	GetKeyphrases() *string
	GetErrorMsg() *string
	GetStartTime() int64
	GetEndTime() int64
}) (*pb.ExtractionResult, error) {
	var entityList []map[string]interface{}
	if r.GetEntities() != "" {
		// 落库的 JSON 理论上由本服务写入，但历史数据/手工修库都可能
		// 留下坏行。反序列化失败必须报错，不能返回一个空列表让调用方
		// 以为「这次没抽到实体」。
		if err := json.Unmarshal([]byte(r.GetEntities()), &entityList); err != nil {
			return nil, fmt.Errorf("解析实体结果失败: %w", err)
		}
	}
	entities, err := entitiesToProto(entityList)
	if err != nil {
		return nil, fmt.Errorf("解析实体结果失败: %w", err)
	}

	var relationList []map[string]interface{}
	if r.GetRelations() != "" {
		if err := json.Unmarshal([]byte(r.GetRelations()), &relationList); err != nil {
			return nil, fmt.Errorf("解析关系结果失败: %w", err)
		}
	}
	relations, err := relationsToProto(relationList)
	if err != nil {
		return nil, fmt.Errorf("解析关系结果失败: %w", err)
	}

	var tripleList []map[string]interface{}
	if r.GetTriples() != "" {
		if err := json.Unmarshal([]byte(r.GetTriples()), &tripleList); err != nil {
			return nil, fmt.Errorf("解析三元组结果失败: %w", err)
		}
	}
	triples, err := triplesToProto(tripleList)
	if err != nil {
		return nil, fmt.Errorf("解析三元组结果失败: %w", err)
	}

	var keyphrases []string
	if r.GetKeyphrases() != nil {
		if err := json.Unmarshal([]byte(*r.GetKeyphrases()), &keyphrases); err != nil {
			return nil, fmt.Errorf("解析关键词结果失败: %w", err)
		}
	}

	return &pb.ExtractionResult{
		Id:           r.GetID(),
		TaskId:       r.GetTaskID(),
		Status:       pb.TaskStatus(r.GetStatus()),
		Entities:     entities,
		Relations:    relations,
		Triples:      triples,
		Summary:      r.GetSummary(),
		Keyphrases:   keyphrases,
		ErrorMessage: r.GetErrorMsg(),
		StartTime:    r.GetStartTime(),
		EndTime:      r.GetEndTime(),
	}, nil
}
