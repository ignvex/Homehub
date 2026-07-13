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
//	@Summary		获取待办列表
//	@Description	获取所有待办事项，可按状态筛选和排序
//	@Tags			待办事项
//	@Produce		json
//	@Param			status	query		string	false	"状态筛选：planned/pending/in_progress/completed/paused"
//	@Param			sort	query		string	false	"排序字段（默认 priority）"
//	@Success		200		{object}	Response	"待办列表"
//	@Failure		500		{object}	Response	"服务器内部错误"
//	@Router			/todos [get]
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
//	@Summary		获取待办详情
//	@Description	根据 ID 获取单个待办事项
//	@Tags			待办事项
//	@Produce		json
//	@Param			id	path		int		true	"待办 ID"
//	@Success		200	{object}	Response	"待办详情"
//	@Failure		400	{object}	Response	"无效的待办 ID"
//	@Failure		404	{object}	Response	"待办不存在"
//	@Router			/todos/{id} [get]
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
//	@Summary		创建待办
//	@Description	创建新的待办事项，可设置标题、描述、优先级和截止日期
//	@Tags			待办事项
//	@Accept			json
//	@Produce		json
//	@Param			request	body		createTodoRequest	true	"待办内容"
//	@Success		200		{object}	Response			"新创建的待办"
//	@Failure		400		{object}	Response			"缺少标题或参数错误"
//	@Router			/todos [post]
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
//	@Summary		更新待办
//	@Description	更新待办事项的标题、描述、状态、优先级或截止日期
//	@Tags			待办事项
//	@Accept			json
//	@Produce		json
//	@Param			id		path		int					true	"待办 ID"
//	@Param			request	body		updateTodoRequest	true	"更新请求（全部可选）"
//	@Success		200		{object}	Response			"更新成功"
//	@Failure		400		{object}	Response			"无效的请求参数"
//	@Failure		500		{object}	Response			"服务器内部错误"
//	@Router			/todos/{id} [put]
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
//	@Summary		删除待办
//	@Description	删除指定待办事项
//	@Tags			待办事项
//	@Produce		json
//	@Param			id	path		int		true	"待办 ID"
//	@Success		200	{object}	Response	"删除成功"
//	@Failure		400	{object}	Response	"无效的待办 ID"
//	@Failure		404	{object}	Response	"待办不存在"
//	@Router			/todos/{id} [delete]
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
//	@Summary		更新待办状态
//	@Description	快捷修改待办事项的状态（planned/pending/in_progress/completed/paused）
//	@Tags			待办事项
//	@Accept			json
//	@Produce		json
//	@Param			id		path		int							true	"待办 ID"
//	@Param			request	body		updateTodoStatusRequest		true	"新状态"
//	@Success		200		{object}	Response					"更新成功"
//	@Failure		400		{object}	Response					"无效的状态值"
//	@Failure		500		{object}	Response					"服务器内部错误"
//	@Router			/todos/{id}/status [patch]
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
//	@Summary		调整待办排序
//	@Description	按 ID 顺序重新排列待办事项的显示顺序
//	@Tags			待办事项
//	@Accept			json
//	@Produce		json
//	@Param			request	body		reorderTodosRequest	true	"排序后的 ID 列表"
//	@Success		200		{object}	Response			"排序成功"
//	@Failure		400		{object}	Response			"缺少 ids"
//	@Failure		500		{object}	Response			"服务器内部错误"
//	@Router			/todos/reorder [post]
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
