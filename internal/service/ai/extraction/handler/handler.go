package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"Logos/internal/service/ai/extraction/model"
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

	taskType, typeErr := taskTypeFromProto(req.Type)
	if typeErr != nil {
		resp.BaseResp.StatusCode = 400
		resp.BaseResp.StatusMessage = typeErr.Error()
		return resp, nil
	}

	var scheduledTime *string
	if req.ScheduledTime != nil {
		scheduledTime = req.ScheduledTime
	}

	task, err := s.ExtractionService.CreateTask(ctx,
		taskType,
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

	protoTask, convErr := extractionToProto(task)
	if convErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = convErr.Error()
		return resp, nil
	}
	resp.Task = protoTask
	return resp, nil
}

// UpdateTask implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) UpdateTask(ctx context.Context, req *pb.UpdateExtractionTaskReq) (*pb.ExtractionTaskResp, error) {
	resp := &pb.ExtractionTaskResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	var taskType *int32
	if req.Type != nil {
		mapped, typeErr := taskTypeFromProto(*req.Type)
		if typeErr != nil {
			resp.BaseResp.StatusCode = 400
			resp.BaseResp.StatusMessage = typeErr.Error()
			return resp, nil
		}
		taskType = &mapped
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

	protoTask, convErr := extractionToProto(task)
	if convErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = convErr.Error()
		return resp, nil
	}
	resp.Task = protoTask
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

	protoTask, convErr := extractionToProto(task)
	if convErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = convErr.Error()
		return resp, nil
	}
	resp.Task = protoTask
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

	for i, t := range tasks {
		protoTask, convErr := extractionToProto(t)
		if convErr != nil {
			resp.Tasks = nil
			resp.BaseResp.StatusCode = 500
			resp.BaseResp.StatusMessage = fmt.Sprintf("第 %d 个任务: %v", i+1, convErr)
			return resp, nil
		}
		resp.Tasks = append(resp.Tasks, protoTask)
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
		// ExecuteTask 失败时可能没有结果行（任务被并发抢占、限流、
		// 任务不存在等）。此时继续转 nil 结果会触发空指针 panic。
		return resp, nil
	}
	if result == nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = "抽取任务未返回结果"
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

	taskType, typeErr := taskTypeFromProto(req.Type)
	if typeErr != nil {
		resp.BaseResp.StatusCode = 400
		resp.BaseResp.StatusMessage = typeErr.Error()
		return resp, nil
	}

	entities, relations, triples, keyphrases, extractErr := s.ExtractionService.ExtractFromText(
		ctx,
		req.Text,
		taskType,
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

// taskTypeFromProto 把 proto 的 0 基枚举映射到存储层的 1 基类型。
// 两套枚举不是简单 ±1：proto 的 SUMMARIZATION 占 3，而存储层的
// 3 是三元组、4 才是摘要、5 是关键短语。直接强转会让实体识别（0）
// 进入「不支持的类型」分支，接口却已经返回创建成功。
func taskTypeFromProto(t pb.ExtractionTaskType) (int32, error) {
	switch t {
	case pb.ExtractionTaskType_ENTITY_RECOGNITION:
		return model.TaskTypeEntityRecognition, nil
	case pb.ExtractionTaskType_RELATION_EXTRACTION:
		return model.TaskTypeRelationExtraction, nil
	case pb.ExtractionTaskType_TRIPLE_EXTRACTION:
		return model.TaskTypeTripleExtraction, nil
	case pb.ExtractionTaskType_KEYPHRASE_EXTRACTION:
		return model.TaskTypeKeyphraseExtraction, nil
	case pb.ExtractionTaskType_SUMMARIZATION:
		return 0, fmt.Errorf("摘要任务尚未实现")
	default:
		return 0, fmt.Errorf("未知的抽取任务类型: %d", int32(t))
	}
}

func taskTypeToProto(t int) (pb.ExtractionTaskType, error) {
	switch t {
	case model.TaskTypeEntityRecognition:
		return pb.ExtractionTaskType_ENTITY_RECOGNITION, nil
	case model.TaskTypeRelationExtraction:
		return pb.ExtractionTaskType_RELATION_EXTRACTION, nil
	case model.TaskTypeTripleExtraction:
		return pb.ExtractionTaskType_TRIPLE_EXTRACTION, nil
	case model.TaskTypeSummarization:
		return pb.ExtractionTaskType_SUMMARIZATION, nil
	case model.TaskTypeKeyphraseExtraction:
		return pb.ExtractionTaskType_KEYPHRASE_EXTRACTION, nil
	default:
		return pb.ExtractionTaskType_ENTITY_RECOGNITION, fmt.Errorf("任务类型 %d 无法映射到 proto 枚举", t)
	}
}

func taskStatusToProto(s int) (pb.TaskStatus, error) {
	switch s {
	case model.TaskStatusPending:
		return pb.TaskStatus_PENDING, nil
	case model.TaskStatusRunning:
		return pb.TaskStatus_RUNNING, nil
	case model.TaskStatusSucceeded:
		return pb.TaskStatus_SUCCESS, nil
	case model.TaskStatusFailed:
		return pb.TaskStatus_FAILED, nil
	case model.TaskStatusCancelled:
		return pb.TaskStatus_CANCELLED, nil
	default:
		return pb.TaskStatus_PENDING, fmt.Errorf("任务状态 %d 无法映射到 proto 枚举", s)
	}
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
}) (*pb.ExtractionTask, error) {
	parameters := make(map[string]string)
	if t.GetParameters() != "" {
		if err := json.Unmarshal([]byte(t.GetParameters()), &parameters); err != nil {
			return nil, fmt.Errorf("解析任务参数失败: %w", err)
		}
	}

	taskType, err := taskTypeToProto(t.GetType())
	if err != nil {
		return nil, err
	}
	status, err := taskStatusToProto(t.GetStatus())
	if err != nil {
		return nil, err
	}

	return &pb.ExtractionTask{
		Id:         t.GetID(),
		Type:       taskType,
		DataId:     t.GetDataID(),
		DataType:   t.GetDataType(),
		Status:     status,
		Parameters: parameters,
		CreatedAt:  t.GetCreatedAt().Unix(),
		UpdatedAt:  t.GetUpdatedAt().Unix(),
	}, nil
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

	status, err := taskStatusToProto(r.GetStatus())
	if err != nil {
		return nil, err
	}

	return &pb.ExtractionResult{
		Id:           r.GetID(),
		TaskId:       r.GetTaskID(),
		Status:       status,
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
