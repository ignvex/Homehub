package model

import "time"

// LogEntry 日志条目模型，对应 log_entries 表。
//
// 每条记录存储一次日志事件的关键信息：
//   - Level：日志级别（DEBUG / INFO / WARN / ERROR）
//   - Message：日志消息正文
//   - Source：调用源文件名（由 slog source 属性提供）
//   - Fields：附加字段，以 JSON 字符串存储
//   - LogTime：日志事件实际发生时间（由 slog.Record.Time 提供）
//
// CreatedAt 仍由 GORM 自动管理，表示记录写入数据库的时间。
type LogEntry struct {
	BaseModel
	Level   string    `gorm:"size:10;not null;index" json:"level"`
	Message string    `gorm:"size:2048;not null" json:"message"`
	Source  string    `gorm:"size:255" json:"source,omitempty"`
	Fields  string    `gorm:"type:text" json:"fields,omitempty"`
	LogTime time.Time `gorm:"not null;index" json:"log_time"`
}

// TableName 指定表名为 log_entries。
func (LogEntry) TableName() string {
	return "homehub_log_entries"
}
