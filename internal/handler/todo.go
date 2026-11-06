package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/model"
	"github.com/ignvex/HomeHub/internal/service"
)

// respondTodoError 把 service 错误映射成状态码。
//
// 「不存在」曾一律报 500：前端看到的是服务端故障，而不是这条已被删除。
func respondTodoError(c *gin.Context, err error, fallback string) {
	switch {
	case errors.Is(err, service.ErrTodoNotFound):
		Fail(c, http.StatusNotFound, CodeNotFound, "待办不存在")
	case errors.Is(err, service.ErrTodoBadStatus), errors.Is(err, service.ErrTodoBadAssignee),
		errors.Is(err, service.ErrReorderTooManyIDs):
		FailBadRequest(c, err.Error())
	case errors.Is(err, service.ErrForbidden):
		Fail(c, http.StatusForbidden, CodeForbidden, "4 级用户只能修改自己创建的待办，被指派的待办只能改状态")
	default:
		FailInternal(c, fallback)
	}
}

// ============================================================
// TODO 待办事项 API
// ============================================================

// createTodoRequest 创建 TODO 请求体。
type createTodoRequest struct {
	Title       string     `json:"title" form:"title" binding:"required"`
	Description string     `json:"description" form:"description"`
	Priority    int        `json:"priority" form:"priority"`
	DueDate     *time.Time `json:"due_date" form:"due_date"`
	// AssigneeID 可选，指派给某个启用中的用户。
	AssigneeID *uint64 `json:"assignee_id" form:"assignee_id"`
}

// updateTodoRequest 更新 TODO 请求体（全部可选）。
//
// description 与 due_date 是三态：省略=不改；null 或空串=清空；值=写入。
type updateTodoRequest struct {
	Title       *string           `json:"title"`
	Description optionalString    `json:"description" swaggertype:"string" extensions:"x-nullable"`
	Status      *string           `json:"status"`
	Priority    *int              `json:"priority"`
	DueDate     optionalTimestamp `json:"due_date" swaggertype:"string" format:"date-time" extensions:"x-nullable"`
	// AssigneeID 省略=不改，null=取消指派，值=指派给该用户。
	AssigneeID optionalGroupID `json:"assignee_id" swaggertype:"integer" extensions:"x-nullable"`
}

// optionalString 区分字段缺失、显式 null / 空串（清空）与具体值。
// 与 optionalGroupID 同一个思路：UnmarshalJSON 只在字段出现时被调用。
type optionalString struct {
	Set   bool
	Value *string
}

// UnmarshalJSON 记录字段出现过；null 与空串都表示清空。
func (o *optionalString) UnmarshalJSON(data []byte) error {
	o.Set = true
	if string(data) == "null" {
		o.Value = nil
		return nil
	}
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if v == "" {
		o.Value = nil
		return nil
	}
	o.Value = &v
	return nil
}

// optionalTimestamp 区分字段缺失、显式 null / 空串（清空）与 RFC3339 时间。
type optionalTimestamp struct {
	Set   bool
	Value *time.Time
}

// UnmarshalJSON 记录字段出现过；null 与空串都表示清空。
func (o *optionalTimestamp) UnmarshalJSON(data []byte) error {
	o.Set = true
	if string(data) == "null" || string(data) == `""` {
		o.Value = nil
		return nil
	}
	var v time.Time
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
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
//	@Description	获取待办事项，可按状态、被指派者、创建者筛选和排序。每条附带 creator / assignee 的 {id, username}
//	@Tags			待办事项
//	@Produce		json
//	@Param			status		query		string	false	"状态筛选：planned/pending/in_progress/completed/paused"
//	@Param			sort		query		string	false	"排序：priority（默认）/due_date/created_at"
//	@Param			assignee	query		string	false	"me 或用户 ID：只看指派给该用户的"
//	@Param			mine		query		string	false	"1：只看我创建的"
//	@Success		200			{object}	Response{data=[]model.Todo}	"待办列表"
//	@Failure		400			{object}	Response	"assignee 参数无效"
//	@Failure		500			{object}	Response	"服务器内部错误"
//	@Router			/todos [get]
func TodoList(c *gin.Context) {
	filter := service.TodoFilter{
		Status: c.Query("status"),
		Sort:   c.DefaultQuery("sort", "priority"),
	}
	me := getCurrentUserID(c)
	if a := c.Query("assignee"); a != "" {
		if a == "me" {
			filter.AssigneeID = &me
		} else {
			id, err := strconv.ParseUint(a, 10, 64)
			if err != nil {
				FailBadRequest(c, "assignee 只能是 me 或用户 ID")
				return
			}
			filter.AssigneeID = &id
		}
	}
	if c.Query("mine") == "1" || c.Query("mine") == "true" {
		filter.CreatedBy = &me
	}

	todos, err := svc.Todo.ListTodos(filter)
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

	todo, err := svc.Todo.CreateTodo(getCurrentUser(c), service.CreateTodoInput{
		Title:       req.Title,
		Description: req.Description,
		Priority:    req.Priority,
		DueDate:     req.DueDate,
		AssigneeID:  req.AssigneeID,
	})
	if err != nil {
		if errors.Is(err, service.ErrForbidden) {
			Fail(c, http.StatusForbidden, CodeForbidden, "权限不足")
			return
		}
		FailBadRequest(c, err.Error())
		return
	}
	Success(c, todo)
}

// TodoUpdate 更新 TODO。
//
//	@Summary		更新待办
//	@Description	更新待办事项的标题、描述、状态、优先级或截止日期。description / due_date 省略=不改，null 或空串=清空
//	@Tags			待办事项
//	@Accept			json
//	@Produce		json
//	@Param			id		path		int					true	"待办 ID"
//	@Param			request	body		updateTodoRequest	true	"更新请求（全部可选）"
//	@Success		200		{object}	Response			"更新成功"
//	@Failure		400		{object}	Response			"无效的请求参数"
//	@Failure		500		{object}	Response			"服务器内部错误"
//	@Failure		403	{object}	Response	"4 级越权"
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

	if err := svc.Todo.UpdateTodo(getCurrentUser(c), id, service.TodoUpdate{
		Title:       req.Title,
		Status:      req.Status,
		Priority:    req.Priority,
		Description: service.Optional[string]{Set: req.Description.Set, Value: req.Description.Value},
		DueDate:     service.Optional[time.Time]{Set: req.DueDate.Set, Value: req.DueDate.Value},
		Assignee:    service.Optional[uint64]{Set: req.AssigneeID.Set, Value: req.AssigneeID.Value},
	}); err != nil {
		respondTodoError(c, err, "更新待办失败")
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
//	@Failure		403	{object}	Response	"4 级越权"
//	@Router			/todos/{id} [delete]
func TodoDelete(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		FailBadRequest(c, "无效的待办 ID")
		return
	}

	if err := svc.Todo.DeleteTodo(getCurrentUser(c), id); err != nil {
		respondTodoError(c, err, "删除待办失败")
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
//	@Failure		403	{object}	Response	"4 级越权"
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

	if err := svc.Todo.UpdateTodo(getCurrentUser(c), id, service.TodoUpdate{Status: &req.Status}); err != nil {
		respondTodoError(c, err, "更新状态失败")
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
//	@Failure		403	{object}	Response	"4 级越权"
//	@Router			/todos/reorder [post]
func TodoReorder(c *gin.Context) {
	var req reorderTodosRequest
	if err := c.ShouldBind(&req); err != nil {
		FailBadRequest(c, "请提供 ids")
		return
	}

	if err := svc.Todo.ReorderTodos(getCurrentUser(c), req.IDs); err != nil {
		respondTodoError(c, err, "更新排序失败")
		return
	}
	Success(c, nil)
}
