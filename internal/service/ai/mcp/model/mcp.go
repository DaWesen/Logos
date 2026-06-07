package model

import (
	"time"

	"gorm.io/gorm"
)

type Tool struct {
	ID          string         `gorm:"primaryKey;size:64;comment:工具ID"`
	Name        string         `gorm:"size:128;not null;index;comment:工具名称"`
	Description string         `gorm:"type:text;comment:工具描述"`
	Type        int            `gorm:"not null;comment:工具类型 1-搜索 2-代码执行 3-天气 4-文件系统 5-自定义"`
	Config      string         `gorm:"type:text;comment:配置JSON"`
	Parameters  string         `gorm:"type:text;comment:参数定义JSON"`
	Enabled     bool           `gorm:"not null;default:true;comment:是否启用"`
	UserID      string         `gorm:"index;size:64;comment:所属用户ID"`
	CreatedAt   time.Time      `gorm:"autoCreateTime;comment:创建时间"`
	UpdatedAt   time.Time      `gorm:"autoUpdateTime;comment:更新时间"`
	DeletedAt   gorm.DeletedAt `gorm:"index;comment:删除时间"`
}

func (Tool) TableName() string {
	return "mcp_tools"
}

type ToolCallLog struct {
	ID        string    `gorm:"primaryKey;size:64;comment:调用ID"`
	ToolID    string    `gorm:"size:64;index;not null;comment:工具ID"`
	ToolName  string    `gorm:"size:128;not null;comment:工具名称"`
	Params    string    `gorm:"type:text;comment:调用参数JSON"`
	Result    string    `gorm:"type:text;comment:调用结果"`
	Status    string    `gorm:"size:32;not null;comment:调用状态"`
	CreatedAt time.Time `gorm:"autoCreateTime;comment:创建时间"`
}

func (ToolCallLog) TableName() string {
	return "mcp_tool_call_logs"
}

type MCPService struct {
	ID             string         `gorm:"primaryKey;size:64;comment:服务ID"`
	Name           string         `gorm:"size:128;not null;index;comment:服务名称"`
	Description    string         `gorm:"type:text;comment:服务描述"`
	Enabled        bool           `gorm:"not null;default:true;comment:是否启用"`
	TransportType  string         `gorm:"size:32;not null;comment:传输类型 sse/http-streamable"`
	URL            string         `gorm:"size:512;not null;comment:服务URL"`
	Headers        string         `gorm:"type:text;comment:请求头JSON"`
	AuthConfig     string         `gorm:"type:text;comment:认证配置JSON"`
	AdvancedConfig string         `gorm:"type:text;comment:高级配置JSON"`
	UserID         string         `gorm:"index;size:64;comment:所属用户ID"`
	CreatedAt      time.Time      `gorm:"autoCreateTime;comment:创建时间"`
	UpdatedAt      time.Time      `gorm:"autoUpdateTime;comment:更新时间"`
	DeletedAt      gorm.DeletedAt `gorm:"index;comment:删除时间"`
}

func (MCPService) TableName() string {
	return "mcp_services"
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&Tool{}, &ToolCallLog{}, &MCPService{})
}
