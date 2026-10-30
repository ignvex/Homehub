package model

import (
	"strings"
	"unicode/utf8"
)

// 本文件提供"把外部字符串安全收进 varchar 列"的截断工具。
//
// 为什么需要它：PostgreSQL 的 varchar(N) 中 N 是**字符数**，而 Go 的
// len(s) 与 s[:n] 走的是**字节**。直接 `s[:n]` 会把一个多字节字符切成
// 两半，产生非法 UTF-8 序列，PostgreSQL 会以
//
//	ERROR: invalid byte sequence for encoding "UTF8"
//
// 拒收整条 INSERT/UPDATE —— 注意是整个批次一起失败，不是只丢这一行。
// 日志入库、RSS 抓取这类"整批写入"路径上，这等于一次坏数据带走一批好数据。
//
// 直接 `len(s) > n` 判断然后用 `s[:n]` 截断的代码，即使不切断字符，也
// 会把 N 个字符错当成 N 字节：中文内容的实际字符数远小于 N，看起来是
// "安全"的，实际是提前截断导致信息损失。

// TruncateRunes 把 s 截断到最多 maxRunes 个字符（rune）。
//
// 截断点一定落在 rune 边界上。输入含非法 UTF-8 字节时，非法字节会被
// 替换为 U+FFFD，保证返回值总能存入 PostgreSQL 的 UTF8 列。
//
// 为什么要修复输入而不是只保证截断点：本函数用于清洗外部数据，而外部
// 数据自身就可能带着被硬切的字节——例如 rss_remote_fetcher 用
// io.LimitReader(body, 512) 读错误页，512 字节很可能把一个中文字符
// 切成两半。那种字符串本来就不能入库，若这里不过滤，调用方会把
// "截断成功"当成"可以写入"，结果整条 UPDATE 被数据库拒收。
func TruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	// 合法输入直接返回原串（ToValidUTF8 内部无替换时不分配）。
	// 非法输入替换掉坏字节：非法序列在 Go 中每个坏字节算一个 rune，
	// 不处理的话字符数校验会通过，但数据库层仍会拒收。
	s = strings.ToValidUTF8(s, "\uFFFD")

	// 快速路径：字节数不超过 maxRunes 时，字符数必然也不超过 maxRunes。
	if len(s) <= maxRunes {
		return s
	}
	n := 0
	for i := range s {
		if n == maxRunes {
			return s[:i]
		}
		n++
	}
	return s
}

// TruncateEllipsis 按字符截断，被截断时追加 "..."，总长不超过 maxRunes。
//
// 用于展示型文本（列表预览、错误摘要）：让读者看出"这里被截断了"。
// 不要用于唯一键或需要还原的 URL —— 省略号会让值本身失真。
func TruncateEllipsis(s string, maxRunes int) string {
	if maxRunes <= 3 {
		return TruncateRunes(s, maxRunes)
	}
	// 先清洗：非法字节若不替换，未超长的快速路径会把它们原样交回
	// 调用方，数据库层照样拒收。
	s = strings.ToValidUTF8(s, "\uFFFD")
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return TruncateRunes(s, maxRunes-3) + "..."
}

// pgBTreeMaxKeyBytes 是截断带 btree 唯一索引的字符串列时使用的字节上限。
//
// 依据：PostgreSQL 默认 8KB 页面下，btree 索引单条记录上限约 2.7KB，
// 超限会报
//
//	ERROR: index row size ... exceeds btree version 4 maximum 2704 for index
//
// 2704 是报错里给出的上限值，其中还包含索引元组头开销，键值本身可用
// 空间略小于该值，因此这里取 2600 留出余量。
//
// 这直接影响 varchar(2048) 上的唯一索引（rss_entries.link、
// rss_feeds.url）：2048 个 ASCII 字符 = 2048 字节，安全；但
// 2048 个中文字符 = 6144 字节，远超上限，插入会失败。
const pgBTreeMaxKeyBytes = 2600

// TruncateIndexed 是为"带 btree 唯一索引的 varchar 列"准备的截断。
//
// 同时满足两个约束：
//   - 字符数 <= maxRunes（列定义的 varchar(N)）
//   - 字节数 <= pgBTreeMaxKeyBytes（btree 索引单键上限）
//
// 与 TruncateEllipsis 不同，这里**不追加省略号**：被截断的值仍要当作
// 数据使用（URL、唯一键），追加 "..." 只会让不同长 URL 更容易撞成同一个
// 截断结果，并制造打不开的链接。
//
// 注意：截断后的 URL 已不可还原，这本身就是有损操作。根治办法是让唯一
// 键改用内容哈希（见 docs/tables.md 的架构建议），截断只作为兜底。
func TruncateIndexed(s string, maxRunes int) string {
	s = TruncateRunes(s, maxRunes)
	if len(s) <= pgBTreeMaxKeyBytes {
		return s
	}
	i := pgBTreeMaxKeyBytes
	// 从字节上限处向前回退到最近的 rune 起始字节。
	// TruncateRunes 已保证合法 UTF-8，所以这里必定能找到一个起点。
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
}
