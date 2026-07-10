package handler

import (
	"net/http"

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
	CodeConflict      = 10006 // 资源冲突
	CodeInternalError = 10007 // 服务器内部错误
	CodeAuthFailed    = 20001 // 用户名或密码错误
	CodeSessionExpired  = 20002 // 会话已过期
	CodePasswordTooWeak = 20003 // 密码强度不足
	CodeGroupNotFound   = 50001 // 书签分组不存在
	CodeBookmarkNotFound = 50002 // 书签不存在
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
