package assistant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
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

// The homepage gates collection; stable notification IDs, not count deltas or
// first-ID transitions, identify pushes. Each call performs at most one API page.
func (e *Engine) stepHybridLocked(ctx context.Context, cfg Config, st State, now time.Time) (result error) {
	if (st.AuthBlocked && st.TokenIssue == "") || st.CookieIssue != "" {
		return nil
	}
	continuing := st.UnreadScan != nil
	if !continuing && (now.Before(st.NextCheck) || now.Before(st.NextWeb)) {
		return nil
	}
	if continuing && now.Before(st.NextAPI) && now.Before(st.NextWeb) {
		return nil
	}
	check := newCheckRecord(&st, now, "web")
	start := time.Now()
	defer e.Store.finishCheck(&check, start, &result)
	reset := false
	paired := cfg.RelayURL == PushServiceURL && cfg.RelayToken != ""
	if !continuing || !now.Before(st.NextWeb) {
		st.NextCheck = now.Add(time.Duration(cfg.IntervalSeconds) * time.Second)
		st.NextWeb = now.Add(3 * time.Minute)
		if err := e.Store.SaveState(st); err != nil {
			return err
		}
		web, err := e.readWebUnread(ctx, cfg.Cookie, st.Username)
		if err != nil {
			check.Web = failedCheckStage(err, start, "无法读取网页未读数，请检查网络、Cookie 或页面访问状态")
			check.Summary = check.Web.Detail
			check.Outcome = "未取得本次未读数，保留推送基准；后续按检查计划重试"
			// Do not keep collecting against an unverified/stale homepage.
			st.UnreadScan = nil
			st.NextWeb = maxTime(st.NextWeb, now.Add(max(3*time.Minute, backoff(st.Failures+1))))
			return e.recordFailure(st, nil, err, now)
		}
		check.UnreadCount = new(web.Count)
		check.Web = CheckStage{Status: "completed", HTTPStatus: 200, DurationMS: time.Since(start).Milliseconds(), Detail: "已读取本次网页未读数"}
		st.CookieIssue, st.CookieCheckedAt = "", now
		st.HasWebUnread, st.WebUnreadCount, st.WebObservedAt = true, web.Count, web.ObservedAt
		st.updateAvatar(web)
		st.Phase, st.Page = "live", 1
		if st.UnreadScan != nil && st.UnreadScan.Count != web.Count {
			st.UnreadScan = nil
			reset = true
		}
	}
	check.Status = "partial"
	if st.AuthBlocked {
		st.UnreadScan = nil
		check.Summary = "网页读取成功，API Token 需要更新"
		check.API.Detail = "Token 尚未恢复，未发起通知 API 请求"
		_, err := e.Store.importUnreadPage(st, nil, true, false, paired, now)
		return err
	}
	if st.WebUnreadCount == 0 {
		st.UnreadScan = nil
		check.Status, check.Summary = "completed", "没有未读消息"
		check.API.Detail = "网页未读数为 0，无需查询 API"
		st.LastError, st.Failures, st.LastSuccess = "", 0, now
		_, err := e.Store.importUnreadPage(st, nil, true, true, paired, now)
		return err
	}
	if st.UnreadScan == nil {
		st.UnreadScan = &UnreadScan{Count: st.WebUnreadCount, Page: 1, ObservedAt: st.WebObservedAt}
		reset = true
	}
	// Persist a reset before an API reservation/failure can leave an old page set.
	if _, err := e.Store.importUnreadPage(st, nil, reset, false, paired, now); err != nil {
		return err
	}
	if ctx.Err() != nil {
		check.Summary = "检查已停止，保留分页进度"
		return nil
	}
	if now.Before(st.NextAPI) {
		check.Summary = "网页读取成功，API 等待冷却"
		check.API.Detail = "API 冷却或退避尚未结束，保留分页进度等待后续检查"
		return nil
	}
	if st.Quota.Exhausted(now) {
		check.Summary = "网页读取成功，API 等待额度"
		check.API.Detail = "账号 API 额度不足，恢复后继续读取通知"
		st.NextAPI = st.Quota.Reset.Add(time.Second)
		return e.Store.SaveState(st)
	}
	if e.Budget != nil {
		ok, err := e.Budget.Reserve(now)
		if err != nil {
			return err
		}
		if !ok {
			check.Summary = "网页读取成功，API 等待共享额度"
			check.API.Detail = "共享额度不足或正在退避，保留分页进度"
			return nil
		}
	}
	st.Quota.Remaining--
	st.NextAPI = now.Add(st.Quota.Delay(now))
	if err := e.Store.SaveState(st); err != nil {
		return err
	}
	scan := st.UnreadScan
	pageNumber := scan.Page
	if scan.Verify {
		pageNumber = 1
	}
	var page Page
	apiStart := time.Now()
	headers, err := e.requestAPI(ctx, cfg.APIToken, fmt.Sprintf("notifications?p=%d", pageNumber), &page, now)
	st.Quota.Observe(headers, now)
	st.NextAPI = now.Add(st.Quota.Delay(now))
	if err != nil {
		check.API = failedCheckStage(err, apiStart, "API 请求失败，请检查网络或响应格式")
		check.Summary = "网页读取成功，API 确认失败"
		return e.recordFailure(st, headers, err, now)
	}
	check.API = CheckStage{Status: "completed", HTTPStatus: 200, DurationMS: time.Since(apiStart).Milliseconds(), Detail: fmt.Sprintf("已读取通知第 %d 页", pageNumber)}
	st.tokenValid(now)
	ids := make([]int64, 0, len(page.Items))
	for i, n := range page.Items {
		if n.ID <= 0 || n.ForMemberID != st.AccountID || n.Created <= 0 || (i > 0 && page.Items[i-1].ID <= n.ID) {
			check.API.Status, check.API.Detail = "failed", "API 通知身份或排序异常，未更新推送基准"
			check.Summary = "网页读取成功，API 数据校验失败"
			return e.recordFailure(st, nil, errors.New(check.API.Detail), now)
		}
		ids = append(ids, n.ID)
	}
	if len(ids) > 0 {
		check.FirstID = new(ids[0])
	}
	if len(ids) == 0 && pageNumber == 1 {
		check.API.Status, check.API.Detail = "failed", "网页有未读，但 API 暂未返回通知"
		check.Summary = "网页与 API 结果暂不一致"
		st.UnreadScan = nil
		return e.recordFailure(st, nil, errors.New(check.API.Detail), now)
	}
	if scan.Verify {
		if !slices.Equal(ids, scan.HeadIDs) {
			// Page positions moved. Wait for a fresh homepage count before starting
			// over; reusing the old n here could miss unread items at the tail.
			st.UnreadScan = nil
			check.Summary = "翻页期间通知列表发生变化，等待新的网页快照后重扫"
			_, err := e.Store.importUnreadPage(st, nil, true, false, paired, now)
			return err
		}
		return e.finishUnreadScan(&st, &check, nil, paired, now)
	}
	if scan.Page == 1 {
		scan.HeadIDs = ids
	}
	if scan.Page > 1 && len(ids) > 0 && ids[len(ids)-1] >= scan.LastMinID {
		st.UnreadScan = nil
		check.Summary = "分页未向更早通知推进，等待新的网页快照后重扫"
		_, err := e.Store.importUnreadPage(st, nil, true, false, paired, now)
		return err
	}
	candidates := make([]Notification, 0, len(page.Items))
	for _, n := range page.Items {
		// A newly inserted head may cause page overlap. Count unique IDs only.
		if scan.LastMinID > 0 && n.ID >= scan.LastMinID {
			continue
		}
		if scan.Collected >= scan.Count {
			break
		}
		candidates = append(candidates, n)
		scan.Collected++
	}
	if len(ids) > 0 {
		scan.LastMinID = ids[len(ids)-1]
	}
	complete := scan.Collected >= scan.Count || page.End
	if complete && scan.Page == 1 {
		return e.finishUnreadScan(&st, &check, candidates, paired, now)
	}
	if complete {
		scan.Verify = true
	} else {
		scan.Page++
	}
	check.Summary = fmt.Sprintf("已暂存 %d 条候选通知，继续核对未读范围", scan.Collected)
	check.Outcome = "分页完成并复查首屏后，按通知 ID 去重加入推送队列"
	_, err = e.Store.importUnreadPage(st, candidates, false, false, paired, now)
	return err
}

func (e *Engine) finishUnreadScan(st *State, check *CheckRecord, items []Notification, paired bool, now time.Time) error {
	head := st.UnreadScan.HeadIDs[0]
	st.UnreadScan = nil
	st.LastError, st.Failures, st.LastSuccess = "", 0, now
	if paired {
		st.HasPushAPIID, st.LastPushAPIID = true, head
	}
	st.HighWater = max(st.HighWater, head)
	id, err := e.Store.importUnreadPage(*st, items, false, true, paired, now)
	check.Status = "completed"
	check.Summary, check.Outcome = "未读范围核对完成，没有新的通知 ID", "已入队的通知不会重复创建；推送状态不代表已阅读"
	if !paired {
		check.Summary, check.Outcome = "检查完成，尚未配对接收设备", "本轮未创建上报事件；配对设备后继续检查"
	} else if id != "" {
		check.EventID = id
		check.Summary, check.Outcome = "发现新提醒，已逐条加入上报队列", "每个通知 ID 对应一个推送事件；后续结果见推送历史"
	}
	return err
}
