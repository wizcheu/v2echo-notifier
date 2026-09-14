package assistant

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type WebUnreadSnapshot struct {
	Count      int
	ObservedAt time.Time
}

type webRequestError struct {
	message string
	status  int
	retry   time.Duration
	auth    bool
}

func (e *webRequestError) Error() string { return e.message }

// The stored Cookie is sent only to the homepage, with no cookie jar, redirects
// or notification-list reads.
func (e *Engine) readWebUnread(ctx context.Context, cookie, username string) (WebUnreadSnapshot, error) {
	cookie = strings.TrimSpace(cookie)
	if strings.HasPrefix(strings.ToLower(cookie), "cookie:") {
		cookie = strings.TrimSpace(cookie[len("cookie:"):])
	}
	if cookie == "" || len(cookie) > 16384 || strings.ContainsAny(cookie, "\r\n\x00") {
		return WebUnreadSnapshot{}, errors.New("请填写同账号的网页 Cookie（单行 Cookie 请求头内容）")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.v2ex.com/", nil)
	if err != nil {
		return WebUnreadSnapshot{}, err
	}
	req.Header.Set("Cookie", cookie)
	valid := false
	for _, c := range req.Cookies() {
		if c.Name == "A2" && c.Value != "" {
			valid = true
		}
	}
	if !valid {
		return WebUnreadSnapshot{}, errors.New("Cookie 缺少有效的 A2 登录凭据，请重新复制")
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "Mozilla/5.0 V2Echo-Notifier/0.1")
	resp, err := e.Web.Do(req)
	if err != nil {
		return WebUnreadSnapshot{}, errors.New("无法读取 V2EX 首页，请检查服务器网络后重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || strings.EqualFold(resp.Header.Get("cf-mitigated"), "challenge") {
		return WebUnreadSnapshot{}, &webRequestError{message: "V2EX 首页未通过登录或访问验证，请更新 Cookie 或检查服务器出口后重试", status: resp.StatusCode, retry: retryAfter(resp.Header, time.Now()), auth: resp.StatusCode == 401}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return WebUnreadSnapshot{}, errors.New("V2EX 首页读取不完整，请重试")
	}
	count, err := parseWebUnread(string(raw), username)
	if err != nil {
		err = &webRequestError{message: err.Error(), auth: true}
	}
	return WebUnreadSnapshot{Count: count, ObservedAt: time.Now().UTC()}, err
}

var leadingCount = regexp.MustCompile(`^\s*(\d+)`)
var webUsername = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func parseWebUnread(source, expected string) (int, error) {
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return 0, errors.New("无法解析 V2EX 首页")
	}
	actual, count, invalid := "", -1, false
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			href, class := "", ""
			for _, attr := range n.Attr {
				if attr.Key == "href" {
					href = attr.Val
				}
				if attr.Key == "class" {
					class = attr.Val
				}
			}
			u, err := url.Parse(href)
			if err == nil && u.User == nil && (u.Host == "" || strings.EqualFold(u.Host, "www.v2ex.com") || strings.EqualFold(u.Host, "v2ex.com")) &&
				(u.Scheme == "" || u.Scheme == "https" || u.Scheme == "http") {
				top := false
				for _, token := range strings.Fields(class) {
					if token == "top" {
						top = true
					}
				}
				if top && strings.HasPrefix(u.Path, "/member/") {
					name := strings.TrimPrefix(u.Path, "/member/")
					if webUsername.MatchString(name) {
						if actual != "" && !strings.EqualFold(actual, name) {
							invalid = true
						}
						actual = name
					}
				}
				if u.Path == "/notifications" {
					var text strings.Builder
					var collect func(*html.Node)
					collect = func(n *html.Node) {
						if n.Type == html.TextNode {
							text.WriteString(n.Data)
						}
						for c := n.FirstChild; c != nil; c = c.NextSibling {
							collect(c)
						}
					}
					collect(n)
					if m := leadingCount.FindStringSubmatch(text.String()); len(m) == 2 {
						value, err := strconv.Atoi(m[1])
						if err != nil || value > 1000000 || (count >= 0 && count != value) {
							invalid = true
						} else {
							count = value
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(doc)
	if actual == "" || invalid {
		return 0, errors.New("Cookie 登录状态无法确认，请重新复制已登录账号的 Cookie")
	}
	if !strings.EqualFold(actual, expected) {
		return 0, errors.New("Cookie 与当前配置不属于同一个 V2EX 账号")
	}
	if count < 0 {
		return 0, errors.New("无法读取未读数量，未将缺失计数当作零；请检查 Cookie 或页面访问状态")
	}
	return count, nil
}
