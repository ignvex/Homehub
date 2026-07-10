package model

import (
	"testing"
	"time"

	"gorm.io/gorm"
)

// testModel 用于测试 BaseModel 嵌入行为。
type testModel struct {
	BaseModel
	Name string `gorm:"size:100" json:"name"`
}

// TestBaseModelEmbedding 验证嵌入 BaseModel 后字段可正常访问。
func TestBaseModelEmbedding(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	m := testModel{
		BaseModel: BaseModel{
			ID:        1,
			CreatedAt: now,
			UpdatedAt: now,
			DeletedAt: gorm.DeletedAt{Time: now, Valid: true},
		},
		Name: "test",
	}

	if m.ID != 1 {
		t.Errorf("ID = %d, want 1", m.ID)
	}
	if !m.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt = %v, want %v", m.CreatedAt, now)
	}
	if !m.UpdatedAt.Equal(now) {
		t.Errorf("UpdatedAt = %v, want %v", m.UpdatedAt, now)
	}
	if !m.DeletedAt.Valid || !m.DeletedAt.Time.Equal(now) {
		t.Errorf("DeletedAt = %v, want valid + %v", m.DeletedAt, now)
	}
	if m.Name != "test" {
		t.Errorf("Name = %q, want %q", m.Name, "test")
	}
}

// TestBaseModelZeroValue 验证 BaseModel 零值状态。
func TestBaseModelZeroValue(t *testing.T) {
	m := testModel{Name: "zero"}

	if m.ID != 0 {
		t.Errorf("zero-value ID = %d, want 0", m.ID)
	}
	if m.DeletedAt.Valid {
		t.Error("zero-value DeletedAt.Valid = true, want false")
	}
}
