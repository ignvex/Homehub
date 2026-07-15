package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/ignvex/HomeHub/internal/config"
)

// ============================================================
// 请求类型
// ============================================================

type createPasteRequest struct {
	Content string `json:"content" binding:"required"`
}

type pasteResponse struct {
	ID          string `json:"id"`
	Content     string `json:"content"`
	CreatedAt   string `json:"created_at"`
	ExpiresAt   string `json:"expires_at"`
	DeleteToken string `json:"delete_token,omitempty"` // 仅创建时返回
}

// ============================================================
// Pastebin API（全部公开，无需认证）
// ============================================================

// pasteSummary 列表响应（不含 content 完整内容和 delete_token）。
type pasteSummary struct {
	ID             string `json:"id"`
	ContentPreview string `json:"content_preview"`
	CreatedAt      string `json:"created_at"`
	ExpiresAt      string `json:"expires_at"`
}

// PasteList 返回最近粘贴的摘要列表。
//
//	@Summary		获取粘贴列表
//	@Description	返回最近 50 条未过期粘贴的摘要（不含完整内容和删除密钥）。无需认证。
//	@Tags			剪贴板
//	@Produce		json
//	@Success		200	{array}		pasteSummary	"粘贴摘要列表"
//	@Router			/pastes [get]
func PasteList(c *gin.Context) {
	if svc.Paste == nil {
		FailInternal(c, "剪贴板服务未初始化")
		return
	}
	pastes, err := svc.Paste.ListPastes()
	if err != nil {
		FailInternal(c, "获取粘贴列表失败")
		return
	}
	result := make([]pasteSummary, len(pastes))
	for i, p := range pastes {
		preview := p.Content
		if len(preview) > 100 {
			preview = preview[:100]
		}
		result[i] = pasteSummary{
			ID:             p.PublicID,
			ContentPreview: preview,
			CreatedAt:      p.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			ExpiresAt:      p.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
		}
	}
	Success(c, result)
}

// PasteCreate 创建一条新的粘贴。
//
//	@Summary		创建粘贴
//	@Description	上传文本内容，返回公开 ID 和删除密钥。无需认证。
//	@Tags			剪贴板
//	@Accept			json
//	@Produce		json
//	@Param			request	body		createPasteRequest	true	"粘贴内容"
//	@Success		200		{object}	Response{data=pasteResponse}	"创建成功"
//	@Failure		400		{object}	Response						"内容为空或超长"
//	@Failure		500		{object}	Response						"服务内部错误"
//	@Router			/pastes [post]
func PasteCreate(c *gin.Context) {
	if svc.Paste == nil {
		FailInternal(c, "剪贴板服务未初始化")
		return
	}

	var req createPasteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailBadRequest(c, "请提供 content 字段")
		return
	}

	if req.Content == "" {
		FailBadRequest(c, "内容不能为空")
		return
	}

	cfg := config.Get()
	if cfg != nil && cfg.Pastebin.MaxContentLength > 0 {
		if len(req.Content) > cfg.Pastebin.MaxContentLength {
			FailBadRequest(c, "内容超过最大长度限制")
			return
		}
	}

	paste, token, err := svc.Paste.CreatePaste(req.Content)
	if err != nil {
		FailInternal(c, "创建粘贴失败")
		return
	}

	Success(c, pasteResponse{
		ID:          paste.PublicID,
		Content:     paste.Content,
		CreatedAt:   paste.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		ExpiresAt:   paste.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
		DeleteToken: token,
	})
}

// PasteGet 通过公开 ID 获取粘贴内容。
//
//	@Summary		获取粘贴
//	@Description	根据公开 ID 获取粘贴内容。已过期的粘贴返回 404。无需认证。
//	@Tags			剪贴板
//	@Produce		json
//	@Param			id	path		string	true	"公开 ID"
//	@Success		200	{object}	Response{data=pasteResponse}	"粘贴内容"
//	@Failure		404	{object}	Response						"不存在或已过期"
//	@Router			/pastes/{id} [get]
func PasteGet(c *gin.Context) {
	if svc.Paste == nil {
		FailInternal(c, "剪贴板服务未初始化")
		return
	}

	id := c.Param("id")
	if id == "" {
		FailBadRequest(c, "缺少 id 参数")
		return
	}

	paste, err := svc.Paste.GetPaste(id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, Response{
				Code:    CodeNotFound,
				Message: "粘贴不存在或已过期",
			})
			return
		}
		FailInternal(c, "获取粘贴失败")
		return
	}

	Success(c, pasteResponse{
		ID:        paste.PublicID,
		Content:   paste.Content,
		CreatedAt: paste.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		ExpiresAt: paste.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

// PasteDelete 通过公开 ID 删除粘贴。
//
//	@Summary		删除粘贴
//	@Description	根据公开 ID 删除粘贴。无需认证，无需密钥。
//	@Tags			剪贴板
//	@Produce		json
//	@Param			id	path	string	true	"公开 ID"
//	@Success		200	{object}	Response	"删除成功"
//	@Failure		404	{object}	Response	"不存在"
//	@Router			/pastes/{id} [delete]
func PasteDelete(c *gin.Context) {
	if svc.Paste == nil {
		FailInternal(c, "剪贴板服务未初始化")
		return
	}

	id := c.Param("id")
	if id == "" {
		FailBadRequest(c, "缺少 id 参数")
		return
	}

	if err := svc.Paste.DeletePaste(id); err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, Response{
				Code:    CodeNotFound,
				Message: "粘贴不存在或 token 不匹配",
			})
			return
		}
		FailInternal(c, "删除粘贴失败")
		return
	}

	Success(c, gin.H{"deleted": true})
}
