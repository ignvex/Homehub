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

// TestGravatarHash 验证邮箱的 Gravatar MD5 哈希计算。
func TestGravatarHash(t *testing.T) {
	tests := []struct {
		name     string
		email    string
		wantHash string
	}{
		{
			name:     "standard email",
			email:    "MyEmailAddress@example.com",
			wantHash: "0bc83cb571cd1c50ba6f3e8a78ef1346",
		},
		{
			name:     "email with spaces",
			email:    "  MyEmailAddress@example.com  ",
			wantHash: "0bc83cb571cd1c50ba6f3e8a78ef1346",
		},
		{
			name:     "empty email",
			email:    "",
			wantHash: "",
		},
		{
			name:     "lowercase gmail",
			email:    "test@gmail.com",
			wantHash: "1aedb8d9dc4751e229a335e371db8058",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := User{Email: tt.email}
			got := u.GravatarHash()
			if got != tt.wantHash {
				t.Errorf("GravatarHash(%q) = %q, want %q", tt.email, got, tt.wantHash)
			}
		})
	}
}

// TestGravatarURL 验证 Gravatar URL 生成。
func TestGravatarURL(t *testing.T) {
	u := User{Email: "MyEmailAddress@example.com"}
	url := u.GravatarURL(200, "identicon", "g")
	want := "https://www.gravatar.com/avatar/0bc83cb571cd1c50ba6f3e8a78ef1346?s=200&d=identicon&r=g"
	if url != want {
		t.Errorf("GravatarURL() = %q, want %q", url, want)
	}
}

// TestGravatarURLEmptyEmail 验证空邮箱返回空 URL。
func TestGravatarURLEmptyEmail(t *testing.T) {
	u := User{Email: ""}
	url := u.GravatarURL(200, "identicon", "g")
	if url != "" {
		t.Errorf("GravatarURL with empty email = %q, want empty string", url)
	}
}
