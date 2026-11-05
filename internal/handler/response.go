package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// ============================================================
// 错误码定义（遵循阿里巴巴错误码规范，5 位数字）
// ============================================================

const (
	CodeSuccess       = 0     // 成功
	CodeBadRequest    = 10001 // 请求参数错误
	CodeUnauthorized  = 10003 // 未认证
	CodeNotFound      = 10005 // 资源不存在
	CodeInternalError = 10007 // 服务器内部错误
	// CodeTooManyRequests 与 middleware.tooManyRequestsCode 一致（不能互相 import）。
	CodeTooManyRequests  = 10009 // 请求过于频繁
	CodeAuthFailed       = 20001 // 用户名或密码错误
	CodePasswordTooWeak  = 20003 // 密码强度不足
	CodeGroupNotFound    = 50001 // 书签分组不存在
	CodeBookmarkNotFound = 50002 // 书签不存在
	CodeMediaNotFound    = 60001 // 媒体资源不存在
	CodeFileTooLarge     = 60002 // 上传文件过大
	CodeInvalidFileType  = 60003 // 不支持的文件类型
	CodeForbidden        = 30001 // 权限不足
	CodeUserDisabled     = 20004 // 账号已被禁用
	CodeUsernameExists   = 30002 // 用户名已存在
	CodeUserNotFound     = 30003 // 用户不存在
	CodeSelfOperation    = 30004 // 不能操作自己

	// Extension 对接（7 段）。上游的状态码不透传：
	// 401 会让前端误以为自己的会话失效，404 会让前端误以为
	// HomeHub 里没有这个资源。两种都归入 70002。
	CodeExtensionDisabled = 70001 // 对接未启用或配置不齐
	CodeExtensionUpstream = 70002 // 上游 Extension 不可达或返回错误
)

// ============================================================
// 统一响应结构
// ============================================================

// Response 统一 JSON 响应体。
type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Success 返回成功响应（HTTP 200）。
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Code:    CodeSuccess,
		Message: "success",
		Data:    data,
	})
}

// Fail 返回业务错误响应。
//
// httpStatus 为 HTTP 状态码，code 为业务错误码，message 为错误描述。
func Fail(c *gin.Context, httpStatus int, code int, message string) {
	c.AbortWithStatusJSON(httpStatus, Response{
		Code:    code,
		Message: message,
	})
}

// FailBadRequest 返回参数错误响应（HTTP 400）。
func FailBadRequest(c *gin.Context, message string) {
	Fail(c, http.StatusBadRequest, CodeBadRequest, message)
}

// FailUnauthorized 返回未认证响应（HTTP 401）。
func FailUnauthorized(c *gin.Context, message string) {
	Fail(c, http.StatusUnauthorized, CodeUnauthorized, message)
}

// FailInternal 返回内部错误响应（HTTP 500）。
func FailInternal(c *gin.Context, message string) {
	Fail(c, http.StatusInternalServerError, CodeInternalError, message)
}

// queryInt 读取整形查询参数，缺失或解析失败时返回默认值。
func queryInt(c *gin.Context, key string, defaultVal int) int {
	val := c.Query(key)
	if val == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	return n
}
