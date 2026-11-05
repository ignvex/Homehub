package service

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ignvex/HomeHub/internal/model"
)

// ============================================================
// OPML 导入导出
// ============================================================
//
// OPML 2.0（http://opml.org/spec2.opml）。订阅源 outline 带 type="rss"
// 与 xmlUrl；分组是不带 xmlUrl、但有子节点的 outline。

// OPML 导入限制。
const (
	// MaxOPMLBytes 是导入文件的大小上限。几千个订阅源的 OPML 也只有几百 KB。
	MaxOPMLBytes = 2 << 20
	// maxOPMLFeeds 是单次导入处理的订阅源 outline 上限，超出部分拒绝整次导入：
	// 只导一半会让人以为全部导入了。
	maxOPMLFeeds = 2000
)

// OPML 导入的调用方错误，handler 据此回 400。
var (
	ErrOPMLTooLarge   = fmt.Errorf("OPML 文件不能超过 %d MB", MaxOPMLBytes>>20)
	ErrOPMLInvalid    = errors.New("无法解析 OPML 文件")
	ErrOPMLTooManyURL = fmt.Errorf("OPML 中的订阅源超过 %d 个", maxOPMLFeeds)
)

type opmlDoc struct {
	XMLName xml.Name `xml:"opml"`
	Version string   `xml:"version,attr"`
	Head    opmlHead `xml:"head"`
	Body    opmlBody `xml:"body"`
}

type opmlHead struct {
	Title       string `xml:"title"`
	DateCreated string `xml:"dateCreated,omitempty"`
}

type opmlBody struct {
	Outlines []opmlOutline `xml:"outline"`
}

type opmlOutline struct {
	Text     string        `xml:"text,attr"`
	Title    string        `xml:"title,attr,omitempty"`
	Type     string        `xml:"type,attr,omitempty"`
	XMLURL   string        `xml:"xmlUrl,attr,omitempty"`
	HTMLURL  string        `xml:"htmlUrl,attr,omitempty"`
	Outlines []opmlOutline `xml:"outline"`
}

// feedOutline 把订阅源转成 outline。text 是必填属性，标题为空时退回 URL。
func feedOutline(f model.RSSFeed) opmlOutline {
	text := f.Title
	if text == "" {
		text = f.URL
	}
	return opmlOutline{Text: text, Title: text, Type: "rss", XMLURL: f.URL, HTMLURL: f.SiteURL}
}

// ExportOPML 导出全部订阅源为 OPML 2.0。
//
// 分组按 sort_order 嵌套成一级 outline，未分组（或分组已被删除）的
// 订阅源放在顶层，排在分组之后。
func (s *RSSService) ExportOPML() ([]byte, error) {
	groups, err := s.ListGroups()
	if err != nil {
		return nil, err
	}
	var feeds []model.RSSFeed
	if err := s.db.Order("id ASC").Find(&feeds).Error; err != nil {
		return nil, fmt.Errorf("查询订阅源失败: %w", err)
	}

	byGroup := make(map[uint64][]opmlOutline, len(groups))
	known := make(map[uint64]bool, len(groups))
	for _, g := range groups {
		known[g.ID] = true
	}
	var top []opmlOutline
	for _, f := range feeds {
		if f.GroupID != nil && known[*f.GroupID] {
			byGroup[*f.GroupID] = append(byGroup[*f.GroupID], feedOutline(f))
			continue
		}
		top = append(top, feedOutline(f))
	}

	body := make([]opmlOutline, 0, len(groups)+len(top))
	for _, g := range groups {
		body = append(body, opmlOutline{Text: g.Name, Title: g.Name, Outlines: byGroup[g.ID]})
	}
	body = append(body, top...)

	doc := opmlDoc{
		Version: "2.0",
		Head:    opmlHead{Title: "HomeHub feeds", DateCreated: time.Now().UTC().Format(time.RFC1123Z)},
		Body:    opmlBody{Outlines: body},
	}
	out, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("生成 OPML 失败: %w", err)
	}
	return append([]byte(xml.Header), append(out, '\n')...), nil
}

// OPMLImportError 是单条订阅源导入失败的原因。
type OPMLImportError struct {
	URL    string `json:"url"`
	Reason string `json:"reason"`
}

// OPMLImportResult 是 OPML 导入的汇总。
type OPMLImportResult struct {
	// Created 新建的订阅源数。
	Created int `json:"created"`
	// Skipped 因 URL 已存在（库里或文件内重复）而跳过的数目。
	Skipped int `json:"skipped"`
	// GroupsCreated 新建的分组数。
	GroupsCreated int `json:"groups_created"`
	// Errors 校验或写入失败的条目；不影响其余条目。
	Errors []OPMLImportError `json:"errors"`
}

// opmlFeed 是从 OPML 中摊平出来的一条订阅源。
type opmlFeed struct {
	url, title, group string
}

// flattenOPML 深度优先摊平 outline 树。
//
// 分组名取最近一级「带子节点的 outline」的 text（无 text 时用 title）：
// 嵌套多层文件夹时只保留最内层，因为 RSS 分组只有一级。
func flattenOPML(outlines []opmlOutline, group string, out *[]opmlFeed) error {
	for _, o := range outlines {
		if u := strings.TrimSpace(o.XMLURL); u != "" {
			if len(*out) >= maxOPMLFeeds {
				return ErrOPMLTooManyURL
			}
			title := strings.TrimSpace(o.Title)
			if title == "" {
				title = strings.TrimSpace(o.Text)
			}
			*out = append(*out, opmlFeed{url: u, title: title, group: group})
		}
		if len(o.Outlines) > 0 {
			name := strings.TrimSpace(o.Text)
			if name == "" {
				name = strings.TrimSpace(o.Title)
			}
			if name == "" {
				name = group
			}
			if err := flattenOPML(o.Outlines, name, out); err != nil {
				return err
			}
		}
	}
	return nil
}

// ImportOPML 从 r 读取 OPML 并创建订阅源。
//
// 规则：
//   - 读入上限 MaxOPMLBytes，超出返回 ErrOPMLTooLarge
//   - 每条 URL 走 CreateFeed 的同一套校验（SSRF、协议），失败记入 Errors
//   - 按 URL 去重：库里已有或同一文件里重复出现都计入 Skipped
//   - 分组按名字匹配已有分组，不存在则创建
//   - **不抓取**：与 CreateFeed 的约定一致，新源由后台调度抓
//
// 不包成一个事务：单条失败（URL 非法）不该让其余几百条一起回滚。
func (s *RSSService) ImportOPML(r io.Reader) (*OPMLImportResult, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxOPMLBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取 OPML 失败: %w", err)
	}
	if len(data) > MaxOPMLBytes {
		return nil, ErrOPMLTooLarge
	}

	var doc opmlDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOPMLInvalid, err)
	}
	var items []opmlFeed
	if err := flattenOPML(doc.Body.Outlines, "", &items); err != nil {
		return nil, err
	}

	res := &OPMLImportResult{Errors: []OPMLImportError{}}
	if len(items) == 0 {
		return res, nil
	}

	var existing []string
	if err := s.db.Model(&model.RSSFeed{}).Pluck("url", &existing).Error; err != nil {
		return nil, fmt.Errorf("查询已有订阅源失败: %w", err)
	}
	seen := make(map[string]bool, len(existing)+len(items))
	for _, u := range existing {
		seen[u] = true
	}

	groups, err := s.ListGroups()
	if err != nil {
		return nil, err
	}
	groupIDs := make(map[string]uint64, len(groups))
	for _, g := range groups {
		if _, dup := groupIDs[g.Name]; !dup {
			groupIDs[g.Name] = g.ID
		}
	}

	for _, it := range items {
		if seen[it.url] {
			res.Skipped++
			continue
		}
		if err := validateFeedURL(it.url); err != nil {
			res.Errors = append(res.Errors, OPMLImportError{URL: it.url, Reason: err.Error()})
			continue
		}

		var gid *uint64
		if it.group != "" {
			name := model.TruncateRunes(it.group, 100)
			id, ok := groupIDs[name]
			if !ok {
				g, err := s.CreateGroup(name)
				if err != nil {
					res.Errors = append(res.Errors, OPMLImportError{URL: it.url, Reason: err.Error()})
					continue
				}
				id = g.ID
				groupIDs[name] = id
				res.GroupsCreated++
			}
			gid = &id
		}

		title := it.title
		if _, err := s.CreateFeed(CreateFeedInput{URL: it.url, Title: &title, GroupID: gid}); err != nil {
			if errors.Is(err, ErrRSSFeedExists) {
				res.Skipped++
			} else {
				res.Errors = append(res.Errors, OPMLImportError{URL: it.url, Reason: err.Error()})
			}
			seen[it.url] = true
			continue
		}
		seen[it.url] = true
		res.Created++
	}
	return res, nil
}
