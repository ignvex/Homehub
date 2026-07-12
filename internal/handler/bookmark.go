package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/service"
)

// ============================================================
// 书签分组 API
// ============================================================

// BookmarkGroupList 获取所有分组。
//
// @Summary 获取所有书签分组及书签
// @Description 返回所有分组及其下书签的嵌套列表
// @Tags 书签
// @Produce json
// @Success 200 {object} Response{data=[]model.BookmarkGroup} "分组列表"
// @Router /bookmark-groups [get]
func BookmarkGroupList(c *gin.Context) {
	groups, err := svc.Bookmark.ListGroups()
	if err != nil {
		FailInternal(c, "查询分组失败")
		return
	}
	Success(c, groups)
}

// createGroupRequest 创建分组请求体。
type createGroupRequest struct {
	Name string `json:"name" form:"name" binding:"required"`
}

// BookmarkGroupCreate 创建分组。
//
// @Summary 创建书签分组
// @Tags 书签
// @Accept json
// @Produce json
// @Param request body createGroupRequest true "分组名称"
// @Success 200 {object} Response{data=model.BookmarkGroup} "创建成功"
// @Failure 400 {object} Response "参数错误"
// @Router /bookmark-groups [post]
func BookmarkGroupCreate(c *gin.Context) {
	var req createGroupRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "请提供分组名称")
		return
	}

	group, err := svc.Bookmark.CreateGroup(req.Name)
	if err != nil {
		FailBadRequest(c, err.Error())
		return
	}
	Success(c, group)
}

// updateGroupRequest 更新分组请求体。
type updateGroupRequest struct {
	Name     string `json:"name" form:"name" binding:"required"`
	Priority *int   `json:"priority" form:"priority"`
}

// BookmarkGroupUpdate 更新分组名称。
//
// @Summary 更新书签分组名称
// @Tags 书签
// @Accept json
// @Produce json
// @Param id path int true "分组 ID"
// @Param request body updateGroupRequest true "新名称"
// @Success 200 {object} Response "更新成功"
// @Failure 400 {object} Response "参数错误"
// @Failure 404 {object} Response "分组不存在"
// @Router /bookmark-groups/{id} [put]
func BookmarkGroupUpdate(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的分组 ID")
		return
	}

	var req updateGroupRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "请提供分组名称")
		return
	}

	if err := svc.Bookmark.UpdateGroupWithPriority(id, req.Name, req.Priority); err != nil {
		if errors.Is(err, service.ErrGroupNotFound) {
			Fail(c, http.StatusNotFound, CodeGroupNotFound, "分组不存在")
			return
		}
		FailInternal(c, "更新分组失败")
		return
	}
	Success(c, nil)
}

// BookmarkGroupDelete 删除分组及其下所有书签。
//
// @Summary 删除书签分组
// @Description 删除分组及其下所有书签
// @Tags 书签
// @Produce json
// @Param id path int true "分组 ID"
// @Success 200 {object} Response "删除成功"
// @Failure 400 {object} Response "参数错误"
// @Failure 404 {object} Response "分组不存在"
// @Router /bookmark-groups/{id} [delete]
func BookmarkGroupDelete(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的分组 ID")
		return
	}

	if err := svc.Bookmark.DeleteGroup(id); err != nil {
		if errors.Is(err, service.ErrGroupNotFound) {
			Fail(c, http.StatusNotFound, CodeGroupNotFound, "分组不存在")
			return
		}
		FailInternal(c, "删除分组失败")
		return
	}
	Success(c, nil)
}

// reorderGroupsRequest 分组排序请求体。
type reorderGroupsRequest struct {
	IDs []uint64 `json:"ids" form:"ids" binding:"required"`
}

// BookmarkGroupReorder 更新分组排序。
//
// @Summary 调整分组排序
// @Tags 书签
// @Accept json
// @Produce json
// @Param request body reorderGroupsRequest true "分组 ID 列表"
// @Success 200 {object} Response "排序成功"
// @Failure 400 {object} Response "参数错误"
// @Router /bookmark-groups/reorder [post]
func BookmarkGroupReorder(c *gin.Context) {
	var req reorderGroupsRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "请提供分组 ID 列表")
		return
	}

	if err := svc.Bookmark.ReorderGroups(req.IDs); err != nil {
		FailInternal(c, "更新分组排序失败")
		return
	}
	Success(c, nil)
}

// ============================================================
// 书签 API
// ============================================================

// BookmarkList 获取所有书签（按分组嵌套）。
//
// @Summary 获取所有书签
// @Description 按分组嵌套返回所有书签
// @Tags 书签
// @Produce json
// @Success 200 {object} Response{data=[]model.BookmarkGroup} "书签列表"
// @Router /bookmarks [get]
func BookmarkList(c *gin.Context) {
	bookmarks, err := svc.Bookmark.ListBookmarks()
	if err != nil {
		FailInternal(c, "查询书签失败")
		return
	}
	Success(c, bookmarks)
}

// createBookmarkRequest 创建书签请求体。
type createBookmarkRequest struct {
	GroupID  uint64 `json:"group_id" form:"group_id" binding:"required"`
	Title    string `json:"title" form:"title" binding:"required"`
	URL      string `json:"url" form:"url" binding:"required"`
	Icon     string `json:"icon" form:"icon"`
	Priority int    `json:"priority" form:"priority"`
}

// BookmarkCreate 创建书签。
//
// @Summary 创建书签
// @Tags 书签
// @Accept json
// @Produce json
// @Param request body createBookmarkRequest true "书签信息"
// @Success 200 {object} Response{data=model.Bookmark} "创建成功"
// @Failure 400 {object} Response "参数错误"
// @Failure 404 {object} Response "分组不存在"
// @Router /bookmarks [post]
func BookmarkCreate(c *gin.Context) {
	var req createBookmarkRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "请提供 group_id、title 和 url")
		return
	}

	// 自动获取 favicon
	icon := req.Icon
	if icon == "" && svc.Favicon != nil {
		if fetched, err := svc.Favicon.FetchFavicon(req.URL); err == nil && fetched != "" {
			icon = fetched
		}
	}

	bookmark, err := svc.Bookmark.CreateBookmark(req.GroupID, req.Title, req.URL, icon)
	if err != nil {
		if errors.Is(err, service.ErrGroupNotFound) {
			Fail(c, http.StatusNotFound, CodeGroupNotFound, "分组不存在")
			return
		}
		FailBadRequest(c, err.Error())
		return
	}

	// 如果指定了优先级，设置优先级
	if req.Priority > 0 {
		p := req.Priority
		_ = svc.Bookmark.UpdateBookmarkWithPriority(bookmark.ID, "", "", icon, &p)
		bookmark.Priority = p
	}

	Success(c, bookmark)
}

// updateBookmarkRequest 更新书签请求体。
type updateBookmarkRequest struct {
	Title    *string `json:"title" form:"title"`
	URL      *string `json:"url" form:"url"`
	Icon     *string `json:"icon" form:"icon"`
	Priority *int    `json:"priority" form:"priority"`
}

// BookmarkUpdate 更新书签。
//
// @Summary 更新书签
// @Tags 书签
// @Accept json
// @Produce json
// @Param id path int true "书签 ID"
// @Param request body updateBookmarkRequest true "书签字段"
// @Success 200 {object} Response{data=model.Bookmark} "更新成功"
// @Failure 400 {object} Response "参数错误"
// @Failure 404 {object} Response "书签不存在"
// @Router /bookmarks/{id} [put]
func BookmarkUpdate(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的书签 ID")
		return
	}

	var req updateBookmarkRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "无效的请求参数")
		return
	}

	title := ""
	if req.Title != nil {
		title = *req.Title
	}
	url := ""
	if req.URL != nil {
		url = *req.URL
	}
	icon := ""
	if req.Icon != nil {
		icon = *req.Icon
	}

	if err := svc.Bookmark.UpdateBookmarkWithPriority(id, title, url, icon, req.Priority); err != nil {
		if errors.Is(err, service.ErrBookmarkNotFound) {
			Fail(c, http.StatusNotFound, CodeBookmarkNotFound, "书签不存在")
			return
		}
		FailInternal(c, "更新书签失败")
		return
	}
	Success(c, nil)
}

// BookmarkDelete 删除书签。
//
// @Summary 删除书签
// @Tags 书签
// @Produce json
// @Param id path int true "书签 ID"
// @Success 200 {object} Response "删除成功"
// @Failure 400 {object} Response "参数错误"
// @Failure 404 {object} Response "书签不存在"
// @Router /bookmarks/{id} [delete]
func BookmarkDelete(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的书签 ID")
		return
	}

	if err := svc.Bookmark.DeleteBookmark(id); err != nil {
		if errors.Is(err, service.ErrBookmarkNotFound) {
			Fail(c, http.StatusNotFound, CodeBookmarkNotFound, "书签不存在")
			return
		}
		FailInternal(c, "删除书签失败")
		return
	}
	Success(c, nil)
}

// reorderBookmarksRequest 书签排序请求体。
type reorderBookmarksRequest struct {
	GroupID uint64   `json:"group_id" form:"group_id" binding:"required"`
	IDs     []uint64 `json:"ids" form:"ids" binding:"required"`
}

// BookmarkReorder 更新指定分组下书签的排序。
//
// @Summary 调整书签排序
// @Tags 书签
// @Accept json
// @Produce json
// @Param request body reorderBookmarksRequest true "书签 ID 列表"
// @Success 200 {object} Response "排序成功"
// @Failure 400 {object} Response "参数错误"
// @Router /bookmarks/reorder [post]
func BookmarkReorder(c *gin.Context) {
	var req reorderBookmarksRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "请提供 group_id 和 ids")
		return
	}

	if err := svc.Bookmark.ReorderBookmarks(req.GroupID, req.IDs); err != nil {
		FailInternal(c, "更新书签排序失败")
		return
	}
	Success(c, nil)
}
