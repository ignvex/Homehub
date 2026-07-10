package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/ignvex/HomeHub/internal/logger"
	"github.com/ignvex/HomeHub/internal/model"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// 会话相关常量。
const (
	sessionTokenLength = 32                 // session token 原始字节数（hex 编码后 ×2）
	sessionDuration    = 7 * 24 * time.Hour // 会话有效期
	bcryptCost         = bcrypt.DefaultCost // bcrypt 哈希强度
)

// AuthService 认证业务服务。
type AuthService struct {
	db *gorm.DB
}

// NewAuthService 创建 AuthService 实例。
func NewAuthService(db *gorm.DB) *AuthService {
	return &AuthService{db: db}
}

// CreateInitialUser 若用户表为空，使用初始密码创建默认用户。
//
// 仅在首次启动时调用。若用户已存在则无操作。
func (s *AuthService) CreateInitialUser(username, password string) error {
	var count int64
	if err := s.db.Model(&model.User{}).Count(&count).Error; err != nil {
		return fmt.Errorf("查询用户数失败: %w", err)
	}
	if count > 0 {
		return nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return fmt.Errorf("密码哈希失败: %w", err)
	}

	user := model.User{
		Username:     username,
		PasswordHash: string(hash),
	}
	if err := s.db.Create(&user).Error; err != nil {
		return fmt.Errorf("创建初始用户失败: %w", err)
	}

	logger.Info("已创建初始用户", "username", username)
	return nil
}

// Login 验证用户名密码，成功时创建 session 并返回 token。
func (s *AuthService) Login(username, password string) (string, error) {
	var user model.User
	if err := s.db.Where("username = ?", username).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrAuthFailed
		}
		return "", fmt.Errorf("查询用户失败: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return "", ErrAuthFailed
	}

	token, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("生成 token 失败: %w", err)
	}

	session := model.Session{
		UserID:    user.ID,
		Token:     token,
		ExpiresAt: time.Now().Add(sessionDuration),
	}
	if err := s.db.Create(&session).Error; err != nil {
		return "", fmt.Errorf("创建会话失败: %w", err)
	}

	logger.Info("用户登录成功", "username", username)
	return token, nil
}

// Logout 删除指定 token 的会话。
func (s *AuthService) Logout(token string) error {
	result := s.db.Where("token = ?", token).Delete(&model.Session{})
	if result.Error != nil {
		return fmt.Errorf("登出失败: %w", result.Error)
	}
	logger.Info("用户登出", "token_prefix", token[:8])
	return nil
}

// ChangePassword 修改用户密码。
//
// 需验证旧密码是否正确。
func (s *AuthService) ChangePassword(userID uint64, oldPassword, newPassword string) error {
	var user model.User
	if err := s.db.First(&user, userID).Error; err != nil {
		return fmt.Errorf("用户不存在: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(oldPassword)); err != nil {
		return ErrAuthFailed
	}

	if len(newPassword) < 6 {
		return ErrPasswordTooWeak
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return fmt.Errorf("密码哈希失败: %w", err)
	}

	if err := s.db.Model(&user).Update("password_hash", string(hash)).Error; err != nil {
		return fmt.Errorf("更新密码失败: %w", err)
	}

	// 修改密码后清除所有 session，强制重新登录
	s.db.Where("user_id = ?", userID).Delete(&model.Session{})

	logger.Info("密码已修改", "user_id", userID)
	return nil
}

// generateToken 生成加密安全的随机 hex token。
func generateToken() (string, error) {
	b := make([]byte, sessionTokenLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// 认证相关业务错误。
var (
	ErrAuthFailed      = errors.New("用户名或密码错误")
	ErrPasswordTooWeak = errors.New("密码长度不能少于 6 位")
)
