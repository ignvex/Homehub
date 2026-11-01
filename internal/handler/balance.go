package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ignvex/HomeHub/internal/service"
)

// ============================================================
// 余额监控 API
//
// 监控点从 1.1.0 起存数据库：增删改走这里的接口，config.json 的
// balance_monitor.targets 只在首次启动时导入一次，之后不再读取。
// ============================================================

// 余额监控的业务错误码（6 段）。
const (
	CodeBalanceNotFound = 61001 // 监控点不存在
	CodeBalanceExists   = 61002 // 同名监控点已存在
	CodeBalanceInvalid  = 61003 // 类型或字段不合法
)

// BalanceList 返回所有监控点及其最新余额。
//
//	@Summary		列出余额监控点
//	@Description	返回全部监控点（含停用的）与最新缓存结果；api_key 只返回掩码
//	@Tags			余额监控
//	@Produce		json
//	@Success		200	{array}		service.BalanceTargetView	"监控点列表"
//	@Failure		500	{object}	Response					"服务内部错误"
//	@Router			/balances [get]
func BalanceList(c *gin.Context) {
	if svc.Balance == nil {
		FailInternal(c, "余额监控服务未初始化")
		return
	}
	items, err := svc.Balance.ListTargets()
	if err != nil {
		FailInternal(c, err.Error())
		return
	}
	Success(c, items)
}

// BalanceCreate 新建一个监控点。
//
//	@Summary		新建监控点
//	@Description	名称唯一；api_key 必填，响应里只返回掩码
//	@Tags			余额监控
//	@Accept			json
//	@Produce		json
//	@Param			body	body		service.BalanceTargetInput	true	"监控点配置"
//	@Success		200		{object}	Response{data=service.BalanceTargetView}
//	@Failure		400		{object}	Response	"字段不合法"
//	@Failure		409		{object}	Response	"同名监控点已存在"
//	@Router			/balances [post]
func BalanceCreate(c *gin.Context) {
	if svc.Balance == nil {
		FailInternal(c, "余额监控服务未初始化")
		return
	}
	var in service.BalanceTargetInput
	if err := c.ShouldBindJSON(&in); err != nil {
		FailBadRequest(c, "请求参数错误："+err.Error())
		return
	}

	view, err := svc.Balance.CreateTarget(in)
	if err != nil {
		failBalance(c, err)
		return
	}
	Success(c, view)
}

// BalanceUpdate 更新监控点。省略的字段保持原值。
//
//	@Summary		更新监控点
//	@Description	省略的字段保持原值；api_key 传空串表示清空，不传表示不改
//	@Tags			余额监控
//	@Accept			json
//	@Produce		json
//	@Param			id		path		int							true	"监控点 ID"
//	@Param			body	body		service.BalanceTargetInput	true	"要修改的字段"
//	@Success		200		{object}	Response{data=service.BalanceTargetView}
//	@Failure		404		{object}	Response	"监控点不存在"
//	@Failure		409		{object}	Response	"同名监控点已存在"
//	@Router			/balances/{id} [put]
func BalanceUpdate(c *gin.Context) {
	if svc.Balance == nil {
		FailInternal(c, "余额监控服务未初始化")
		return
	}
	id, ok := parseBalanceID(c)
	if !ok {
		return
	}

	var in service.BalanceTargetInput
	if err := c.ShouldBindJSON(&in); err != nil {
		FailBadRequest(c, "请求参数错误："+err.Error())
		return
	}

	view, err := svc.Balance.UpdateTarget(id, in)
	if err != nil {
		failBalance(c, err)
		return
	}
	Success(c, view)
}

// BalanceDelete 删除监控点。
//
//	@Summary		删除监控点
//	@Description	同时清掉它的缓存结果；不可恢复
//	@Tags			余额监控
//	@Produce		json
//	@Param			id	path		int	true	"监控点 ID"
//	@Success		200	{object}	Response
//	@Failure		404	{object}	Response	"监控点不存在"
//	@Router			/balances/{id} [delete]
func BalanceDelete(c *gin.Context) {
	if svc.Balance == nil {
		FailInternal(c, "余额监控服务未初始化")
		return
	}
	id, ok := parseBalanceID(c)
	if !ok {
		return
	}

	if err := svc.Balance.DeleteTarget(id); err != nil {
		failBalance(c, err)
		return
	}
	Success(c, gin.H{"deleted": true, "id": id})
}

// BalanceRefreshOne 立即查询单个监控点。
//
//	@Summary		刷新单个监控点
//	@Description	同步查询一次，返回最新余额与状态
//	@Tags			余额监控
//	@Produce		json
//	@Param			id	path		int	true	"监控点 ID"
//	@Success		200	{object}	Response{data=service.BalanceTargetView}
//	@Failure		404	{object}	Response	"监控点不存在"
//	@Router			/balances/{id}/refresh [post]
func BalanceRefreshOne(c *gin.Context) {
	if svc.Balance == nil {
		FailInternal(c, "余额监控服务未初始化")
		return
	}
	id, ok := parseBalanceID(c)
	if !ok {
		return
	}

	view, err := svc.Balance.RefreshOne(id)
	if err != nil {
		failBalance(c, err)
		return
	}
	Success(c, view)
}

// BalanceRefresh 手动刷新所有监控点的余额。
//
//	@Summary		手动刷新余额
//	@Description	立即查询所有启用的监控点，返回最新结果（同步执行，可能耗时）
//	@Tags			余额监控
//	@Produce		json
//	@Success		200	{array}		service.TargetResult	"刷新后的监控点列表"
//	@Failure		500	{object}	Response				"服务内部错误"
//	@Router			/balances/refresh [post]
func BalanceRefresh(c *gin.Context) {
	if svc.Balance == nil {
		FailInternal(c, "余额监控服务未初始化")
		return
	}
	results := svc.Balance.RefreshAll()
	Success(c, results)
}

// BalanceStatus 返回余额监控模块的整体运行状态。
//
//	@Summary		余额监控状态
//	@Description	返回模块开关状态、监控点总数、成功/失败计数、上次刷新时间
//	@Tags			余额监控
//	@Produce		json
//	@Success		200	{object}	service.BalanceStatus	"监控状态概览"
//	@Failure		500	{object}	Response				"服务内部错误"
//	@Router			/balances/status [get]
func BalanceStatus(c *gin.Context) {
	if svc.Balance == nil {
		FailInternal(c, "余额监控服务未初始化")
		return
	}
	status := svc.Balance.GetStatus()
	Success(c, status)
}

// parseBalanceID 解析路径里的监控点 ID。
func parseBalanceID(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		FailBadRequest(c, "监控点 ID 不合法")
		return 0, false
	}
	return id, true
}

// failBalance 把 service 层的余额错误映射成状态码。
//
// 名字冲突必须给 409 而不是 400：前端要据此提示「换个名字」，
// 而不是「你填错了」——字段本身完全合法，只是重名。
func failBalance(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrBalanceTargetNotFound):
		Fail(c, http.StatusNotFound, CodeBalanceNotFound, err.Error())
	case errors.Is(err, service.ErrBalanceTargetExists):
		Fail(c, http.StatusConflict, CodeBalanceExists, err.Error())
	case errors.Is(err, service.ErrBalanceInvalidType),
		errors.Is(err, service.ErrBalanceMissingField):
		Fail(c, http.StatusBadRequest, CodeBalanceInvalid, err.Error())
	default:
		FailInternal(c, err.Error())
	}
}
