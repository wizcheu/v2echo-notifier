package assistant

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

func normalizeWebCookie(cookie string) (string, error) {
	cookie = strings.TrimSpace(cookie)
	if strings.HasPrefix(strings.ToLower(cookie), "cookie:") {
		cookie = strings.TrimSpace(cookie[7:])
	}
	if cookie == "" || len(cookie) > 16384 || strings.ContainsAny(cookie, "\r\n\x00") {
		return "", errors.New("请填写有效的单行网页 Cookie")
	}
	req, _ := http.NewRequest(http.MethodGet, "https://www.v2ex.com/", nil)
	req.Header.Set("Cookie", cookie)
	for _, c := range req.Cookies() {
		if c.Name == "A2" && c.Value != "" {
			return cookie, nil
		}
	}
	return "", errors.New("Cookie 缺少 A2 登录凭据")
}

// Website counts are authoritative. API first IDs identify the last notification
// snapshot queued for push, including changes caused by deleting the first row.
// No local read-state inference or count arithmetic is performed.
func (e *Engine) stepHybridLocked(ctx context.Context, cfg Config, st State, now time.Time) error {
	if st.AuthBlocked || now.Before(st.NextCheck) || now.Before(st.NextWeb) {
		return nil
	}
	st.NextCheck = now.Add(time.Duration(cfg.IntervalSeconds) * time.Second)
	st.NextWeb = now.Add(3 * time.Minute)
	if err := e.Store.SaveState(st); err != nil {
		return err
	}
	web, err := e.readWebUnread(ctx, cfg.Cookie, st.Username)
	if err != nil {
		st.NextWeb = maxTime(st.NextWeb, now.Add(max(3*time.Minute, backoff(st.Failures+1))))
		return e.recordFailure(st, nil, err, now)
	}
	st.HasWebUnread = true
	st.WebUnreadCount = web.Count
	st.WebObservedAt = web.ObservedAt
	st.Phase = "live"
	st.Page = 1
	if web.Count == 0 {
		st.LastError = ""
		st.Failures = 0
		st.LastSuccess = now
		return e.Store.ImportPageAndUnread(st, nil, nil)
	}
	if now.Before(st.NextAPI) {
		return e.Store.ImportPageAndUnread(st, nil, nil)
	}
	if st.Quota.Exhausted(now) {
		st.NextAPI = st.Quota.Reset.Add(time.Second)
		return e.Store.ImportPageAndUnread(st, nil, nil)
	}
	if e.Budget != nil {
		ok, err := e.Budget.Reserve(now)
		if err != nil {
			return err
		}
		if !ok {
			return e.Store.ImportPageAndUnread(st, nil, nil)
		}
	}
	st.Quota.Remaining--
	st.NextAPI = now.Add(st.Quota.Delay(now))
	if err = e.Store.SaveState(st); err != nil {
		return err
	}
	var page Page
	headers, err := e.requestAPI(ctx, cfg.APIToken, "notifications?p=1", &page, now)
	st.Quota.Observe(headers, now)
	st.NextAPI = now.Add(st.Quota.Delay(now))
	if err != nil {
		return e.recordFailure(st, headers, err, now)
	}
	if len(page.Items) == 0 {
		return e.recordFailure(st, nil, errors.New("网页有未读，但 API 暂未返回通知；保留推送基准等待下次检查"), now)
	}
	for i, n := range page.Items {
		if n.ID <= 0 || n.ForMemberID != st.AccountID || n.Created <= 0 || (i > 0 && page.Items[i-1].ID <= n.ID) {
			return e.recordFailure(st, nil, errors.New("API 通知身份或排序异常，未更新推送基准"), now)
		}
	}
	first := page.Items[0].ID
	var summary *Event
	if cfg.RelayURL == PushServiceURL && cfg.RelayToken != "" && (!st.HasPushAPIID || st.LastPushAPIID != first) {
		var id [32]byte
		if _, err = rand.Read(id[:]); err != nil {
			return err
		}
		summary = &Event{EventID: hex.EncodeToString(id[:]), Type: "unread_summary", SourceAccountID: st.AccountID, NotificationID: first,
			UnreadCount: web.Count, Title: "@" + st.Username, CreatedAt: web.ObservedAt, ExpiresAt: web.ObservedAt.Add(15 * time.Minute)}
		st.HasPushAPIID = true
		st.LastPushAPIID = first
	}
	st.LastError = ""
	st.Failures = 0
	st.LastSuccess = now
	st.HighWater = max(st.HighWater, first)
	return e.Store.ImportPageAndUnread(st, page.Items, summary)
}
