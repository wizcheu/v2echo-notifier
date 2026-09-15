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
func (e *Engine) stepHybridLocked(ctx context.Context, cfg Config, st State, now time.Time) (result error) {
	if (st.AuthBlocked && st.TokenIssue == "") || st.CookieIssue != "" || now.Before(st.NextCheck) || now.Before(st.NextWeb) {
		return nil
	}
	st.NextCheck = now.Add(time.Duration(cfg.IntervalSeconds) * time.Second)
	st.NextWeb = now.Add(3 * time.Minute)
	check := newCheckRecord(&st, now, "web")
	if err := e.Store.SaveState(st); err != nil {
		return err
	}
	start := time.Now()
	defer e.Store.finishCheck(&check, start, &result)
	web, err := e.readWebUnread(ctx, cfg.Cookie, st.Username)
	if err != nil {
		check.Web = failedCheckStage(err, start, "无法读取网页未读数，请检查网络、Cookie 或页面访问状态")
		check.Summary = check.Web.Detail
		check.Outcome = "未取得本次未读数，保留推送基准；后续按检查计划重试"
		st.NextWeb = maxTime(st.NextWeb, now.Add(max(3*time.Minute, backoff(st.Failures+1))))
		return e.recordFailure(st, nil, err, now)
	}
	check.UnreadCount = &web.Count
	check.Web = CheckStage{Status: "completed", HTTPStatus: 200, DurationMS: time.Since(start).Milliseconds(), Detail: "已读取本次网页未读数"}
	check.Status = "partial"
	st.CookieIssue = ""
	st.CookieCheckedAt = now
	st.HasWebUnread = true
	st.WebUnreadCount = web.Count
	st.WebObservedAt = web.ObservedAt
	st.Phase = "live"
	st.Page = 1
	if st.AuthBlocked {
		check.Summary = "网页读取成功，API Token 需要更新"
		check.API.Detail = "Token 尚未恢复，未发起 API 首条消息确认"
		// A previously verified identity can still check its Cookie while the
		// Token needs replacement, but must not request API data or queue a push.
		return e.Store.ImportPageAndUnread(st, nil, nil)
	}
	if web.Count == 0 {
		check.Status, check.Summary = "completed", "没有未读消息"
		check.API.Detail = "网页未读数为 0，无需查询 API"
		st.LastError = ""
		st.Failures = 0
		st.LastSuccess = now
		return e.Store.ImportPageAndUnread(st, nil, nil)
	}
	if ctx.Err() != nil {
		check.Summary = "网页读取成功，本轮检查已停止"
		check.API.Detail = "运行时段结束或任务取消，未发起 API 首条消息确认"
		return e.Store.ImportPageAndUnread(st, nil, nil)
	}
	if now.Before(st.NextAPI) {
		check.Summary = "网页读取成功，API 等待冷却"
		check.API.Detail = "API 冷却或退避尚未结束，保留推送基准等待后续检查"
		return e.Store.ImportPageAndUnread(st, nil, nil)
	}
	if st.Quota.Exhausted(now) {
		check.Summary = "网页读取成功，API 等待额度"
		check.API.Detail = "账号 API 额度不足，恢复后再确认首条消息"
		st.NextAPI = st.Quota.Reset.Add(time.Second)
		return e.Store.ImportPageAndUnread(st, nil, nil)
	}
	if e.Budget != nil {
		ok, err := e.Budget.Reserve(now)
		if err != nil {
			return err
		}
		if !ok {
			check.Summary = "网页读取成功，API 等待共享额度"
			check.API.Detail = "共享额度不足或正在退避，未发起 API 首条消息确认"
			return e.Store.ImportPageAndUnread(st, nil, nil)
		}
	}
	st.Quota.Remaining--
	st.NextAPI = now.Add(st.Quota.Delay(now))
	if err = e.Store.SaveState(st); err != nil {
		return err
	}
	var page Page
	apiStart := time.Now()
	headers, err := e.requestAPI(ctx, cfg.APIToken, "notifications?p=1", &page, now)
	st.Quota.Observe(headers, now)
	st.NextAPI = now.Add(st.Quota.Delay(now))
	if err != nil {
		check.API = failedCheckStage(err, apiStart, "API 请求失败，请检查网络或响应格式")
		check.Summary = "网页读取成功，API 确认失败"
		return e.recordFailure(st, headers, err, now)
	}
	check.API = CheckStage{Status: "completed", HTTPStatus: 200, DurationMS: time.Since(apiStart).Milliseconds(), Detail: "已确认 API 首条消息"}
	st.tokenValid(now)
	if len(page.Items) == 0 {
		check.API.Status, check.API.Detail = "failed", "网页有未读，但 API 暂未返回通知"
		check.Summary = "网页与 API 结果暂不一致"
		return e.recordFailure(st, nil, errors.New("网页有未读，但 API 暂未返回通知；保留推送基准等待下次检查"), now)
	}
	for i, n := range page.Items {
		if n.ID <= 0 || n.ForMemberID != st.AccountID || n.Created <= 0 || (i > 0 && page.Items[i-1].ID <= n.ID) {
			check.API.Status, check.API.Detail = "failed", "API 通知身份或排序异常，未更新推送基准"
			check.Summary = "网页读取成功，API 数据校验失败"
			return e.recordFailure(st, nil, errors.New("API 通知身份或排序异常，未更新推送基准"), now)
		}
	}
	first := page.Items[0].ID
	check.FirstID = &first
	check.Status = "completed"
	check.Summary = "API 首条 ID 未变化"
	check.Outcome = "保留当前推送基准，未重复上报"
	if cfg.RelayURL != PushServiceURL || cfg.RelayToken == "" {
		check.Summary = "检查完成，尚未配对接收设备"
		check.Outcome = "本轮未创建上报事件；配对设备后继续检查"
	}
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
		check.EventID = summary.EventID
		check.Summary = "发现新提醒，已加入上报队列"
		check.Outcome = "后续上报结果见推送历史；加入队列不代表设备已收到通知"
	}
	st.LastError = ""
	st.Failures = 0
	st.LastSuccess = now
	st.HighWater = max(st.HighWater, first)
	return e.Store.ImportPageAndUnread(st, page.Items, summary)
}
