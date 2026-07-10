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
// GET /api/v1/bookmark-groups
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
	Name string `json:"name" binding:"required"`
}

// BookmarkGroupCreate 创建分组。
//
// POST /api/v1/bookmark-groups
func BookmarkGroupCreate(c *gin.Context) {
	var req createGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
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
	Name string `json:"name" binding:"required"`
}

// BookmarkGroupUpdate 更新分组名称。
//
// PUT /api/v1/bookmark-groups/:id
func BookmarkGroupUpdate(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的分组 ID")
		return
	}

	var req updateGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailBadRequest(c, "请提供分组名称")
		return
	}

	if err := svc.Bookmark.UpdateGroup(id, req.Name); err != nil {
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
// DELETE /api/v1/bookmark-groups/:id
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
	IDs []uint64 `json:"ids" binding:"required"`
}

// BookmarkGroupReorder 更新分组排序。
//
// POST /api/v1/bookmark-groups/reorder
func BookmarkGroupReorder(c *gin.Context) {
	var req reorderGroupsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
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
// GET /api/v1/bookmarks
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
	GroupID uint64 `json:"group_id" binding:"required"`
	Title   string `json:"title" binding:"required"`
	URL     string `json:"url" binding:"required"`
	Icon    string `json:"icon"`
}

// BookmarkCreate 创建书签。
//
// POST /api/v1/bookmarks
func BookmarkCreate(c *gin.Context) {
	var req createBookmarkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailBadRequest(c, "请提供 group_id、title 和 url")
		return
	}

	bookmark, err := svc.Bookmark.CreateBookmark(req.GroupID, req.Title, req.URL, req.Icon)
	if err != nil {
		if errors.Is(err, service.ErrGroupNotFound) {
			Fail(c, http.StatusNotFound, CodeGroupNotFound, "分组不存在")
			return
		}
		FailBadRequest(c, err.Error())
		return
	}
	Success(c, bookmark)
}

// updateBookmarkRequest 更新书签请求体。
type updateBookmarkRequest struct {
	Title *string `json:"title"`
	URL   *string `json:"url"`
	Icon  *string `json:"icon"`
}

// BookmarkUpdate 更新书签。
//
// PUT /api/v1/bookmarks/:id
func BookmarkUpdate(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的书签 ID")
		return
	}

	var req updateBookmarkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
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

	if err := svc.Bookmark.UpdateBookmark(id, title, url, icon); err != nil {
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
// DELETE /api/v1/bookmarks/:id
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
	GroupID uint64   `json:"group_id" binding:"required"`
	IDs     []uint64 `json:"ids" binding:"required"`
}

// BookmarkReorder 更新指定分组下书签的排序。
//
// POST /api/v1/bookmarks/reorder
func BookmarkReorder(c *gin.Context) {
	var req reorderBookmarksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailBadRequest(c, "请提供 group_id 和 ids")
		return
	}

	if err := svc.Bookmark.ReorderBookmarks(req.GroupID, req.IDs); err != nil {
		FailInternal(c, "更新书签排序失败")
		return
	}
	Success(c, nil)
}
