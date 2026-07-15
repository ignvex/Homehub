package handler

import (
	"github.com/gin-gonic/gin"
)

// ============================================================
// 余额监控 API
// ============================================================

// BalanceList 返回所有监控点的最新余额（缓存结果）。
//
//	@Summary		获取所有余额
//	@Description	返回所有已配置监控点的最新查询结果（内存缓存），包括 DeepSeek、智谱 AI、one-api/new-api 中转站
//	@Tags			余额监控
//	@Produce		json
//	@Success		200	{array}		service.TargetResult	"监控点列表"
//	@Failure		500	{object}	Response				"服务内部错误"
//	@Router			/balances [get]
func BalanceList(c *gin.Context) {
	if svc.Balance == nil {
		FailInternal(c, "余额监控服务未初始化")
		return
	}
	results := svc.Balance.GetAllResults()
	Success(c, results)
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
