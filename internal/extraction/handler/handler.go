package handler

import (
	common "Noah/kitex_gen/common"
	extraction "Noah/kitex_gen/extraction"
	"context"
)

// KnowledgeExtractionServiceImpl implements the last service interface defined in the IDL.
type KnowledgeExtractionServiceImpl struct{}

// CreateTask implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) CreateTask(ctx context.Context, req *extraction.CreateExtractionTaskReq) (resp *extraction.ExtractionTaskResp, err error) {
	// TODO: Your code here...
	return
}

// UpdateTask implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) UpdateTask(ctx context.Context, req *extraction.UpdateExtractionTaskReq) (resp *extraction.ExtractionTaskResp, err error) {
	// TODO: Your code here...
	return
}

// DeleteTask implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) DeleteTask(ctx context.Context, taskId string) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// GetTask implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) GetTask(ctx context.Context, id string) (resp *extraction.ExtractionTaskResp, err error) {
	// TODO: Your code here...
	return
}

// ListTasks implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) ListTasks(ctx context.Context) (resp *extraction.BatchExtractionTaskResp, err error) {
	// TODO: Your code here...
	return
}

// ExecuteTask implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) ExecuteTask(ctx context.Context, req *extraction.ExecuteExtractionTaskReq) (resp *extraction.ExtractionResultResp, err error) {
	// TODO: Your code here...
	return
}

// CancelTask implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) CancelTask(ctx context.Context, taskId string) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// GetExtractionResult_ implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) GetExtractionResult_(ctx context.Context, id string) (resp *extraction.ExtractionResultResp, err error) {
	// TODO: Your code here...
	return
}

// ListExtractionResults implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) ListExtractionResults(ctx context.Context, taskId string) (resp *extraction.BatchExtractionResultResp, err error) {
	// TODO: Your code here...
	return
}

// ExtractFromText implements the KnowledgeExtractionServiceImpl interface.
func (s *KnowledgeExtractionServiceImpl) ExtractFromText(ctx context.Context, req *extraction.TextExtractionReq) (resp *extraction.TextExtractionResp, err error) {
	// TODO: Your code here...
	return
}
