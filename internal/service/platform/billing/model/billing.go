package model

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// Account 账户模型
type Account struct {
	ID          string         `gorm:"primaryKey;size:36;comment:账户ID" json:"id"`
	UserID      string         `gorm:"index;size:36;not null;comment:用户ID" json:"userId"`
	Balance     float64        `gorm:"not null;default:0;comment:余额" json:"balance"`
	CreditLimit float64        `gorm:"not null;default:0;comment:信用额度" json:"creditLimit"`
	Usage       JSONMap        `gorm:"type:jsonb;comment:用量" json:"usage"`
	CreatedAt   time.Time      `gorm:"autoCreateTime;comment:创建时间" json:"createdAt"`
	UpdatedAt   time.Time      `gorm:"autoUpdateTime;comment:更新时间" json:"updatedAt"`
}

// Transaction 交易记录模型
type Transaction struct {
	ID             string         `gorm:"primaryKey;size:36;comment:交易ID" json:"id"`
	UserID         string         `gorm:"index;size:36;not null;comment:用户ID" json:"userId"`
	AccountID      string         `gorm:"index;size:36;not null;comment:账户ID" json:"accountId"`
	Type           int            `gorm:"size:10;not null;comment:交易类型" json:"type"` // 1=deposit, 2=withdrawal, 3=consume,4=refund
	Item           int            `gorm:"size:10;not null;comment:计费项目" json:"item"` // 1=model_call,2=storage,3=bandwidth,4=embedding
	Amount         float64        `gorm:"not null;comment:金额" json:"amount"`
	BalanceBefore  float64        `gorm:"not null;comment:交易前余额" json:"balanceBefore"`
	BalanceAfter   float64        `gorm:"not null;comment:交易后余额" json:"balanceAfter"`
	Description    string         `gorm:"type:text;comment:描述" json:"description"`
	Status         int            `gorm:"size:10;not null;default:1;comment:状态" json:"status"` // 1=pending, 2=success,3=failed
	Metadata       JSONMap        `gorm:"type:jsonb;comment:元数据" json:"metadata"`
	CreatedAt      time.Time      `gorm:"autoCreateTime;comment:创建时间" json:"createdAt"`
}

// JSONMap JSON映射类型
type JSONMap map[string]string

func (j JSONMap) Value() (driver.Value, error) {
	return json.Marshal(j)
}

func (j *JSONMap) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(bytes, j)
}

// AutoMigrate 自动迁移数据库
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&Account{}, &Transaction{})
}
