package assistant

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"
)

func notificationEvent(account State, n Notification, created time.Time, unread int) Event {
	title, body, kind := notificationPresentation(n, account.Username)
	sum := sha256.Sum256(fmt.Appendf(nil, "v2ex:%d:%d", account.AccountID, n.ID))
	avatar := ""
	for _, value := range []string{n.Member.AvatarLarge, n.Member.AvatarNormal, n.Member.AvatarMini} {
		if len(value) <= 1024 {
			avatar = normalizeAvatarURL(value)
			// URL escaping can grow the value; reject paths browsers would
			// canonicalize outside the avatar endpoint before batching it.
			if parsed, err := url.Parse(avatar); err != nil || len(avatar) > 1024 || path.Clean(parsed.Path) != parsed.Path {
				avatar = ""
			}
		}
		if avatar != "" {
			break
		}
	}
	return Event{EventID: hex.EncodeToString(sum[:]), Type: "notification", DetailsVersion: 1,
		SourceAccountID: account.AccountID, NotificationID: n.ID, NotificationKind: kind, Title: title, Body: body,
		AvatarURL: avatar, UnreadCount: unread, CreatedAt: created.UTC(), ExpiresAt: created.UTC().Add(24 * time.Hour)}
}

// Classify the action around the topic link, never words inside the topic or
// reply. Both API HTML and notification-page HTML use these link boundaries.
func notificationPresentation(n Notification, self string) (string, string, string) {
	doc, _ := html.Parse(strings.NewReader(n.Text))
	actor := n.Member.Username
	var topic, action strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" {
			path := notificationLinkPath(node)
			if strings.HasPrefix(path, "/t/") {
				topic.WriteString(nodeText(node))
				return
			}
			if strings.HasPrefix(path, "/member/") {
				if actor == "" {
					actor = strings.TrimPrefix(path, "/member/")
				}
				return
			}
		}
		if node.Type == html.TextNode {
			action.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	if doc != nil {
		walk(doc)
	}
	a := strings.ToLower(strings.Join(strings.Fields(action.String()), " "))
	topicTitle := strings.Join(strings.Fields(topic.String()), " ")
	payload := n.PayloadRendered
	if strings.TrimSpace(payload) == "" && n.Payload != nil {
		payload = *n.Payload
	}
	verb, body, kind := "有新的动态", notificationPlain(payload, ""), "other"
	switch {
	case strings.Contains(a, "感谢") || strings.Contains(a, "thank"):
		if strings.Contains(a, "回复") || strings.Contains(a, "reply") {
			verb, kind = "感谢了你的回复", "thanks_reply"
		} else {
			verb, body, kind = "感谢了你的主题", topicTitle, "thanks_topic"
		}
	case strings.Contains(a, "收藏") || strings.Contains(a, "favorit") || strings.Contains(a, "favourit") || strings.Contains(a, "bookmark"):
		verb, body, kind = "收藏了你的主题", topicTitle, "favorite"
	case strings.Contains(a, "提到") || strings.Contains(a, "提及") || strings.Contains(a, "mention"):
		verb, body, kind = "提到了你", notificationPlain(payload, self), "mention"
	case strings.Contains(a, "回复") || strings.Contains(a, "replied"):
		verb, kind = "回复了你的主题", "reply"
	default:
		body = cmp.Or(body, notificationPlain(n.Text, ""))
	}
	body = cmp.Or(body, topicTitle, "打开应用查看详情")
	actor = cmp.Or(strings.Join(strings.Fields(actor), " "), "有人")
	// A short, Unicode-safe excerpt; the OS owns wrapping and visible line count.
	return truncate(actor+" "+verb, 100), truncate(body, 180), kind
}

func notificationLinkPath(node *html.Node) string {
	for _, attr := range node.Attr {
		if attr.Key != "href" {
			continue
		}
		u, err := url.Parse(attr.Val)
		if err != nil || u.User != nil {
			return ""
		}
		host := strings.ToLower(u.Hostname())
		if host != "" && host != "v2ex.com" && host != "www.v2ex.com" {
			return ""
		}
		return u.Path
	}
	return ""
}

func nodeText(node *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(node)
	return b.String()
}

func notificationPlain(source, self string) string {
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return ""
	}
	var parts []string
	var visit func(*html.Node, bool)
	visit = func(n *html.Node, code bool) {
		if n.Type == html.ElementNode {
			if n.Data == "script" || n.Data == "style" {
				return
			}
			code = code || n.Data == "code" || n.Data == "pre"
			if self != "" && !code && n.Data == "a" && strings.EqualFold(notificationLinkPath(n), "/member/"+self) &&
				((len(parts) > 0 && strings.HasSuffix(parts[len(parts)-1], "@")) || strings.EqualFold(nodeText(n), "@"+self)) {
				// V2EX renders @ outside the member anchor.
				if len(parts) > 0 {
					parts[len(parts)-1] = strings.TrimSuffix(parts[len(parts)-1], "@")
				}
				return
			}
			if n.Data == "br" || n.Data == "p" || n.Data == "div" || n.Data == "li" || n.Data == "pre" {
				parts = append(parts, " ")
			}
			if n.Data == "img" {
				parts = append(parts, " [图片] ")
				return
			}
		}
		if n.Type == html.TextNode {
			text := n.Data
			if !code && self != "" {
				text = removeSelfMention(text, self)
			}
			parts = append(parts, text)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c, code)
		}
		if n.Type == html.ElementNode && (n.Data == "p" || n.Data == "div" || n.Data == "li" || n.Data == "pre") {
			parts = append(parts, " ")
		}
	}
	visit(doc, false)
	return strings.Join(strings.Fields(strings.Join(parts, "")), " ")
}

func removeSelfMention(text, self string) string {
	runes, username := []rune(text), []rune(self)
	var out strings.Builder
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
	for i := 0; i < len(runes); {
		end := i + 1 + len(username)
		if runes[i] == '@' && end <= len(runes) &&
			(i == 0 || (!word(runes[i-1]) && !strings.ContainsRune(".+-@", runes[i-1]))) &&
			(end == len(runes) || !word(runes[end])) && strings.EqualFold(string(runes[i+1:end]), self) {
			i = end
			continue
		}
		out.WriteRune(runes[i])
		i++
	}
	return out.String()
}
