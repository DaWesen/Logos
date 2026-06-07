package dao

import (
	"context"

	"Logos/internal/service/ai/mcp/model"

	"gorm.io/gorm"
)

type MCPRepository interface {
	CreateTool(ctx context.Context, tool *model.Tool) error
	GetTool(ctx context.Context, id string) (*model.Tool, error)
	GetToolByName(ctx context.Context, name string, userID string) (*model.Tool, error)
	ListTools(ctx context.Context, toolType *int, enabledOnly bool, userID string, page, pageSize int) ([]*model.Tool, int64, error)
	UpdateTool(ctx context.Context, tool *model.Tool) error
	DeleteTool(ctx context.Context, id string) error
	CreateCallLog(ctx context.Context, log *model.ToolCallLog) error
}

type mcpRepository struct {
	db *gorm.DB
}

func NewMCPRepository(db *gorm.DB) MCPRepository {
	return &mcpRepository{db: db}
}

func (r *mcpRepository) CreateTool(ctx context.Context, tool *model.Tool) error {
	return r.db.WithContext(ctx).Create(tool).Error
}

func (r *mcpRepository) GetTool(ctx context.Context, id string) (*model.Tool, error) {
	var tool model.Tool
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&tool).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &tool, nil
}

func (r *mcpRepository) GetToolByName(ctx context.Context, name string, userID string) (*model.Tool, error) {
	var tool model.Tool
	query := r.db.WithContext(ctx).Where("name = ?", name)
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	err := query.First(&tool).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &tool, nil
}

func (r *mcpRepository) ListTools(ctx context.Context, toolType *int, enabledOnly bool, userID string, page, pageSize int) ([]*model.Tool, int64, error) {
	var tools []*model.Tool
	var total int64
	query := r.db.WithContext(ctx).Model(&model.Tool{})
	if toolType != nil {
		query = query.Where("type = ?", *toolType)
	}
	if enabledOnly {
		query = query.Where("enabled = ?", true)
	}
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	err := query.Offset(offset).Limit(pageSize).Order("created_at DESC").Find(&tools).Error
	return tools, total, err
}

func (r *mcpRepository) UpdateTool(ctx context.Context, tool *model.Tool) error {
	return r.db.WithContext(ctx).Save(tool).Error
}

func (r *mcpRepository) DeleteTool(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&model.Tool{}).Error
}

func (r *mcpRepository) CreateCallLog(ctx context.Context, log *model.ToolCallLog) error {
	return r.db.WithContext(ctx).Create(log).Error
}

type MCPServiceRepository interface {
	CreateService(ctx context.Context, svc *model.MCPService) error
	GetService(ctx context.Context, id string) (*model.MCPService, error)
	GetServiceByName(ctx context.Context, name string, userID string) (*model.MCPService, error)
	ListServices(ctx context.Context, enabledOnly bool, userID string, page, pageSize int) ([]*model.MCPService, int64, error)
	UpdateService(ctx context.Context, svc *model.MCPService) error
	DeleteService(ctx context.Context, id string) error
}

type mcpServiceRepository struct {
	db *gorm.DB
}

func NewMCPServiceRepository(db *gorm.DB) MCPServiceRepository {
	return &mcpServiceRepository{db: db}
}

func (r *mcpServiceRepository) CreateService(ctx context.Context, svc *model.MCPService) error {
	return r.db.WithContext(ctx).Create(svc).Error
}

func (r *mcpServiceRepository) GetService(ctx context.Context, id string) (*model.MCPService, error) {
	var svc model.MCPService
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&svc).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &svc, nil
}

func (r *mcpServiceRepository) GetServiceByName(ctx context.Context, name string, userID string) (*model.MCPService, error) {
	var svc model.MCPService
	query := r.db.WithContext(ctx).Where("name = ?", name)
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	err := query.First(&svc).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &svc, nil
}

func (r *mcpServiceRepository) ListServices(ctx context.Context, enabledOnly bool, userID string, page, pageSize int) ([]*model.MCPService, int64, error) {
	var services []*model.MCPService
	var total int64
	query := r.db.WithContext(ctx).Model(&model.MCPService{})
	if enabledOnly {
		query = query.Where("enabled = ?", true)
	}
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	err := query.Offset(offset).Limit(pageSize).Order("created_at DESC").Find(&services).Error
	return services, total, err
}

func (r *mcpServiceRepository) UpdateService(ctx context.Context, svc *model.MCPService) error {
	return r.db.WithContext(ctx).Save(svc).Error
}

func (r *mcpServiceRepository) DeleteService(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&model.MCPService{}).Error
}
