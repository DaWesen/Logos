package handler

import (
	"context"
	"encoding/json"
	"time"

	"Logos/internal/ai/extraction/service"
	pb "Logos/proto_gen/extraction"
	pbCommon "Logos/proto_gen/common"
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

	resp.Result = resultToProto(result)
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

	resp.Result = resultToProto(result)
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

	for _, r := range results {
		resp.Results = append(resp.Results, resultToProto(r))
	}
	return resp, nil
}

// ExtractFromText implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) ExtractFromText(ctx context.Context, req *pb.TextExtractionReq) (*pb.TextExtractionResp, error) {
	resp := &pb.TextExtractionResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	entities, relations, triples, summary, keyphrases, extractErr := s.ExtractionService.ExtractFromText(
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

	for _, e := range entities {
		id, _ := e["id"].(string)
		text, _ := e["text"].(string)
		typ, _ := e["type"].(string)
		confidence := 0.0
		if c, ok := e["confidence"].(float64); ok {
			confidence = c
		} else if c, ok := e["confidence"].(float32); ok {
			confidence = float64(c)
		}
		startPos := int32(0)
		if sp, ok := e["startPos"].(float64); ok {
			startPos = int32(sp)
		}
		endPos := int32(0)
		if ep, ok := e["endPos"].(float64); ok {
			endPos = int32(ep)
		}
		if id == "" {
			id = text + "_" + typ
		}
		resp.Entities = append(resp.Entities, &pb.ExtractedEntity{
			Id:         id,
			Text:       text,
			Type:       typ,
			Confidence: confidence,
			StartPos:   startPos,
			EndPos:     endPos,
		})
	}

	for _, r := range relations {
		id, _ := r["id"].(string)
		typ, _ := r["type"].(string)
		sourceID, _ := r["sourceId"].(string)
		targetID, _ := r["targetId"].(string)
		confidence := 0.0
		if c, ok := r["confidence"].(float64); ok {
			confidence = c
		}
		textVal, _ := r["text"].(string)
		if id == "" {
			id = sourceID + "_" + typ + "_" + targetID
		}
		resp.Relations = append(resp.Relations, &pb.ExtractedRelation{
			Id:         id,
			Type:       typ,
			SourceId:   sourceID,
			TargetId:   targetID,
			Confidence: confidence,
			Text:       textVal,
		})
	}

	for _, t := range triples {
		id, _ := t["id"].(string)
		subject, _ := t["subject"].(string)
		predicate, _ := t["predicate"].(string)
		object, _ := t["object"].(string)
		confidence := 0.0
		if c, ok := t["confidence"].(float64); ok {
			confidence = c
		}
		if id == "" {
			id = subject + "_" + predicate + "_" + object
		}
		resp.Triples = append(resp.Triples, &pb.Triple{
			Id:         id,
			Subject:    subject,
			Predicate:  predicate,
			Object:     object,
			Confidence: confidence,
		})
	}

	if summary != nil && *summary != "" {
		resp.Summary = summary
	}
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
}) *pb.ExtractionResult {
	var entities []*pb.ExtractedEntity
	if r.GetEntities() != "" {
		var entityList []map[string]interface{}
		json.Unmarshal([]byte(r.GetEntities()), &entityList)
		for _, e := range entityList {
			id, _ := e["id"].(string)
			text, _ := e["text"].(string)
			typ, _ := e["type"].(string)
			confidence := 0.0
			if c, ok := e["confidence"].(float64); ok {
				confidence = c
			}
			sp := int32(0)
			if v, ok := e["startPos"].(float64); ok {
				sp = int32(v)
			}
			ep := int32(0)
			if v, ok := e["endPos"].(float64); ok {
				ep = int32(v)
			}
			entities = append(entities, &pb.ExtractedEntity{
				Id: id, Text: text, Type: typ, Confidence: confidence, StartPos: sp, EndPos: ep,
			})
		}
	}

	var relations []*pb.ExtractedRelation
	if r.GetRelations() != "" {
		var relationList []map[string]interface{}
		json.Unmarshal([]byte(r.GetRelations()), &relationList)
		for _, rel := range relationList {
			id, _ := rel["id"].(string)
			typ, _ := rel["type"].(string)
			sourceID, _ := rel["sourceId"].(string)
			targetID, _ := rel["targetId"].(string)
			confidence := 0.0
			if c, ok := rel["confidence"].(float64); ok {
				confidence = c
			}
			textVal, _ := rel["text"].(string)
			relations = append(relations, &pb.ExtractedRelation{
				Id: id, Type: typ, SourceId: sourceID, TargetId: targetID, Confidence: confidence, Text: textVal,
			})
		}
	}

	var triples []*pb.Triple
	if r.GetTriples() != "" {
		var tripleList []map[string]interface{}
		json.Unmarshal([]byte(r.GetTriples()), &tripleList)
		for _, t := range tripleList {
			id, _ := t["id"].(string)
			subject, _ := t["subject"].(string)
			predicate, _ := t["predicate"].(string)
			obj, _ := t["object"].(string)
			confidence := 0.0
			if c, ok := t["confidence"].(float64); ok {
				confidence = c
			}
			triples = append(triples, &pb.Triple{
				Id: id, Subject: subject, Predicate: predicate, Object: obj, Confidence: confidence,
			})
		}
	}

	var keyphrases []string
	if r.GetKeyphrases() != nil {
		json.Unmarshal([]byte(*r.GetKeyphrases()), &keyphrases)
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
	}
}
