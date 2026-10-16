package assistant

import (
	"strings"
	"testing"
	"time"
)

func TestNotificationPresentationTypesAndMentionCleaning(t *testing.T) {
	topic := `<a href="/t/123#reply2" class="topic-link">主题 &amp; 标题</a>`
	for _, tc := range []struct{ name, action, payload, title, body string }{
		{"reply", "在 " + topic + " 回复了你", "<p>具体<strong>回复</strong> &amp; 内容</p>", "测试用户 回复了你的主题", "具体回复 & 内容"},
		{"mention", "在回复 " + topic + " 时提到了你", `@<a href="/member/tester">tester</a> 你好 @someone @tester2 mail@tester.com`, "测试用户 提到了你", "你好 @someone @tester2 mail@tester.com"},
		{"mention-plain", "在 " + topic + " 中提及了你", "@Tester 你好，@tester！", "测试用户 提到了你", "你好，！"},
		{"thanks-topic", "感谢了你发布的主题 › " + topic, "不应使用主题正文", "测试用户 感谢了你的主题", "主题 & 标题"},
		{"thanks-topic-variant", "感谢了你的主题 › " + topic, "", "测试用户 感谢了你的主题", "主题 & 标题"},
		{"thanks-reply", "感谢了你在主题 › " + topic + " 里的回复", "<p>被感谢的回复</p><script>secret()</script>", "测试用户 感谢了你的回复", "被感谢的回复"},
		{"favorite", "收藏了你发布的主题 › " + topic, "不应使用主题正文", "测试用户 收藏了你的主题", "主题 & 标题"},
		{"english", "mentioned you in " + topic, "@tester hello", "测试用户 提到了你", "hello"},
		{"profile-link", "mentioned you in " + topic, `<a href="/member/tester">tester</a> 的资料；<a href="/member/tester">@tester</a> 你好`, "测试用户 提到了你", "tester 的资料； 你好"},
		{"code", "在回复 " + topic + " 时提到了你", `<pre><code>@tester</code></pre>@tester 正文`, "测试用户 提到了你", "@tester 正文"},
		{"missing-body", "感谢了你在主题 › " + topic + " 里的回复", "", "测试用户 感谢了你的回复", "主题 & 标题"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := notification(1)
			n.Member.Username = "测试用户"
			n.Text = tc.action
			n.PayloadRendered = tc.payload
			title, body, _ := notificationPresentation(n, "tester")
			if title != tc.title || body != tc.body {
				t.Fatalf("%q / %q", title, body)
			}
		})
	}
	n := notification(2)
	n.Text = `收藏了你的主题 <a href="/t/3">如何回复和感谢</a>`
	if title, _, _ := notificationPresentation(n, "tester"); title != "example 收藏了你的主题" {
		t.Fatal("classified words in topic", title)
	}
}

func TestNotificationEventStableIdentityAvatarAndExcerpt(t *testing.T) {
	n := notification(42)
	n.Member.AvatarLarge = "https://evil.test/avatar/a.png"
	n.Member.AvatarNormal = "//cdn.v2ex.com/avatar/a_normal.png"
	n.PayloadRendered = strings.Repeat("😀", 300)
	st := State{AccountID: 7, Username: "tester"}
	now := time.Now()
	a := notificationEvent(st, n, now, 3)
	n.PayloadRendered = "changed"
	b := notificationEvent(st, n, now.Add(time.Minute), 4)
	if a.EventID != b.EventID || len([]rune(a.Body)) != 181 || a.AvatarURL != "https://cdn.v2ex.com/avatar/a_normal.png" || a.DetailsVersion != 1 {
		t.Fatal(a, b)
	}
	if a.ExpiresAt.Sub(a.CreatedAt) != 24*time.Hour || a.UnreadCount != 3 {
		t.Fatal(a)
	}
	st.AccountID = 8
	if notificationEvent(st, n, now, 3).EventID == a.EventID {
		t.Fatal("cross-account collision")
	}
	for _, invalid := range []string{"https://cdn.v2ex.com/avatar/../other", "https://cdn.v2ex.com/avatar/" + strings.Repeat("中", 200)} {
		n.Member.AvatarLarge = invalid
		if notificationEvent(st, n, now, 3).AvatarURL != "https://cdn.v2ex.com/avatar/a_normal.png" {
			t.Fatal("invalid normalized avatar poisoned an otherwise valid event")
		}
	}
}
