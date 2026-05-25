package handler

import (
	"context"
	"encoding/json"
	"time"

	"Logos/internal/service/ai/moderation/model"
	"Logos/internal/service/ai/moderation/service"
	"Logos/pkg/logger"
	pb "Logos/proto_gen/moderation"

	"google.golang.org/protobuf/types/known/timestamppb"
)

type ModerationServiceImpl struct {
	pb.UnimplementedModerationServiceServer
	ModerationService service.ModerationService
}

func protoToModelConfig(pbCfg *pb.ModelConfig) *service.ModelConfig {
	if pbCfg == nil {
		return nil
	}
	if pbCfg.ApiKey == "" || pbCfg.Model == "" {
		return nil
	}
	return &service.ModelConfig{
		Provider:    pbCfg.Provider,
		Model:       pbCfg.Model,
		ApiKey:      pbCfg.ApiKey,
		BaseUrl:     pbCfg.BaseUrl,
		Temperature: pbCfg.Temperature,
	}
}

func (s *ModerationServiceImpl) Translate(ctx context.Context, req *pb.TranslateRequest) (*pb.TranslateResponse, error) {
	resp := &pb.TranslateResponse{}

	cfg := protoToModelConfig(req.ModelConfig)

	record, err := s.ModerationService.Translate(ctx, req.Content, req.SourceLang, req.TargetLang, req.ContentId, cfg)
	if err != nil {
		logger.Error("翻译失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.TranslatedContent = record.TranslatedContent
	resp.SourceLang = record.SourceLang
	resp.TargetLang = record.TargetLang
	return resp, nil
}

func (s *ModerationServiceImpl) ModerateContent(ctx context.Context, req *pb.ModerateContentRequest) (*pb.ModerateContentResponse, error) {
	resp := &pb.ModerateContentResponse{}

	cfg := protoToModelConfig(req.ModelConfig)

	record, err := s.ModerationService.ModerateContent(ctx, req.Content, req.ContentId, req.ContentType, cfg)
	if err != nil {
		logger.Error("审核失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Data = convertModelRecordToProtoRecord(record)
	return resp, nil
}

func (s *ModerationServiceImpl) GetModerationRecords(ctx context.Context, req *pb.GetModerationRecordsRequest) (*pb.GetModerationRecordsResponse, error) {
	resp := &pb.GetModerationRecordsResponse{}

	var result string
	if req.Result != pb.ModerationResult_MODERATION_RESULT_UNSPECIFIED {
		result = req.Result.String()
	}

	var startTime, endTime *time.Time
	if req.StartTime != nil {
		t := req.StartTime.AsTime()
		startTime = &t
	}
	if req.EndTime != nil {
		t := req.EndTime.AsTime()
		endTime = &t
	}

	page := int(req.Page)
	if page <= 0 {
		page = 1
	}
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = 20
	}

	records, total, err := s.ModerationService.GetModerationRecords(ctx, result, startTime, endTime, page, pageSize)
	if err != nil {
		logger.Error("获取审核记录失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Total = int32(total)
	for _, record := range records {
		resp.Records = append(resp.Records, convertModelRecordToProtoRecord(record))
	}
	return resp, nil
}

func convertModelRecordToProtoRecord(record *model.ModerationRecord) *pb.ModerationRecord {
	if record == nil {
		return nil
	}

	var categories []pb.ModerationCategory
	if record.Categories != "" {
		var cats []string
		json.Unmarshal([]byte(record.Categories), &cats)
		for _, c := range cats {
			if v, ok := pb.ModerationCategory_value[c]; ok {
				categories = append(categories, pb.ModerationCategory(v))
			}
		}
	}

	var scores map[string]float32
	if record.Scores != "" {
		var rawScores map[string]float64
		json.Unmarshal([]byte(record.Scores), &rawScores)
		scores = make(map[string]float32)
		for k, v := range rawScores {
			scores[k] = float32(v)
		}
	}

	return &pb.ModerationRecord{
		Id:          record.ID,
		Content:     record.Content,
		Result:      pb.ModerationResult(pb.ModerationResult_value[record.Result]),
		Categories:  categories,
		Scores:      scores,
		ActionTaken: record.ActionTaken,
		ModeratorId: record.ModeratorID,
		CreatedAt:   timestamppb.New(record.CreatedAt),
	}
}
