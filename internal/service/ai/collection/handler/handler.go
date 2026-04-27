package handler

import (
	"context"
	"encoding/json"
	"time"

	"Logos/internal/service/ai/collection/service"
	pb "Logos/proto_gen/collection"
	pbCommon "Logos/proto_gen/common"
)

type DataCollectionServiceImpl struct {
	pb.UnimplementedDataCollectionServiceServer
	CollectionService service.CollectionService
}

// AddDataSource implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) AddDataSource(ctx context.Context, req *pb.AddDataSourceReq) (*pb.DataSourceResp, error) {
	resp := &pb.DataSourceResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	ds, addErr := s.CollectionService.AddDataSource(ctx,
		req.Name,
		int32(req.Type),
		req.Url,
		req.Config,
		req.Description,
	)
	if addErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = addErr.Error()
		return resp, nil
	}

	resp.DataSource = dataSourceToProto(ds)
	return resp, nil
}

// UpdateDataSource implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) UpdateDataSource(ctx context.Context, req *pb.UpdateDataSourceReq) (*pb.DataSourceResp, error) {
	resp := &pb.DataSourceResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	var dsType *int32
	if req.Type != nil {
		t := int32(*req.Type)
		dsType = &t
	}

	ds, updateErr := s.CollectionService.UpdateDataSource(ctx,
		req.Id,
		req.Name,
		dsType,
		req.Url,
		req.Config,
		req.Description,
	)
	if updateErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = updateErr.Error()
		return resp, nil
	}

	resp.DataSource = dataSourceToProto(ds)
	return resp, nil
}

// DeleteDataSource implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) DeleteDataSource(ctx context.Context, req *pb.DeleteDataSourceReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}

	if delErr := s.CollectionService.DeleteDataSource(ctx, req.Id); delErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = delErr.Error()
	}
	return resp, nil
}

// GetDataSource implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) GetDataSource(ctx context.Context, req *pb.GetByIdReq) (*pb.DataSourceResp, error) {
	resp := &pb.DataSourceResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	ds, getErr := s.CollectionService.GetDataSource(ctx, req.Id)
	if getErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = getErr.Error()
		return resp, nil
	}
	if ds == nil {
		resp.BaseResp.StatusCode = 404
		resp.BaseResp.StatusMessage = "数据源不存在"
		return resp, nil
	}

	resp.DataSource = dataSourceToProto(ds)
	return resp, nil
}

// ListDataSources implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) ListDataSources(ctx context.Context, req *pb.EmptyReq) (*pb.BatchDataSourceResp, error) {
	resp := &pb.BatchDataSourceResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	list, listErr := s.CollectionService.ListDataSources(ctx)
	if listErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = listErr.Error()
		return resp, nil
	}

	for _, ds := range list {
		resp.DataSources = append(resp.DataSources, dataSourceToProto(ds))
	}
	return resp, nil
}

// CreateTask implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) CreateTask(ctx context.Context, req *pb.CreateTaskReq) (*pb.TaskResp, error) {
	resp := &pb.TaskResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	task, createErr := s.CollectionService.CreateTask(ctx,
		req.DataSourceId,
		req.Name,
		int32(req.Format),
		req.Schedule,
	)
	if createErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = createErr.Error()
		return resp, nil
	}

	resp.Task = collectionTaskToProto(task)
	return resp, nil
}

// UpdateTask implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) UpdateTask(ctx context.Context, req *pb.UpdateTaskReq) (*pb.TaskResp, error) {
	resp := &pb.TaskResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	var format *int32
	if req.Format != nil {
		f := int32(*req.Format)
		format = &f
	}

	task, updateErr := s.CollectionService.UpdateTask(ctx,
		req.Id,
		req.Name,
		format,
		req.Schedule,
	)
	if updateErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = updateErr.Error()
		return resp, nil
	}

	resp.Task = collectionTaskToProto(task)
	return resp, nil
}

// DeleteTask implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) DeleteTask(ctx context.Context, req *pb.GetByIdReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}

	if delErr := s.CollectionService.DeleteTask(ctx, req.Id); delErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = delErr.Error()
	}
	return resp, nil
}

// GetTask implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) GetTask(ctx context.Context, req *pb.GetByIdReq) (*pb.TaskResp, error) {
	resp := &pb.TaskResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	task, getErr := s.CollectionService.GetTask(ctx, req.Id)
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

	resp.Task = collectionTaskToProto(task)
	return resp, nil
}

// ListTasks implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) ListTasks(ctx context.Context, req *pb.EmptyReq) (*pb.BatchTaskResp, error) {
	resp := &pb.BatchTaskResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	tasks, listErr := s.CollectionService.ListTasks(ctx)
	if listErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = listErr.Error()
		return resp, nil
	}

	for _, t := range tasks {
		resp.Tasks = append(resp.Tasks, collectionTaskToProto(t))
	}
	return resp, nil
}

// ExecuteTask implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) ExecuteTask(ctx context.Context, req *pb.ExecuteTaskReq) (*pb.CollectionResultResp, error) {
	resp := &pb.CollectionResultResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	result, execErr := s.CollectionService.ExecuteTask(ctx, req.TaskId)
	if execErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = execErr.Error()
	}

	resp.Result = collectionResultToProto(result)
	return resp, nil
}

// StopTask implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) StopTask(ctx context.Context, req *pb.StopByTaskIdReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}

	if stopErr := s.CollectionService.StopTask(ctx, req.TaskId); stopErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = stopErr.Error()
	}
	return resp, nil
}

// GetCollectionResult implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) GetCollectionResult(ctx context.Context, req *pb.GetByIdReq) (*pb.CollectionResultResp, error) {
	resp := &pb.CollectionResultResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	result, getErr := s.CollectionService.GetCollectionResult(ctx, req.Id)
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

	resp.Result = collectionResultToProto(result)
	return resp, nil
}

// ListCollectionResults implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) ListCollectionResults(ctx context.Context, req *pb.GetByTaskIdReq) (*pb.BatchCollectionResultResp, error) {
	resp := &pb.BatchCollectionResultResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	results, listErr := s.CollectionService.ListCollectionResults(ctx, req.TaskId)
	if listErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = listErr.Error()
		return resp, nil
	}

	for _, r := range results {
		resp.Results = append(resp.Results, collectionResultToProto(r))
	}
	return resp, nil
}

func dataSourceToProto(d interface {
	GetID() string
	GetName() string
	GetType() int
	GetURL() string
	GetConfig() string
	GetDescription() *string
	GetCreatedAt() time.Time
	GetUpdatedAt() time.Time
}) *pb.DataSource {
	config := make(map[string]string)
	json.Unmarshal([]byte(d.GetConfig()), &config)

	return &pb.DataSource{
		Id:          d.GetID(),
		Name:        d.GetName(),
		Type:        pb.DataSourceType(d.GetType()),
		Url:         d.GetURL(),
		Config:      config,
		Description: d.GetDescription(),
		CreatedAt:   d.GetCreatedAt().Unix(),
		UpdatedAt:   d.GetUpdatedAt().Unix(),
	}
}

func collectionTaskToProto(t interface {
	GetID() string
	GetDataSourceID() string
	GetName() string
	GetFormat() int
	GetStatus() string
	GetSchedule() *string
	GetCreatedAt() time.Time
	GetUpdatedAt() time.Time
}) *pb.CollectionTask {
	return &pb.CollectionTask{
		Id:           t.GetID(),
		DataSourceId: t.GetDataSourceID(),
		Name:         t.GetName(),
		Format:       pb.DataFormat(t.GetFormat()),
		Status:       t.GetStatus(),
		Schedule:     t.GetSchedule(),
		CreatedAt:    t.GetCreatedAt().Unix(),
		UpdatedAt:    t.GetUpdatedAt().Unix(),
	}
}

func collectionResultToProto(r interface {
	GetID() string
	GetTaskID() string
	GetStatus() string
	GetCollectedCount() int64
	GetProcessedCount() int64
	GetErrorMsg() *string
	GetStartTime() int64
	GetEndTime() int64
}) *pb.CollectionResult {
	return &pb.CollectionResult{
		Id:             r.GetID(),
		TaskId:         r.GetTaskID(),
		Status:         r.GetStatus(),
		CollectedCount: r.GetCollectedCount(),
		ProcessedCount: r.GetProcessedCount(),
		ErrorMessage:   r.GetErrorMsg(),
		StartTime:      r.GetStartTime(),
		EndTime:        r.GetEndTime(),
	}
}
