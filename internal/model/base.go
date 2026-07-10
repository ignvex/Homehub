// Package model 提供 HomeHub 的数据模型定义。
//
// 所有持久化模型嵌入 BaseModel，获得主键、时间戳和软删除支持。
// 模型对应 PostgreSQL 数据库表，由 GORM AutoMigrate 管理 DDL。
package model

import (
	"time"

	"gorm.io/gorm"
)

// BaseModel 是所有数据库模型的公共基类。
//
// 嵌入此结构体后，模型自动获得：
//   - ID：自增主键（uint64）
//   - CreatedAt：记录创建时间（GORM 自动管理）
//   - UpdatedAt：记录更新时间（GORM 自动管理）
//   - DeletedAt：软删除标记（gorm.DeletedAt，NULL 表示未删除）
type BaseModel struct {
	ID        uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}
