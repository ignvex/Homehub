package service

import (
	"bytes"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"strings"

	"github.com/ignvex/HomeHub/internal/model"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"gorm.io/gorm"
)

// ============================================================
// 书签导入导出（Netscape Bookmark File 格式）
// ============================================================
//
// 所有主流浏览器都能导出/导入这种格式：<DL> 列表里 <DT><H3> 是文件夹，
// <DT><A HREF ADD_DATE> 是书签。它不是合法 HTML（<p> 与 <DT> 从不闭合），
// 所以用 x/net/html 的 Tokenizer 逐个看标签，而不是建 DOM 树——
// html.Parse 会按 HTML5 规则「修复」嵌套，把文件夹层级改得面目全非。

// 书签导入限制。
const (
	// MaxBookmarkImportBytes 是导入文件大小上限。
	MaxBookmarkImportBytes = 5 << 20
	// maxBookmarkImport 是单次导入的书签数量上限。
	maxBookmarkImport = 20000
	// defaultImportGroup 承接不在任何文件夹里的书签。
	defaultImportGroup = "导入的书签"
	// bookmarkFolderSep 拼接嵌套文件夹名（书签分组只有一级）。
	bookmarkFolderSep = "/"
	// maxBookmarkFolderDepth 是文件夹嵌套层数上限（不带标题的 <DL> 也算一层）。
	// 浏览器里真实的收藏夹很少超过十层；不设上限时，构造的深层文件
	// 能让解析阶段分配到 GB 级内存。
	maxBookmarkFolderDepth = 32
	// bookmarkGroupNameMaxRunes 与 model.BookmarkGroup.Name 的 size 一致，
	// 由 TestBookmarkGroupNameMaxMatchesColumn 锁住。
	bookmarkGroupNameMaxRunes = 100
)

// 书签导入的调用方错误，handler 据此回 400。
var (
	ErrBookmarkImportTooLarge = fmt.Errorf("书签文件不能超过 %d MB", MaxBookmarkImportBytes>>20)
	ErrBookmarkImportTooMany  = fmt.Errorf("书签文件中的书签超过 %d 个", maxBookmarkImport)
	ErrBookmarkImportTooDeep  = fmt.Errorf("书签文件夹嵌套超过 %d 层", maxBookmarkFolderDepth)
)

// ExportBookmarksHTML 导出全部书签为 Netscape Bookmark File。
//
// 每个分组一个 <H3> 文件夹，按分组与书签的显示顺序排列。
// 所有文本与属性都经过 HTML 转义。
func (s *BookmarkService) ExportBookmarksHTML() ([]byte, error) {
	groups, err := s.ListBookmarks()
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	b.WriteString("<!DOCTYPE NETSCAPE-Bookmark-file-1>\n")
	b.WriteString("<!-- This is an automatically generated file. Exported by HomeHub. -->\n")
	b.WriteString(`<META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">` + "\n")
	b.WriteString("<TITLE>Bookmarks</TITLE>\n<H1>Bookmarks</H1>\n<DL><p>\n")
	for _, g := range groups {
		fmt.Fprintf(&b, "    <DT><H3 ADD_DATE=\"%d\">%s</H3>\n    <DL><p>\n",
			g.CreatedAt.Unix(), stdhtml.EscapeString(g.Name))
		for _, bm := range g.Bookmarks {
			fmt.Fprintf(&b, "        <DT><A HREF=\"%s\" ADD_DATE=\"%d\"", stdhtml.EscapeString(bm.URL), bm.CreatedAt.Unix())
			if bm.Icon != "" && strings.HasPrefix(strings.ToLower(bm.Icon), "http") {
				fmt.Fprintf(&b, " ICON_URI=\"%s\"", stdhtml.EscapeString(bm.Icon))
			}
			fmt.Fprintf(&b, ">%s</A>\n", stdhtml.EscapeString(bm.Title))
		}
		b.WriteString("    </DL><p>\n")
	}
	b.WriteString("</DL><p>\n")
	return b.Bytes(), nil
}

// parsedBookmark 是从文件里解析出的一条书签。
type parsedBookmark struct {
	group, title, url string
}

// parseNetscapeBookmarks 用 Tokenizer 宽松解析书签文件。
//
// 文件夹层级靠 <DL> 的开闭维护：遇到 <H3> 记下名字，紧随其后的 <DL>
// 把它压栈，对应的 </DL> 弹栈。缺了闭合标签也只是让后面的书签归到
// 更深一层，不会解析失败——浏览器导出的文件经常不完全规整。
//
// 栈里存的是每一层拍平后的完整分组名，压栈时增量拼接并立即截断到列宽；
// 嵌套超过 maxBookmarkFolderDepth 层返回 ErrBookmarkImportTooDeep。
// 两者一起保证解析内存与文件大小同阶，不随「层数 × 书签数」膨胀。
func parseNetscapeBookmarks(r io.Reader) ([]parsedBookmark, error) {
	z := html.NewTokenizer(r)
	var (
		stack      []string // 每层拍平并截断后的分组名，栈顶即当前分组
		pending    *string  // 刚读到、尚未遇到 <DL> 的 H3 名称
		inH3, inA  bool
		text       strings.Builder
		href       string
		out        []parsedBookmark
		rootOpened bool
	)
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if errors.Is(z.Err(), io.EOF) {
				return out, nil
			}
			return nil, z.Err()
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			switch atom.Lookup(name) {
			case atom.Dl:
				if pending == nil && !rootOpened {
					// 最外层 <DL> 是根，不算文件夹。
					rootOpened = true
					break
				}
				// 没有标题的嵌套列表同样压栈（沿用上一级的名字，保持开闭配对），
				// 也同样计入深度——否则只写 <DL> 就能绕过上限。
				if len(stack) >= maxBookmarkFolderDepth {
					return nil, ErrBookmarkImportTooDeep
				}
				seg := ""
				if pending != nil {
					seg = *pending
					pending = nil
				}
				stack = append(stack, childFolderName(currentFolder(stack), seg))
			case atom.H3:
				inH3 = true
				text.Reset()
			case atom.A:
				inA = true
				text.Reset()
				href = ""
				for hasAttr {
					var k, v []byte
					k, v, hasAttr = z.TagAttr()
					if strings.EqualFold(string(k), "href") {
						href = string(v)
					}
				}
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			switch atom.Lookup(name) {
			case atom.Dl:
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				pending = nil
			case atom.H3:
				if inH3 {
					n := strings.TrimSpace(text.String())
					pending = &n
					inH3 = false
				}
			case atom.A:
				if inA {
					inA = false
					if len(out) >= maxBookmarkImport {
						return nil, ErrBookmarkImportTooMany
					}
					out = append(out, parsedBookmark{
						group: currentFolder(stack),
						title: strings.TrimSpace(text.String()),
						url:   strings.TrimSpace(href),
					})
				}
			}
		case html.TextToken:
			if inH3 || inA {
				text.Write(z.Text())
			}
		}
	}
}

// currentFolder 返回当前所在的分组名，不在任何文件夹里时为空。
func currentFolder(stack []string) string {
	if len(stack) == 0 {
		return ""
	}
	return stack[len(stack)-1]
}

// childFolderName 把子文件夹名接到父分组名后（「父/子」，空段跳过），
// 并截断到分组名列宽。
//
// 父名已经截断过，先截断子段再拼接，结果与「整条路径拼好再截断」一致，
// 但单次分配不超过两倍列宽。
func childFolderName(parent, seg string) string {
	seg = model.TruncateRunes(seg, bookmarkGroupNameMaxRunes)
	switch {
	case seg == "":
		return parent
	case parent == "":
		return seg
	default:
		return model.TruncateRunes(parent+bookmarkFolderSep+seg, bookmarkGroupNameMaxRunes)
	}
}

// BookmarkImportResult 是书签导入的汇总。
type BookmarkImportResult struct {
	// Created 新建的书签数。
	Created int `json:"created"`
	// Skipped 跳过的书签数：同组同 URL 已存在、文件内重复、非 http/https、URL 过长。
	Skipped int `json:"skipped"`
	// GroupsCreated 新建的分组数。
	GroupsCreated int `json:"groups_created"`
}

// ImportBookmarksHTML 从 Netscape Bookmark File 导入书签。
//
// 规则：
//   - <H3> 文件夹作为分组名，嵌套文件夹拍平成「父/子」，按 rune 截断到 100
//   - 不在任何文件夹里的书签归入「导入的书签」
//   - 分组按名字匹配已有分组，不存在则创建
//   - 同组同 URL 去重（含库里已有的），只接受 http/https
//
// 整次导入一个事务：书签是一批一起出现在界面上的，中途失败留下半份
// 比全部回滚更难收拾。
func (s *BookmarkService) ImportBookmarksHTML(r io.Reader) (*BookmarkImportResult, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxBookmarkImportBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取书签文件失败: %w", err)
	}
	if len(data) > MaxBookmarkImportBytes {
		return nil, ErrBookmarkImportTooLarge
	}
	items, err := parseNetscapeBookmarks(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	res := &BookmarkImportResult{}
	if len(items) == 0 {
		return res, nil
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		var groups []model.BookmarkGroup
		if err := tx.Order("id ASC").Find(&groups).Error; err != nil {
			return fmt.Errorf("查询分组失败: %w", err)
		}
		groupIDs := make(map[string]uint64, len(groups))
		for _, g := range groups {
			if _, dup := groupIDs[g.Name]; !dup {
				groupIDs[g.Name] = g.ID
			}
		}

		var existing []model.Bookmark
		if err := tx.Select("group_id", "url").Find(&existing).Error; err != nil {
			return fmt.Errorf("查询已有书签失败: %w", err)
		}
		type key struct {
			group uint64
			url   string
		}
		seen := make(map[key]bool, len(existing)+len(items))
		for _, b := range existing {
			seen[key{b.GroupID, b.URL}] = true
		}

		var maxGroupOrder int
		if err := tx.Model(&model.BookmarkGroup{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxGroupOrder).Error; err != nil {
			return fmt.Errorf("查询分组排序失败: %w", err)
		}
		nextOrder := map[uint64]int{}

		var batch []model.Bookmark
		for _, it := range items {
			if validateBookmarkURL(it.url) != nil || len([]rune(it.url)) > 2048 {
				res.Skipped++
				continue
			}
			name := it.group
			if name == "" {
				name = defaultImportGroup
			}
			name = model.TruncateRunes(name, bookmarkGroupNameMaxRunes)

			gid, ok := groupIDs[name]
			if !ok {
				maxGroupOrder++
				g := model.BookmarkGroup{Name: name, SortOrder: maxGroupOrder}
				if err := tx.Create(&g).Error; err != nil {
					return fmt.Errorf("创建分组失败: %w", err)
				}
				gid = g.ID
				groupIDs[name] = gid
				nextOrder[gid] = 0
				res.GroupsCreated++
			}
			k := key{gid, it.url}
			if seen[k] {
				res.Skipped++
				continue
			}
			seen[k] = true

			order, ok := nextOrder[gid]
			if !ok {
				if err := tx.Model(&model.Bookmark{}).Where("group_id = ?", gid).
					Select("COALESCE(MAX(sort_order), 0)").Scan(&order).Error; err != nil {
					return fmt.Errorf("查询书签排序失败: %w", err)
				}
			}
			order++
			nextOrder[gid] = order

			title := it.title
			if title == "" {
				title = it.url
			}
			batch = append(batch, model.Bookmark{
				GroupID:   gid,
				Title:     model.TruncateRunes(title, 255),
				URL:       it.url,
				SortOrder: order,
			})
		}
		if len(batch) > 0 {
			if err := tx.CreateInBatches(&batch, 500).Error; err != nil {
				return fmt.Errorf("写入书签失败: %w", err)
			}
		}
		res.Created = len(batch)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}
