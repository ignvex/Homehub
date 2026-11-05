package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ============================================================
// RSS 阅读状态（按用户的已读 / 星标）
// ============================================================
//
// 状态存在 rss_entry_states，主键 (user_id, entry_id)。写入一律用
// INSERT ... SELECT FROM rss_entries ... ON CONFLICT DO UPDATE：
// 条目是否存在与写入是同一条语句，不会给已删除的条目留下状态行，
// 也不需要「先查后写」两次往返。

// entryStateColumns 追加在 entryListColumns 之后，依赖别名 st 的 LEFT JOIN。
const entryStateColumns = `,
	(st.read_at IS NOT NULL) AS is_read,
	(st.starred_at IS NOT NULL) AS is_starred`

// EntryStateValues 返回 state 筛选的合法取值（不含空串）。
func EntryStateValues() []string {
	return []string{"unread", "read", "starred"}
}

// entryStateCondition 把 state 筛选翻译成 WHERE 条件（依赖别名 st）。
// 第二个返回值为 false 表示取值非法。
func entryStateCondition(state string) (string, bool) {
	switch state {
	case "":
		return "", true
	case "unread":
		// 没有状态行（LEFT JOIN 得到 NULL）与 read_at 为 NULL 都算未读。
		return "st.read_at IS NULL", true
	case "read":
		return "st.read_at IS NOT NULL", true
	case "starred":
		return "st.starred_at IS NOT NULL", true
	default:
		return "", false
	}
}

// entryStateColumn 把状态种类映射成列名。只接受白名单内的值，
// 因为列名会拼进 SQL。
func entryStateColumn(kind string) (string, error) {
	switch kind {
	case "read":
		return "read_at", nil
	case "star":
		return "starred_at", nil
	default:
		return "", fmt.Errorf("未知的阅读状态 %q", kind)
	}
}

// SetEntryRead 把条目对 userID 标记为已读（read=true）或未读。
func (s *RSSService) SetEntryRead(userID, entryID uint64, read bool) error {
	return s.setEntryState(userID, entryID, "read", read)
}

// SetEntryStarred 给条目加上或去掉 userID 的星标。
func (s *RSSService) SetEntryStarred(userID, entryID uint64, starred bool) error {
	return s.setEntryState(userID, entryID, "star", starred)
}

// setEntryState 设置或清除一个状态列。
//
// 设置是幂等的：已读的条目再标一次已读，保留第一次的 read_at。
// 清除后若两列都为 NULL，就删掉这一行，避免表里堆满全空的行。
func (s *RSSService) setEntryState(userID, entryID uint64, kind string, on bool) error {
	if userID == 0 {
		return ErrForbidden
	}
	col, err := entryStateColumn(kind)
	if err != nil {
		return err
	}
	now := time.Now()

	if on {
		res := s.db.Exec(`
			INSERT INTO rss_entry_states (user_id, entry_id, `+col+`, updated_at)
			SELECT ?, e.id, ?, ?
			FROM rss_entries e
			WHERE e.id = ? AND e.deleted_at IS NULL
			ON CONFLICT (user_id, entry_id) DO UPDATE
			SET `+col+` = COALESCE(rss_entry_states.`+col+`, EXCLUDED.`+col+`),
			    updated_at = EXCLUDED.updated_at`,
			userID, now, now, entryID)
		if res.Error != nil {
			return fmt.Errorf("更新阅读状态失败: %w", res.Error)
		}
		// ON CONFLICT DO UPDATE 也计入 RowsAffected；为 0 只可能是 SELECT 没选到条目。
		if res.RowsAffected == 0 {
			return ErrRSSEntryNotFound
		}
		return nil
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(`UPDATE rss_entry_states SET `+col+` = NULL, updated_at = ?
			WHERE user_id = ? AND entry_id = ?`, now, userID, entryID)
		if res.Error != nil {
			return fmt.Errorf("更新阅读状态失败: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			// 没有状态行：条目存在就是幂等成功，不存在则 404。
			var n int64
			if err := tx.Table("rss_entries").
				Where("id = ? AND deleted_at IS NULL", entryID).
				Count(&n).Error; err != nil {
				return fmt.Errorf("查询条目失败: %w", err)
			}
			if n == 0 {
				return ErrRSSEntryNotFound
			}
			return nil
		}
		if err := tx.Exec(`DELETE FROM rss_entry_states
			WHERE user_id = ? AND entry_id = ? AND read_at IS NULL AND starred_at IS NULL`,
			userID, entryID).Error; err != nil {
			return fmt.Errorf("清理阅读状态失败: %w", err)
		}
		return nil
	})
}

// MarkReadInput 批量标记已读的范围。各条件之间是 AND；全部为空表示「全部已读」。
type MarkReadInput struct {
	FeedID  *uint64
	GroupID *uint64
	// EntryIDs 为 nil 表示不按 id 限定；非 nil 但为空由调用方拒绝
	// （否则一个空数组就会变成「全部已读」）。
	EntryIDs []uint64
	// Before 只标记发布时间不晚于它的条目（「标记到这里为止」）。
	Before *time.Time
}

// maxMarkReadIDs 是 entry_ids 的数量上限，与批量更新订阅源同一取舍：防误用。
const maxMarkReadIDs = 1000

// 批量标记已读的调用方错误，handler 据此回 400。
var (
	// ErrMarkReadEmptyIDs 表示 entry_ids 显式给了空数组。
	ErrMarkReadEmptyIDs = errors.New("entry_ids 不能为空数组")
	// ErrMarkReadTooMany 表示 entry_ids 超过上限。
	ErrMarkReadTooMany = fmt.Errorf("单次最多标记 %d 个条目", maxMarkReadIDs)
)

// MarkEntriesRead 把范围内的条目对 userID 标记为已读，返回新标记的条数。
//
// 一条 INSERT ... SELECT ... ON CONFLICT DO UPDATE 完成；DO UPDATE 带
// `WHERE read_at IS NULL`，已读的行不重写，也不计入返回值——返回值就是
// 「这次真正从未读变成已读的条数」，前端可以直接拿它减计数。
//
// 只处理至少被一个源收录的条目，与列表可见范围一致。
func (s *RSSService) MarkEntriesRead(userID uint64, in MarkReadInput) (int64, error) {
	if userID == 0 {
		return 0, ErrForbidden
	}
	if in.EntryIDs != nil && len(in.EntryIDs) == 0 {
		return 0, ErrMarkReadEmptyIDs
	}
	if len(in.EntryIDs) > maxMarkReadIDs {
		return 0, ErrMarkReadTooMany
	}

	now := time.Now()
	linkCond := "ef.entry_id = e.id AND ef.deleted_at IS NULL"
	linkJoin := ""
	var linkArgs []any
	if in.FeedID != nil {
		linkCond += " AND ef.feed_id = ?"
		linkArgs = append(linkArgs, *in.FeedID)
	}
	if in.GroupID != nil {
		linkJoin = " JOIN rss_feeds f ON f.id = ef.feed_id AND f.deleted_at IS NULL"
		linkCond += " AND f.group_id = ?"
		linkArgs = append(linkArgs, *in.GroupID)
	}

	var where strings.Builder
	where.WriteString("e.deleted_at IS NULL AND EXISTS (SELECT 1 FROM rss_entry_feeds ef" + linkJoin + " WHERE " + linkCond + ")")
	args := []any{userID, now, now}
	args = append(args, linkArgs...)
	if in.EntryIDs != nil {
		where.WriteString(" AND e.id IN ?")
		args = append(args, in.EntryIDs)
	}
	if in.Before != nil {
		where.WriteString(" AND e.published <= ?")
		args = append(args, *in.Before)
	}

	res := s.db.Exec(`
		INSERT INTO rss_entry_states (user_id, entry_id, read_at, updated_at)
		SELECT ?, e.id, ?, ?
		FROM rss_entries e
		WHERE `+where.String()+`
		ON CONFLICT (user_id, entry_id) DO UPDATE
		SET read_at = EXCLUDED.read_at, updated_at = EXCLUDED.updated_at
		WHERE rss_entry_states.read_at IS NULL`, args...)
	if res.Error != nil {
		return 0, fmt.Errorf("批量标记已读失败: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// UnreadCounts 是当前用户的未读计数。
//
// map 的键在 JSON 里是字符串（"3": 12），这是 encoding/json 对整数键的规则。
// 计数为 0 的源与分组不出现在 map 里。
type UnreadCounts struct {
	// Total 是去重后的未读条目数：同一条目被多个源收录只算一次。
	Total int64 `json:"total"`
	// ByFeed 按源计数，一个条目被两个源收录时在两个源下各算一次。
	ByFeed map[uint64]int64 `json:"by_feed"`
	// ByGroup 按分组计数，同一分组内去重；未分组的源不计入任何分组。
	ByGroup map[uint64]int64 `json:"by_group"`
}

// unreadCountRow 是 GROUPING SETS 的一行。
type unreadCountRow struct {
	FeedID  *uint64 `gorm:"column:feed_id"`
	GroupID *uint64 `gorm:"column:group_id"`
	GFeed   int     `gorm:"column:g_feed"`
	GGroup  int     `gorm:"column:g_group"`
	N       int64   `gorm:"column:n"`
}

// UnreadCounts 用一条 SQL 聚合出总数、按源、按分组的未读数。
//
// GROUPING SETS 一次扫描给出三种分组；GROUPING() 区分「这一行是哪个
// 分组集合的汇总」与「group_id 本身就是 NULL（未分组）」，后者不计入
// by_group。计数按 rss_entry_feeds 关联表算，COUNT(DISTINCT) 保证分组
// 与总数里共享条目只算一次。
func (s *RSSService) UnreadCounts(userID uint64) (*UnreadCounts, error) {
	if userID == 0 {
		return nil, ErrForbidden
	}
	var rows []unreadCountRow
	if err := s.db.Raw(`
		SELECT ef.feed_id, f.group_id,
		       GROUPING(ef.feed_id) AS g_feed, GROUPING(f.group_id) AS g_group,
		       COUNT(DISTINCT ef.entry_id) AS n
		FROM rss_entry_feeds ef
		JOIN rss_entries e ON e.id = ef.entry_id AND e.deleted_at IS NULL
		JOIN rss_feeds f ON f.id = ef.feed_id AND f.deleted_at IS NULL
		LEFT JOIN rss_entry_states st ON st.entry_id = ef.entry_id AND st.user_id = ?
		WHERE ef.deleted_at IS NULL AND st.read_at IS NULL
		GROUP BY GROUPING SETS ((ef.feed_id), (f.group_id), ())`, userID).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("统计未读数失败: %w", err)
	}

	out := &UnreadCounts{ByFeed: map[uint64]int64{}, ByGroup: map[uint64]int64{}}
	for _, r := range rows {
		switch {
		case r.GFeed == 1 && r.GGroup == 1:
			out.Total = r.N
		case r.GFeed == 0 && r.FeedID != nil:
			out.ByFeed[*r.FeedID] = r.N
		case r.GGroup == 0 && r.GroupID != nil:
			out.ByGroup[*r.GroupID] = r.N
		}
	}
	return out, nil
}
