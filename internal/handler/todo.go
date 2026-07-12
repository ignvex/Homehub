package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/model"
)

// ============================================================
// TODO 待办事项 API
// ============================================================

// createTodoRequest 创建 TODO 请求体。
type createTodoRequest struct {
	Title       string     `json:"title" form:"title" binding:"required"`
	Description string     `json:"description" form:"description"`
	Priority    int        `json:"priority" form:"priority"`
	DueDate     *time.Time `json:"due_date" form:"due_date"`
}

// updateTodoRequest 更新 TODO 请求体（全部可选）。
type updateTodoRequest struct {
	Title       *string    `json:"title"`
	Description *string    `json:"description"`
	Status      *string    `json:"status"`
	Priority    *int       `json:"priority"`
	DueDate     *time.Time `json:"due_date"`
}

// updateTodoStatusRequest 快捷修改状态请求体。
type updateTodoStatusRequest struct {
	Status string `json:"status" form:"status" binding:"required"`
}

// reorderTodosRequest TODO 排序请求体。
type reorderTodosRequest struct {
	IDs []uint64 `json:"ids" form:"ids" binding:"required"`
}

// TodoList 获取 TODO 列表。
//
// GET /api/v1/todos?status=&sort=
func TodoList(c *gin.Context) {
	status := c.Query("status")
	sort := c.DefaultQuery("sort", "priority")

	todos, err := svc.Todo.ListTodos(status, sort)
	if err != nil {
		FailInternal(c, "查询待办列表失败")
		return
	}
	Success(c, todos)
}

// TodoGet 获取单个 TODO。
//
// GET /api/v1/todos/:id
func TodoGet(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的待办 ID")
		return
	}

	todo, err := svc.Todo.GetTodo(id)
	if err != nil {
		Fail(c, http.StatusNotFound, CodeNotFound, "待办不存在")
		return
	}
	Success(c, todo)
}

// TodoCreate 创建 TODO。
//
// POST /api/v1/todos
func TodoCreate(c *gin.Context) {
	var req createTodoRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "请提供待办标题")
		return
	}

	todo, err := svc.Todo.CreateTodo(req.Title, req.Description, req.Priority, req.DueDate)
	if err != nil {
		FailBadRequest(c, err.Error())
		return
	}
	Success(c, todo)
}

// TodoUpdate 更新 TODO。
//
// PUT /api/v1/todos/:id
func TodoUpdate(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的待办 ID")
		return
	}

	var req updateTodoRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "无效的请求参数")
		return
	}

	// 状态校验
	if req.Status != nil {
		if !model.IsValidTodoStatus(*req.Status) {
			FailBadRequest(c, "无效的状态值，合法值：planned/pending/in_progress/completed/paused")
			return
		}
	}

	title := ""
	if req.Title != nil {
		title = *req.Title
	}
	desc := ""
	if req.Description != nil {
		desc = *req.Description
	}
	status := ""
	if req.Status != nil {
		status = *req.Status
	}

	if err := svc.Todo.UpdateTodo(id, title, desc, status, req.Priority, req.DueDate); err != nil {
		FailInternal(c, "更新待办失败")
		return
	}
	Success(c, nil)
}

// TodoDelete 删除 TODO。
//
// DELETE /api/v1/todos/:id
func TodoDelete(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的待办 ID")
		return
	}

	if err := svc.Todo.DeleteTodo(id); err != nil {
		Fail(c, http.StatusNotFound, CodeNotFound, "待办不存在")
		return
	}
	Success(c, nil)
}

// TodoUpdateStatus 快捷修改 TODO 状态。
//
// PATCH /api/v1/todos/:id/status
func TodoUpdateStatus(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的待办 ID")
		return
	}

	var req updateTodoStatusRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "请提供状态值")
		return
	}

	if !model.IsValidTodoStatus(req.Status) {
		FailBadRequest(c, "无效的状态值，合法值：planned/pending/in_progress/completed/paused")
		return
	}

	if err := svc.Todo.UpdateTodo(id, "", "", req.Status, nil, nil); err != nil {
		FailInternal(c, "更新状态失败")
		return
	}
	Success(c, nil)
}

// TodoReorder 调整 TODO 排序。
//
// POST /api/v1/todos/reorder
func TodoReorder(c *gin.Context) {
	var req reorderTodosRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "请提供 ids")
		return
	}

	if err := svc.Todo.ReorderTodos(req.IDs); err != nil {
		FailInternal(c, "更新排序失败")
		return
	}
	Success(c, nil)
}
