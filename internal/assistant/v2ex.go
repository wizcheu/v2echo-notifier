package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type V2EX struct {
	BaseURL string
	Client  *http.Client
}
type APIError struct {
	Code    int
	Expired bool
}

func (e *APIError) Error() string {
	if e.Expired {
		return "V2EX Token 无效或已过期，请更新后重试"
	}
	return fmt.Sprintf("V2EX 请求未成功（HTTP %d），稍后重试", e.Code)
}

func secureClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}

func (v *V2EX) get(ctx context.Context, token, path string, target any) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "V2Echo-Notifier/0.1")
	resp, err := v.Client.Do(req)
	if err != nil {
		return nil, errors.New("无法连接 V2EX，请检查网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var failure struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&failure)
		return resp.Header, &APIError{resp.StatusCode, resp.StatusCode == 401 || invalidTokenMessage(failure.Message)}
	}
	var envelope struct {
		Success bool            `json:"success"`
		Message string          `json:"message"`
		Result  json.RawMessage `json:"result"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&envelope); err != nil {
		return resp.Header, errors.New("V2EX 返回了无法解析的数据或验证页面")
	}
	if !envelope.Success {
		return resp.Header, &APIError{resp.StatusCode, invalidTokenMessage(envelope.Message)}
	}
	if len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return resp.Header, errors.New("V2EX 响应缺少 result，已停止本次同步")
	}
	switch dst := target.(type) {
	case *Page:
		if err = json.Unmarshal(envelope.Result, &dst.Items); err != nil {
			return resp.Header, errors.New("通知列表格式不正确")
		}
		dst.End = len(dst.Items) == 0
		if m := pageRange.FindStringSubmatch(envelope.Message); len(m) == 4 {
			end, _ := strconv.Atoi(m[2])
			total, _ := strconv.Atoi(m[3])
			dst.End = dst.End || end >= total
		}
	default:
		if err = json.Unmarshal(envelope.Result, target); err != nil {
			return resp.Header, errors.New("账号信息格式不正确")
		}
	}
	return resp.Header, nil
}

func invalidTokenMessage(message string) bool {
	m := strings.ToLower(message)
	return strings.Contains(m, "token") && (strings.Contains(m, "expired") || strings.Contains(m, "invalid") || strings.Contains(m, "revoked"))
}

var pageRange = regexp.MustCompile(`^Notifications\s+(\d+)-(\d+)/(\d+)$`)

type Page struct {
	Items []Notification
	End   bool
}
type Member struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

func plain(source string, limit int) string {
	z := html.NewTokenizer(strings.NewReader(source))
	var b strings.Builder
	for {
		switch z.Next() {
		case html.ErrorToken:
			return truncate(strings.Join(strings.Fields(b.String()), " "), limit)
		case html.TextToken:
			b.Write(z.Text())
		case html.StartTagToken, html.EndTagToken:
			name, _ := z.TagName()
			if string(name) == "br" || string(name) == "p" {
				b.WriteByte(' ')
			}
		}
	}
}
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
