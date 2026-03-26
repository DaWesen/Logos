package handler

import (
	collection "Noah/kitex_gen/collection"
	common "Noah/kitex_gen/common"
	"context"
)

// DataCollectionServiceImpl implements the last service interface defined in the IDL.
type DataCollectionServiceImpl struct{}

// AddDataSource implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) AddDataSource(ctx context.Context, req *collection.AddDataSourceReq) (resp *collection.DataSourceResp, err error) {
	// TODO: Your code here...
	return
}

// UpdateDataSource implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) UpdateDataSource(ctx context.Context, req *collection.UpdateDataSourceReq) (resp *collection.DataSourceResp, err error) {
	// TODO: Your code here...
	return
}

// DeleteDataSource implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) DeleteDataSource(ctx context.Context, req *collection.DeleteDataSourceReq) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// GetDataSource implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) GetDataSource(ctx context.Context, id string) (resp *collection.DataSourceResp, err error) {
	// TODO: Your code here...
	return
}

// ListDataSources implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) ListDataSources(ctx context.Context) (resp *collection.BatchDataSourceResp, err error) {
	// TODO: Your code here...
	return
}

// CreateTask implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) CreateTask(ctx context.Context, req *collection.CreateTaskReq) (resp *collection.TaskResp, err error) {
	// TODO: Your code here...
	return
}

// UpdateTask implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) UpdateTask(ctx context.Context, req *collection.UpdateTaskReq) (resp *collection.TaskResp, err error) {
	// TODO: Your code here...
	return
}

// DeleteTask implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) DeleteTask(ctx context.Context, taskId string) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// GetTask implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) GetTask(ctx context.Context, id string) (resp *collection.TaskResp, err error) {
	// TODO: Your code here...
	return
}

// ListTasks implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) ListTasks(ctx context.Context) (resp *collection.BatchTaskResp, err error) {
	// TODO: Your code here...
	return
}

// ExecuteTask implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) ExecuteTask(ctx context.Context, req *collection.ExecuteTaskReq) (resp *collection.CollectionResultResp, err error) {
	// TODO: Your code here...
	return
}

// StopTask implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) StopTask(ctx context.Context, taskId string) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// GetCollectionResult_ implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) GetCollectionResult_(ctx context.Context, id string) (resp *collection.CollectionResultResp, err error) {
	// TODO: Your code here...
	return
}

// ListCollectionResults implements the DataCollectionServiceImpl interface.
func (s *DataCollectionServiceImpl) ListCollectionResults(ctx context.Context, taskId string) (resp *collection.BatchCollectionResultResp, err error) {
	// TODO: Your code here...
	return
}
