package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/ignvex/HomeHub/internal/model"
)

// getLoginInfo 从 gin.Context 中提取登录状态信息。
//
// 返回 (是否已登录, 用户名)。
func getLoginInfo(c *gin.Context) (bool, string) {
	u, exists := c.Get("user")
	if !exists {
		return false, ""
	}
	user := u.(*model.User)
	return true, user.Username
}

// LoginPage 渲染登录页面。
//
// GET /login
func LoginPage(c *gin.Context) {
	c.HTML(http.StatusOK, "login.html", gin.H{
		"title": "HomeHub - 登录",
	})
}

// HomePage 渲染首页导航页面。
//
// GET /
func HomePage(c *gin.Context) {
	loggedIn, username := getLoginInfo(c)
	c.HTML(http.StatusOK, "home.html", gin.H{
		"title":    "HomeHub",
		"LoggedIn": loggedIn,
		"Username": username,
	})
}

// BookmarksPage 渲染书签管理页面。
//
// GET /bookmarks
func BookmarksPage(c *gin.Context) {
	loggedIn, username := getLoginInfo(c)
	c.HTML(http.StatusOK, "bookmarks.html", gin.H{
		"title":    "书签管理 - HomeHub",
		"LoggedIn": loggedIn,
		"Username": username,
	})
}

// SettingsPage 渲染配置管理页面。
//
// GET /settings
func SettingsPage(c *gin.Context) {
	loggedIn, username := getLoginInfo(c)
	c.HTML(http.StatusOK, "settings.html", gin.H{
		"title":    "设置 - HomeHub",
		"LoggedIn": loggedIn,
		"Username": username,
	})
}
