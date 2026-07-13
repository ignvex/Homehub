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

// AuthService 认证与用户管理业务服务。
type AuthService struct {
	db *gorm.DB
}

// NewAuthService 创建 AuthService 实例。
func NewAuthService(db *gorm.DB) *AuthService {
	return &AuthService{db: db}
}

// CreateInitialUser 若用户表为空，使用配置中的初始密码创建管理员用户。
//
// 仅在首次启动时调用。若用户已存在则无操作。
// role 通常传入 "admin"，确保第一个用户拥有管理员权限。
func (s *AuthService) CreateInitialUser(username, password, role string) error {
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
		Role:         role,
		IsActive:     true,
	}
	if err := s.db.Create(&user).Error; err != nil {
		return fmt.Errorf("创建初始用户失败: %w", err)
	}

	logger.Info("已创建初始用户", "username", username, "role", role)
	return nil
}

// Login 验证用户名/邮箱和密码，成功时创建 session 并返回 token。
//
// 验证流程：
//  1. 按 username 精确查找，找不到则按 email 查找
//  2. 检查用户是否已启用（IsActive）
//  3. 验证密码哈希
//  4. 创建 session
func (s *AuthService) Login(login, password string) (string, error) {
	var user model.User

	// 先按 username 查找
	err := s.db.Where("username = ?", login).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// username 没找到，尝试 email
		err = s.db.Where("email = ? AND email != ''", login).First(&user).Error
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrAuthFailed
		}
		return "", fmt.Errorf("查询用户失败: %w", err)
	}

	if !user.IsActive {
		return "", ErrUserDisabled
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

	logger.Info("用户登录成功", "username", user.Username)
	return token, nil
}

// Logout 删除指定 token 的会话。
func (s *AuthService) Logout(token string) error {
	result := s.db.Where("token = ?", token).Delete(&model.Session{})
	if result.Error != nil {
		return fmt.Errorf("登出失败: %w", result.Error)
	}
	if len(token) >= 8 {
		logger.Info("用户登出", "token_prefix", token[:8])
	}
	return nil
}

// ChangePassword 修改当前用户的密码。
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

// ============================================================
// 用户管理（管理员操作）
// ============================================================

// ListUsers 返回所有用户列表，按创建时间倒序排列。
//
// 仅管理员可调用。
func (s *AuthService) ListUsers() ([]model.User, error) {
	var users []model.User
	if err := s.db.Order("created_at DESC").Find(&users).Error; err != nil {
		return nil, fmt.Errorf("查询用户列表失败: %w", err)
	}
	return users, nil
}

// GetUserByID 根据 ID 获取用户。
func (s *AuthService) GetUserByID(id uint64) (*model.User, error) {
	var user model.User
	if err := s.db.First(&user, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}
	return &user, nil
}

// CreateUser 管理员创建新用户。
//
// email 为可选参数，用于 Gravatar 头像生成和 email 登录。
func (s *AuthService) CreateUser(username, password, role, email string) (*model.User, error) {
	if username == "" {
		return nil, errors.New("用户名不能为空")
	}
	if password == "" {
		return nil, errors.New("密码不能为空")
	}
	if len(password) < 6 {
		return nil, ErrPasswordTooWeak
	}
	if role != "admin" && role != "user" {
		return nil, errors.New("角色必须为 admin 或 user")
	}

	// 检查用户名是否已存在
	var count int64
	s.db.Model(&model.User{}).Where("username = ?", username).Count(&count)
	if count > 0 {
		return nil, ErrUsernameExists
	}

	// 检查 email 是否已被使用
	if email != "" {
		var emailCount int64
		s.db.Model(&model.User{}).Where("email = ?", email).Count(&emailCount)
		if emailCount > 0 {
			return nil, errors.New("邮箱已被其他用户使用")
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("密码哈希失败: %w", err)
	}

	user := model.User{
		Username:     username,
		PasswordHash: string(hash),
		Email:        email,
		Role:         role,
		IsActive:     true,
	}
	if err := s.db.Create(&user).Error; err != nil {
		return nil, fmt.Errorf("创建用户失败: %w", err)
	}

	logger.Info("管理员创建用户", "username", username, "role", role)
	return &user, nil
}

// UpdateUserRequest 管理员更新用户的请求字段。
type UpdateUserRequest struct {
	Username *string `json:"username,omitempty"`
	Email    *string `json:"email,omitempty"`
	Role     *string `json:"role,omitempty"`
	IsActive *bool   `json:"is_active,omitempty"`
}

// UpdateUser 管理员更新用户信息（用户名、角色、启用状态）。
//
// 限制：
//   - 不能修改自己的角色
//   - 用户名不能与已有用户重复
func (s *AuthService) UpdateUser(targetID uint64, operatorID uint64, req UpdateUserRequest) (*model.User, error) {
	var user model.User
	if err := s.db.First(&user, targetID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}

	updates := map[string]interface{}{}

	if req.Username != nil && *req.Username != "" && *req.Username != user.Username {
		// 检查用户名冲突
		var dupCount int64
		s.db.Model(&model.User{}).Where("username = ? AND id != ?", *req.Username, targetID).Count(&dupCount)
		if dupCount > 0 {
			return nil, ErrUsernameExists
		}
		updates["username"] = *req.Username
	}

	if req.Email != nil {
		if *req.Email != user.Email {
			// 检查邮箱冲突
			if *req.Email != "" {
				var dupCount int64
				s.db.Model(&model.User{}).Where("email = ? AND id != ?", *req.Email, targetID).Count(&dupCount)
				if dupCount > 0 {
					return nil, errors.New("邮箱已被其他用户使用")
				}
			}
			updates["email"] = *req.Email
		}
	}

	if req.Role != nil && *req.Role != "" {
		if *req.Role != "admin" && *req.Role != "user" {
			return nil, errors.New("角色必须为 admin 或 user")
		}
		if targetID == operatorID {
			return nil, ErrSelfOperation
		}
		updates["role"] = *req.Role
	}

	if req.IsActive != nil {
		if targetID == operatorID {
			return nil, ErrSelfOperation
		}
		updates["is_active"] = *req.IsActive
	}

	if len(updates) > 0 {
		if err := s.db.Model(&user).Updates(updates).Error; err != nil {
			return nil, fmt.Errorf("更新用户失败: %w", err)
		}
		// 重新读取以返回最新数据
		s.db.First(&user, targetID)
	}

	logger.Info("管理员更新用户", "target_id", targetID, "operator_id", operatorID)
	return &user, nil
}

// DeleteUser 管理员删除用户。
//
// 会同时清除该用户的所有会话。
// 管理员不能删除自己。
func (s *AuthService) DeleteUser(targetID uint64, operatorID uint64) error {
	if targetID == operatorID {
		return ErrSelfOperation
	}

	if err := s.db.First(&model.User{}, targetID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrUserNotFound
		}
		return fmt.Errorf("查询用户失败: %w", err)
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		// 删除该用户的所有 session
		if err := tx.Where("user_id = ?", targetID).Delete(&model.Session{}).Error; err != nil {
			return fmt.Errorf("清除用户会话失败: %w", err)
		}
		// 删除用户
		if err := tx.Delete(&model.User{}, targetID).Error; err != nil {
			return fmt.Errorf("删除用户失败: %w", err)
		}
		return nil
	})
}

// ResetUserPassword 管理员重置指定用户的密码。
func (s *AuthService) ResetUserPassword(userID uint64, newPassword string) error {
	if len(newPassword) < 6 {
		return ErrPasswordTooWeak
	}

	if err := s.db.First(&model.User{}, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrUserNotFound
		}
		return fmt.Errorf("查询用户失败: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return fmt.Errorf("密码哈希失败: %w", err)
	}

	if err := s.db.Model(&model.User{}).Where("id = ?", userID).Update("password_hash", string(hash)).Error; err != nil {
		return fmt.Errorf("重置密码失败: %w", err)
	}

	// 清除该用户所有 session，强制重新登录
	s.db.Where("user_id = ?", userID).Delete(&model.Session{})

	logger.Info("管理员重置用户密码", "user_id", userID)
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

// 认证与用户管理相关业务错误。
var (
	ErrAuthFailed      = errors.New("用户名或密码错误")
	ErrPasswordTooWeak = errors.New("密码长度不能少于 6 位")
	ErrUserDisabled    = errors.New("账号已被禁用")
	ErrUsernameExists  = errors.New("用户名已存在")
	ErrUserNotFound    = errors.New("用户不存在")
	ErrSelfOperation   = errors.New("不能操作自己的账号")
)
