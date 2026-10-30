package model

import "fmt"

// 本文件定义 5 级权限模型。
//
// 约定：**数值越小权限越高**，1 级是最高的管理员。这样判断“是否足够权限”
// 就是一次 `user.Level <= required` 的整数比较，不需要任何映射表，也不会有
// “哪个角色能干什么”散落在各处的隐性规则。
//
// 为什么用数值而不是角色名：等级天然可比较，新增权限点只需指定最低等级，
// 不必为每个功能维护一张“允许角色列表”。代价是等级的含义必须写下来 ——
// 就是下面这张表。
const (
	// LevelAdmin 1 级：完全控制。配置、数据库连接、session_secret、用户管理、
	// 日志清空，以及所有内容。这是唯一能改动系统自身运行参数的等级。
	LevelAdmin = 1
	// LevelOperator 2 级：运维。可以管用户（但不能改动 1 级账号）、看与清日志，
	// 不能触碰配置与凭据。
	LevelOperator = 2
	// LevelEditor 3 级：内容管理。可以增删改全部内容（RSS、书签、媒体、待办），
	// 不分作者。不能管用户、不能看日志。
	LevelEditor = 3
	// LevelMember 4 级：成员。只能改自己创建的内容（自己上传的媒体等），
	// 可以读全部内容。
	LevelMember = 4
	// LevelViewer 5 级：只读。能看内容，不能写任何东西。
	LevelViewer = 5

	// LevelMin / LevelMax 是合法区间，用于校验。
	LevelMin = LevelAdmin
	LevelMax = LevelViewer
)

// levelNames 给每个等级一个稳定的英文标识，用于日志与 API 输出。
//
// 这些名字进 JSON 与日志，改名字等于改对外契约，不要顺手改。
var levelNames = map[int]string{
	LevelAdmin:    "admin",
	LevelOperator: "operator",
	LevelEditor:   "editor",
	LevelMember:   "member",
	LevelViewer:   "viewer",
}

// levelDescriptions 是给人和前端看的说明。
var levelDescriptions = map[int]string{
	LevelAdmin:    "完全控制：配置、用户、日志与全部内容",
	LevelOperator: "运维：用户管理与日志，不含配置与凭据",
	LevelEditor:   "内容管理：可增删改全部内容，不能管用户",
	LevelMember:   "成员：只能改自己创建的内容",
	LevelViewer:   "只读：可以浏览，不能写入",
}

// LevelName 返回等级的英文标识；非法等级返回空串。
func LevelName(level int) string {
	return levelNames[level]
}

// LevelDescription 返回等级的中文说明；非法等级返回空串。
func LevelDescription(level int) string {
	return levelDescriptions[level]
}

// IsValidLevel 判断等级是否落在 1–5。
func IsValidLevel(level int) bool {
	return level >= LevelMin && level <= LevelMax
}

// NormalizeLevel 把任意整数夹到合法区间。
//
// 用于“配置里写了个 0 或 99”这种输入错误的兜底：宁可降级到最低权限，
// 也不要因为一个坏值把权限放大。
func NormalizeLevel(level int) int {
	if level < LevelMin {
		return LevelMin
	}
	if level > LevelMax {
		return LevelMax
	}
	return level
}

// LevelFromRole 把旧版的 role 字符串映射成等级。
//
// 兼容用：改造前数据库里存的是 admin / user，前端可能仍在发这两个值。
// 认不出的值一律按最低权限处理 —— 这是有意的失败方向。
func LevelFromRole(role string) int {
	switch role {
	case "admin":
		return LevelAdmin
	case "user":
		return LevelViewer
	default:
		return LevelViewer
	}
}

// AllLevels 返回 1–5 的全部等级，供“列出可选等级”的接口使用。
func AllLevels() []LevelInfo {
	out := make([]LevelInfo, 0, LevelMax-LevelMin+1)
	for l := LevelMin; l <= LevelMax; l++ {
		out = append(out, LevelInfo{
			Level:       l,
			Name:        levelNames[l],
			Description: levelDescriptions[l],
		})
	}
	return out
}

// LevelInfo 描述一个等级，用于 API 输出。
type LevelInfo struct {
	Level       int    `json:"level"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// String 便于日志里直接打印等级。
func (i LevelInfo) String() string {
	return fmt.Sprintf("%d(%s)", i.Level, i.Name)
}
