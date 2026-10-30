package service

import (
	"testing"
)

// TestAvatarETagChangesWithContent 锁住一条回归：
// 上游换了头像、而 variant 与邮箱都没变时，ETag 必须跟着变。
//
// 否则浏览器带 If-None-Match 重验会持续拿到 304，永远显示旧图，
// 而服务端已返回 updated:true —— 表现为"刷新成功但没效果"。
func TestAvatarETagChangesWithContent(t *testing.T) {
	const variant = "official|256|identicon|g"
	const emailHash = "d41d8cd98f00b204e9800998ecf8427e"

	oldImg := []byte("\x89PNG-old-avatar-bytes")
	newImg := []byte("\x89PNG-new-avatar-bytes")

	oldTag := avatarETag(variant, emailHash, oldImg)
	newTag := avatarETag(variant, emailHash, newImg)

	if oldTag == newTag {
		t.Fatalf("图片内容变化后 ETag 未变（%s），客户端将永远 304 旧图", oldTag)
	}
}

// TestAvatarETagStableForSameInput 确认同一份输入必须产出同一个 ETag，
// 否则每次请求都会重新下载、缓存完全失效。
func TestAvatarETagStableForSameInput(t *testing.T) {
	img := []byte("\x89PNG-same-bytes")

	a := avatarETag("v1", "hash1", img)
	b := avatarETag("v1", "hash1", img)
	if a != b {
		t.Fatalf("同一输入产出不同 ETag: %s vs %s", a, b)
	}

	// 内容相同但 variant 不同，必须区分（不同尺寸是不同的图）。
	if c := avatarETag("v2", "hash1", img); c == a {
		t.Error("variant 变化后 ETag 未变")
	}
	// 邮箱变化同理。
	if d := avatarETag("v1", "hash2", img); d == a {
		t.Error("邮箱哈希变化后 ETag 未变")
	}
}

// TestAvatarETagIsQuoted 确认符合 RFC 7232 的实体标签语法。
func TestAvatarETagIsQuoted(t *testing.T) {
	tag := avatarETag("v", "h", []byte("x"))
	if len(tag) < 3 || tag[0] != '"' || tag[len(tag)-1] != '"' {
		t.Fatalf("ETag 未加引号: %q", tag)
	}
}

// TestAvatarETagNoDelimiterCollision 确认拼接分隔符不会被内容伪造。
//
// 若用裸字符串拼接 "a|b" + "c" 与 "a" + "b|c" 会得到同一个输入，
// 这里用 0 字节分隔符 + 分别写入，避免这种歧义。
func TestAvatarETagNoDelimiterCollision(t *testing.T) {
	img := []byte("data")
	x := avatarETag("a|b", "c", img)
	y := avatarETag("a", "b|c", img)
	if x == y {
		t.Fatal("分隔符歧义导致不同输入产出相同 ETag")
	}
}

// TestAvatarETagEmptyData 确认空数据不 panic。
func TestAvatarETagEmptyData(t *testing.T) {
	tag := avatarETag("v", "h", nil)
	if tag == "" {
		t.Fatal("空数据时 ETag 不应为空串")
	}
	if got := avatarETag("v", "h", []byte{}); got != tag {
		t.Errorf("nil 与空切片应等价: %q vs %q", got, tag)
	}
}
