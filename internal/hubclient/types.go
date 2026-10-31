package hubclient

// 协议文档 §4 的请求与响应结构。
//
// 响应里的时刻统一是 Unix 秒，未设置时为 null —— 因此用指针而不是 0，
// 好让调用方区分"1970-01-01"与"还没发生"。

// Subscription 是 Extension 侧的一条订阅。
type Subscription struct {
	ID            uint64 `json:"id"`
	URL           string `json:"url"`
	Title         string `json:"title"`
	SiteURL       string `json:"site_url"`
	Enabled       bool   `json:"enabled"`
	FetchInterval string `json:"fetch_interval"` // 紧凑写法，如 "30m"、"1h30m"

	NextFetchAt *int64 `json:"next_fetch_at"`
	LastFetchAt *int64 `json:"last_fetch_at"`
	CreatedAt   int64  `json:"created_at"`

	LastStatus  int    `json:"last_status"` // 最近一次 HTTP 状态码，0 表示还没抓过
	LastError   string `json:"last_error"`  // 已截断到 500 字符，成功时为空
	ErrorCount  int    `json:"error_count"` // 连续失败次数，成功一次即清零
	UnreadCount int    `json:"unread_count"`
}

// UpsertSubscriptionRequest 推送或更新订阅。
//
// 省略字段表示"保留原值"，不是"重置为默认"，所以除 URL 外全部用指针：
// nil 与零值是两种不同的意图。想清空标题要显式传空字符串。
type UpsertSubscriptionRequest struct {
	URL      string  `json:"url"`
	Title    *string `json:"title,omitempty"`
	Interval *string `json:"interval,omitempty"`
	Enabled  *bool   `json:"enabled,omitempty"`
	// FetchNow 省略时默认为 true（异步抓一次后立即返回）。
	FetchNow *bool `json:"fetch_now,omitempty"`
}

// RefreshResult 是一次同步抓取的结果。最长耗时 2 分钟。
type RefreshResult struct {
	FeedID      uint64 `json:"feed_id"`
	URL         string `json:"url"`
	NotModified bool   `json:"not_modified"`
	NewEntries  int    `json:"new_entries"`
	Skipped     int    `json:"skipped"`
	MediaOK     int    `json:"media_ok"`
	MediaFail   int    `json:"media_fail"`
	Status      int    `json:"status"` // 目标站点返回的状态码
}

// AccessKey 是已签发的访问密钥。响应里永远不含明文，也不含哈希。
type AccessKey struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ClientID   string `json:"client_id"`
	CreatedAt  int64  `json:"created_at"`
	ExpiresAt  *int64 `json:"expires_at"`
	LastUsedAt *int64 `json:"last_used_at"`
	Revoked    bool   `json:"revoked"`
	// Live 是服务端算好的"未撤销且未过期"。对账只看这一个字段，
	// 不要自己拿 ExpiresAt 与本地时间比较 —— 时钟偏差会得出相反结论。
	Live bool `json:"live"`
}

// CreatedKey 是签发密钥的响应。
//
// Key 只在这一个响应里出现一次：Extension 存的是 sha256，之后任何接口
// 都拿不回明文。调用方必须当场交给用户或写入安全的存储。
type CreatedKey struct {
	AccessKey
	Key string `json:"key"`
}

// CreateKeyRequest 签发访问密钥。
//
// ExpiresIn 是 Go 风格的时长串（如 "720h"）。省略表示永不过期；
// "0" 或负数会被服务端拒绝。
type CreateKeyRequest struct {
	Name      string  `json:"name"`
	ExpiresIn *string `json:"expires_in,omitempty"`
}

// subscriptionList 与 keyList 是列表响应外壳，单独定义以免调用方
// 直接处理 items 可能为 null 的情况。
type subscriptionList struct {
	Items []Subscription `json:"items"`
}

type keyList struct {
	Items []AccessKey `json:"items"`
}

// errorEnvelope 是协议的统一错误结构。
type errorEnvelope struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}
