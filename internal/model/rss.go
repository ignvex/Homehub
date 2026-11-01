package model

import "time"

// ============================================================
// RSSFeed — RSS 订阅源
// ============================================================

// RSSFeed RSS 订阅源模型。
//
// 每个订阅源对应一个 RSS/Atom feed URL，定时抓取条目。可归属到分组。
// 表名：rss_feeds
type RSSFeed struct {
	BaseModel
	Title       string `gorm:"size:255;not null" json:"title"`            // 订阅源标题（用户填的优先，否则抓取时补全）
	URL         string `gorm:"uniqueIndex;size:2048;not null" json:"url"` // 订阅源 URL（唯一）
	SiteURL     string `gorm:"size:2048" json:"site_url"`                 // 站点主页 URL
	Description string `gorm:"type:text" json:"description,omitempty"`    // 订阅源描述（来自 feed 元数据）
	// Remark 是使用者自己写的备注，与 Description 分开。
	//
	// Description 来自外部 feed，抓取时会被覆盖；Remark 只由人写、只由人改。
	// 合成一个字段的结果是「每次抓取都把你写的备注冲掉」，而且没人能预期。
	Remark        string `gorm:"size:500" json:"remark,omitempty"`
	IconURL       string `gorm:"size:2048" json:"icon_url,omitempty"` // 站点图标 URL
	FetchInterval int    `gorm:"default:3600" json:"fetch_interval"`  // 抓取间隔（秒），默认 3600
	// FetchBy 决定这个源由谁抓：local（HomeHub 自己）或 extension（推给 hhe）。
	//
	// 这不是「把调度转发出去」——两边的条目存在各自的库里，互不相通。
	// 选 extension 意味着阅读要去 Extension 的界面，HomeHub 这侧不会再
	// 产生条目。所以它是**阅读器归属**的选择，不是抓取开关。
	FetchBy string `gorm:"size:20;not null;default:'local';index" json:"fetch_by"`
	// FetchEnabled 控制这个源是否参与后台调度。
	//
	// 与 FetchBy 是两件事：后者决定「谁抓」，前者决定「抓不抓」。
	// 合在一起没法表达「暂停但不改变归属」——而暂时不想看某个源、
	// 又不想丢掉它的分组与历史时，删掉再重建是最差的选择。
	//
	// 默认 true：旧数据迁移后应当照常被抓，不能因为新加一列就静默停摆。
	FetchEnabled bool      `gorm:"not null;default:true" json:"fetch_enabled"`
	LastFetch    time.Time `json:"last_fetch"`                           // 最近一次抓取时间
	ErrorMsg     string    `gorm:"size:1024" json:"error_msg,omitempty"` // 最近一次错误信息
	GroupID      *uint64   `gorm:"index" json:"group_id,omitempty"`      // 所属分组 ID（NULL 表示未分组）
}

// TableName 指定表名。
func (RSSFeed) TableName() string {
	return "rss_feeds"
}

// 抓取执行者。取值进数据库与 JSON，改名等于改对外契约。
const (
	// FetchByLocal 由 HomeHub 自己抓取，条目落在本仓库的 rss_entries。
	FetchByLocal = "local"
	// FetchByExtension 推给 HomeHub Extension 抓取，条目落在对方的库。
	// HomeHub 侧的 FetchAll 会跳过这些源，避免两边都抓、各存一份。
	FetchByExtension = "extension"
)

// FetchByValues 返回合法取值，供校验与文档使用。
func FetchByValues() []string {
	return []string{FetchByLocal, FetchByExtension}
}

// IsValidFetchBy 判断取值是否合法。空串按 local 处理（兼容旧数据）。
func IsValidFetchBy(v string) bool {
	if v == "" {
		return true
	}
	for _, valid := range FetchByValues() {
		if v == valid {
			return true
		}
	}
	return false
}

// NormalizeFetchBy 把空串兜底成 local。
//
// 旧数据与新建请求都可能不带这个字段；默认必须是「自己抓」，
// 因为把源静默交给远端会让本地阅读器突然空掉。
func NormalizeFetchBy(v string) string {
	if v == "" {
		return FetchByLocal
	}
	return v
}

// ============================================================
// RSSEntry — RSS 条目
// ============================================================

// RSSEntry RSS 条目模型（纯内容，不归属任何源）。
//
// 按 link 全局去重：同一篇文章被多个 feed 收录时只有一行，谁收录了它
// 由 RSSEntryFeed 记录。这样删除一个源不会带走其他源仍在引用的内容，
// 正文也不必按源重复存储。
//
// Published/FetchedAt 是"首次见到"的时间；每个源自己的发布时间在
// RSSEntryFeed.Published 上。
//
// 表名：rss_entries
type RSSEntry struct {
	BaseModel
	Title     string    `gorm:"size:500;not null" json:"title"`             // 条目标题
	Link      string    `gorm:"uniqueIndex;size:2048;not null" json:"link"` // 条目链接（全表唯一）
	Author    string    `gorm:"size:255" json:"author,omitempty"`           // 作者
	Content   string    `gorm:"type:text" json:"content,omitempty"`         // 清洗后的正文
	MagnetURL string    `gorm:"type:text" json:"magnet_url,omitempty"`      // 磁力链接
	Published time.Time `gorm:"index" json:"published"`                     // 首次见到的发布时间
	FetchedAt time.Time `gorm:"not null" json:"fetched_at"`                 // 首次抓取时间

	// ---- 磁力链接锁定与 PikPak 提交状态 ----
	//
	// 锁定的含义：这条磁力链接已被人工确认，后续抓取不得改写它。
	// 抓取时的 ON CONFLICT 已经有“空值不覆盖”的保护，但那只挡得住
	// 空值；源站换了磁力链接时仍会覆盖。锁定就是把这个口子关上。
	//
	// 锁定与 PikPak 是两件独立的事：可以只锁定不提交，也可以在未启用的
	// 情况下单纯标记。所以两者字段分开，状态也分开。
	MagnetLocked   bool       `gorm:"not null;default:false;index" json:"magnet_locked"`
	MagnetLockedAt *time.Time `json:"magnet_locked_at,omitempty"`
	MagnetLockedBy uint64     `gorm:"not null;default:0" json:"magnet_locked_by,omitempty"`

	// PikPakStatus 提交状态：空 / pending / saved / failed。
	// 用短字符串而不是枚举类型：改状态不需要 DDL。
	//
	// 这四列都显式指定 column：GORM 会把 PikPak 拆成 pik_pak，
	// 得到 pik_pak_status 这种与 JSON、文档、手写 SQL 都不一致的名字。
	// 别名一旦分叉，排查时最容易在这里踩坑。
	PikPakStatus  string     `gorm:"column:pikpak_status;size:20;not null;default:'';index" json:"pikpak_status,omitempty"`
	PikPakTaskID  string     `gorm:"column:pikpak_task_id;size:128" json:"pikpak_task_id,omitempty"`
	PikPakError   string     `gorm:"column:pikpak_error;size:512" json:"pikpak_error,omitempty"`
	PikPakSavedAt *time.Time `gorm:"column:pikpak_saved_at" json:"pikpak_saved_at,omitempty"`
}

// TableName 指定表名。
func (RSSEntry) TableName() string {
	return "rss_entries"
}

// ============================================================
// RSSEntryFeed — 条目与订阅源的收录关系
// ============================================================

// RSSEntryFeed 记录"某个订阅源收录了某个条目"。
//
// 这是条目归属的唯一来源：删除订阅源只删这里的行，只有当一个条目
// 不再被任何源收录时才回收内容本身。Published 是**该源**给出的发布
// 时间，不同源可能不一致，按源筛选时按它排序。
//
// 表名：rss_entry_feeds
type RSSEntryFeed struct {
	BaseModel
	EntryID     uint64    `gorm:"not null;uniqueIndex:idx_rss_entry_feed" json:"entry_id"`      // 条目 ID
	FeedID      uint64    `gorm:"not null;index;uniqueIndex:idx_rss_entry_feed" json:"feed_id"` // 订阅源 ID
	Published   time.Time `gorm:"index" json:"published"`                                       // 该源给出的发布时间
	FirstSeenAt time.Time `gorm:"not null" json:"first_seen_at"`                                // 首次被该源收录
	LastSeenAt  time.Time `gorm:"not null" json:"last_seen_at"`                                 // 最近一次出现在该源
}

// TableName 指定表名。
func (RSSEntryFeed) TableName() string {
	return "rss_entry_feeds"
}
