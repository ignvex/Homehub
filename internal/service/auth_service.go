package service

import (
	"context"
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
// 初始用户固定为 LevelAdmin（1 级），这是唯一的提权入口，不接受外部指定。
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
		Level:        model.LevelAdmin,
		IsActive:     true,
	}
	if err := s.db.Create(&user).Error; err != nil {
		return fmt.Errorf("创建初始用户失败: %w", err)
	}

	logger.Info("已创建初始用户", "username", username, "level", model.LevelAdmin)
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

// Directory 返回启用中用户的 {id, username}，按用户名排序。
//
// 登录用户都能调用：只有这两个字段，不含邮箱与等级。
func (s *AuthService) Directory() ([]model.UserBrief, error) {
	users := []model.UserBrief{}
	if err := s.db.Model(&model.User{}).Select("id", "username").
		Where("is_active = ?", true).
		Order("username ASC").
		Scan(&users).Error; err != nil {
		return nil, fmt.Errorf("查询用户目录失败: %w", err)
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
// CreateUser 创建新用户。
//
// operator 是发起创建的人，用于防止提权：只能创建**等级低于自己**
// （数值更大）的账号。少了这条，2 级运维就能造一个 1 级管理员，
// 等于给所有中间等级开了一个直接提权的后门。
func (s *AuthService) CreateUser(operator *model.User, username, password string, level int, email string) (*model.User, error) {
	if username == "" {
		return nil, errors.New("用户名不能为空")
	}
	if password == "" {
		return nil, errors.New("密码不能为空")
	}
	if len(password) < 6 {
		return nil, ErrPasswordTooWeak
	}
	if !model.IsValidLevel(level) {
		return nil, fmt.Errorf("权限等级必须在 %d–%d 之间", model.LevelMin, model.LevelMax)
	}
	if operator == nil {
		return nil, ErrForbidden
	}
	// 只能建比自己低的等级；同级也不行，否则 2 级之间可以互相复制账号。
	if level <= operator.Level {
		return nil, fmt.Errorf("不能创建等级 %d 的用户（只能创建低于自己等级 %d 的账号）",
			level, operator.Level)
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
		Level:        level,
		IsActive:     true,
	}
	if err := s.db.Create(&user).Error; err != nil {
		return nil, fmt.Errorf("创建用户失败: %w", err)
	}

	logger.Info("创建用户", "username", username, "level", level, "operator_id", operator.ID)
	return &user, nil
}

// UpdateUserRequest 更新用户的请求字段。
type UpdateUserRequest struct {
	Username *string `json:"username,omitempty"`
	Email    *string `json:"email,omitempty"`
	// Level 权限等级 1–5；Role 是兼容字段，只有 level 缺失时才会被采用。
	Level    *int    `json:"level,omitempty"`
	Role     *string `json:"role,omitempty"`
	IsActive *bool   `json:"is_active,omitempty"`
}

// UpdateUser 更新用户信息（用户名、等级、启用状态）。
//
// 权限约束集中在开头几行，全部是为了防止“通过改用户来提权”：
//   - 操作者必须严格高于目标等级，否则 2 级可以改 1 级；
//   - 新等级必须严格低于操作者，否则 2 级可以把自己人提到 1 级；
//   - 任何人不能改自己的等级（自我提权）。
func (s *AuthService) UpdateUser(targetID uint64, operator *model.User, req UpdateUserRequest) (*model.User, error) {
	if operator == nil {
		return nil, ErrForbidden
	}

	var user model.User
	if err := s.db.First(&user, targetID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}

	// 能不能动这个账号：必须严格高于对方，且不是自己。
	if targetID == operator.ID {
		// 自己可以改用户名/邮箱，但下面单独拦等级与启用状态。
	} else if !operator.CanManage(&user) {
		return nil, fmt.Errorf("不能操作等级 %d 的用户（你的等级是 %d）", user.Level, operator.Level)
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

	// 等级变更：level 优先，缺省时回退到 role 字符串（兼容旧前端）。
	if newLevel, ok := resolveLevel(req); ok {
		if targetID == operator.ID {
			return nil, ErrSelfOperation
		}
		if newLevel <= operator.Level {
			return nil, fmt.Errorf("不能把用户提升到 %d 级（只能设置低于自己等级 %d 的账号）",
				newLevel, operator.Level)
		}
		updates["level"] = newLevel
	}

	if req.IsActive != nil {
		if targetID == operator.ID {
			return nil, ErrSelfOperation
		}
		updates["is_active"] = *req.IsActive

		// 停用必须同时失效该用户的全部会话。
		//
		// 只改 is_active 是不够的：登录态校验发生在 AuthRequired，那里
		// 用的是已有的 session 行，不会重新判断账号是否启用。停用后不删
		// session，被停用的人（包括被降权的管理员）仍能继续访问全部接口
		// 直到会话自然过期（长达数天）。
		if !*req.IsActive {
			if err := s.db.Unscoped().
				Where("user_id = ?", targetID).
				Delete(&model.Session{}).Error; err != nil {
				return nil, fmt.Errorf("停用用户时清理会话失败: %w", err)
			}
		}
	}

	if len(updates) > 0 {
		if err := s.db.Model(&user).Updates(updates).Error; err != nil {
			return nil, fmt.Errorf("更新用户失败: %w", err)
		}
		// 重新读取以返回最新数据
		s.db.First(&user, targetID)
	}

	logger.Info("更新用户", "target_id", targetID, "operator_id", operator.ID)
	return &user, nil
}

// DeleteUser 删除用户。
//
// 会同时清除该用户的所有会话与头像缓存。
// 头像缓存容易漏：它是每行上百 KB 的 bytea，用户删了却留着缓存，
// 等于永久占着最占空间的一张表。
//
// 权限约束与 UpdateUser 一致：必须严格高于目标等级，且不能删自己。
func (s *AuthService) DeleteUser(targetID uint64, operator *model.User) error {
	if operator == nil {
		return ErrForbidden
	}
	if targetID == operator.ID {
		return ErrSelfOperation
	}

	var target model.User
	if err := s.db.First(&target, targetID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrUserNotFound
		}
		return fmt.Errorf("查询用户失败: %w", err)
	}

	if !operator.CanManage(&target) {
		return fmt.Errorf("不能删除等级 %d 的用户（你的等级是 %d）", target.Level, operator.Level)
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		// 删除该用户的所有 session
		if err := tx.Unscoped().Where("user_id = ?", targetID).Delete(&model.Session{}).Error; err != nil {
			return fmt.Errorf("清除用户会话失败: %w", err)
		}
		// 删除头像缓存（bytea 大字段，不清理会永久残留）
		if err := tx.Unscoped().Where("user_id = ?", targetID).Delete(&model.AvatarCache{}).Error; err != nil {
			return fmt.Errorf("清除用户头像缓存失败: %w", err)
		}
		// 删除 RSS 阅读状态（已读/星标是个人数据，没有外键，不删会永久残留）
		if err := tx.Where("user_id = ?", targetID).Delete(&model.RSSEntryState{}).Error; err != nil {
			return fmt.Errorf("清除用户阅读状态失败: %w", err)
		}
		// 待办的创建者/被指派者置空。置空后的旧待办只有 3 级及以上能动，
		// 这是有意的：归属消失了，就不再有「自己的」。
		if err := tx.Model(&model.Todo{}).Where("created_by = ?", targetID).Update("created_by", nil).Error; err != nil {
			return fmt.Errorf("清除待办创建者失败: %w", err)
		}
		if err := tx.Model(&model.Todo{}).Where("assignee_id = ?", targetID).Update("assignee_id", nil).Error; err != nil {
			return fmt.Errorf("清除待办指派失败: %w", err)
		}
		// 删除用户
		if err := tx.Delete(&model.User{}, targetID).Error; err != nil {
			return fmt.Errorf("删除用户失败: %w", err)
		}
		return nil
	})
}

// ResetUserPassword 重置指定用户的密码。
//
// 权限约束与更新/删除一致：必须严格高于目标等级，且不能重置自己
// （改自己的密码走 ChangePassword，需要验证旧密码 —— 否则任何一个
// “能管自己”的路径都等于免密改密）。
func (s *AuthService) ResetUserPassword(userID uint64, operator *model.User, newPassword string) error {
	if operator == nil {
		return ErrForbidden
	}
	if len(newPassword) < 6 {
		return ErrPasswordTooWeak
	}
	if userID == operator.ID {
		return ErrSelfOperation
	}

	var target model.User
	if err := s.db.First(&target, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrUserNotFound
		}
		return fmt.Errorf("查询用户失败: %w", err)
	}

	if !operator.CanManage(&target) {
		return fmt.Errorf("不能重置等级 %d 用户的密码（你的等级是 %d）", target.Level, operator.Level)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return fmt.Errorf("密码哈希失败: %w", err)
	}

	if err := s.db.Model(&model.User{}).Where("id = ?", userID).Update("password_hash", string(hash)).Error; err != nil {
		return fmt.Errorf("重置密码失败: %w", err)
	}

	// 清除该用户所有 session，强制重新登录（物理删除，避免表膨胀）
	s.db.Unscoped().Where("user_id = ?", userID).Delete(&model.Session{})

	logger.Info("重置用户密码", "user_id", userID, "operator_id", operator.ID)
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

// CleanupExpiredSessions 后台 goroutine，定期清理过期的数据库会话。
//
// 每小时执行一次 `DELETE FROM sessions WHERE expires_at < now()`，
// 防止 sessions 表无限增长。通过 ctx 控制生命周期。
//
// 必须用 Unscoped：Session 嵌入 BaseModel 因而带 gorm.DeletedAt，
// 不加 Unscoped 的 Delete 只会写 deleted_at，行永远不会被回收。
// 那不是"软删除保留审计痕迹"，而是纯粹的泄漏——过期的会话标记毫无
// 价值，它们只会让表和索引持续变大，而清理查询本身也越跑越慢。
func CleanupExpiredSessions(ctx context.Context, db *gorm.DB) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			result := db.Unscoped().Where("expires_at < ?", time.Now()).Delete(&model.Session{})
			if result.Error != nil {
				logger.Warn("定期清理过期会话失败", "error", result.Error)
			} else if result.RowsAffected > 0 {
				logger.Info("已清理过期会话", "count", result.RowsAffected)
			}
		}
	}
}

// 认证与用户管理相关业务错误。
var (
	ErrAuthFailed      = errors.New("用户名或密码错误")
	ErrPasswordTooWeak = errors.New("密码长度不能少于 6 位")
	ErrUserDisabled    = errors.New("账号已被禁用")
	ErrUsernameExists  = errors.New("用户名已存在")
	ErrUserNotFound    = errors.New("用户不存在")
	ErrSelfOperation   = errors.New("不能操作自己的账号")
	// ErrForbidden 表示权限不足。与校验失败的 ErrAuthFailed 区分开：
	// 前者是 403，后者是 401，前端处理方式不同。
	ErrForbidden = errors.New("权限不足")
)

// resolveLevel 从请求里解出目标等级。
//
// Level 优先，缺省时回退到 Role 字符串（兼容按 "admin"/"user" 传值的老客户端）。
// 第二个返回值表示“调用方确实想改等级”——用它区分“没传”和“传了 0”。
func resolveLevel(req UpdateUserRequest) (int, bool) {
	if req.Level != nil {
		return model.NormalizeLevel(*req.Level), true
	}
	if req.Role != nil && *req.Role != "" {
		switch *req.Role {
		case "admin":
			return model.LevelAdmin, true
		case "user":
			return model.LevelViewer, true
		default:
			return model.LevelFromRole(*req.Role), true
		}
	}
	return 0, false
}
