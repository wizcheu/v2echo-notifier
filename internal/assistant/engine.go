package assistant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Engine struct {
	proxyTestMu  sync.Mutex
	network      *accountTransport
	Budget       *APIBudget
	ClaimAccount func(Member) error
	removed      bool
	Store        *Store
	API          *V2EX
	Relay        *http.Client
	Web          *http.Client
	mu           sync.Mutex
	relayMu      sync.Mutex
}

func NewEngine(s *Store) *Engine {
	network := &accountTransport{store: s, base: http.DefaultTransport.(*http.Transport).Clone()}
	client := func(timeout time.Duration) *http.Client { c := secureClient(timeout); c.Transport = network; return c }
	return &Engine{network: network, Store: s, API: &V2EX{"https://www.v2ex.com/api/v2/", client(20 * time.Second)}, Relay: client(17 * time.Second), Web: client(10 * time.Second)}
}

// Config edits and API calls are serialized so a response from an old token
// cannot be committed as a newly configured account's state.
func (e *Engine) Configure(c Config) error {
	return e.configure(c, false)
}

func (e *Engine) configure(c Config, preservePairing bool) error {
	e.relayMu.Lock()
	defer e.relayMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed {
		return ErrAccountRemoved
	}
	if c.IntervalSeconds < 30 || c.IntervalSeconds > 86400 {
		return errors.New("检查间隔应为 30～86400 秒")
	}
	old, err := e.Store.Config()
	if err != nil {
		return err
	}
	// Omitted schedules preserve the account setting for credential edits and older clients.
	if c.PushSchedule == nil {
		c.PushSchedule = old.PushSchedule
	}
	if err := c.schedule().validate(); err != nil {
		return err
	}
	if preservePairing {
		c.RelayURL, c.RelayToken = old.RelayURL, old.RelayToken
	}
	if c.ProxyMode == "" {
		c.ProxyMode, c.ProxyURL = old.ProxyMode, old.ProxyURL
	}
	if c.ProxyMode == "custom" && strings.TrimSpace(c.ProxyURL) == "" {
		c.ProxyURL = old.ProxyURL
	}
	if c.ProxyMode, c.ProxyURL, err = normalizeProxy(c.ProxyMode, c.ProxyURL); err != nil {
		return err
	}
	if c.APIToken == "" {
		c.APIToken = old.APIToken
	}
	if c.Cookie == "" {
		c.Cookie = old.Cookie
	} else {
		c.Cookie, err = normalizeWebCookie(c.Cookie)
		if err != nil {
			return err
		}
	}
	if c.RelayToken == "" {
		c.RelayToken = old.RelayToken
	}
	c.APIToken = strings.TrimSpace(c.APIToken)
	c.RelayToken = strings.TrimSpace(c.RelayToken)
	c.RelayURL = strings.TrimRight(strings.TrimSpace(c.RelayURL), "/")
	for _, secret := range []string{c.APIToken, c.RelayToken} {
		if len(secret) > 4096 || strings.ContainsAny(secret, "\r\n") {
			return errors.New("凭据格式不正确")
		}
	}
	if c.RelayURL != "" {
		u, err := url.Parse(c.RelayURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("推送服务地址必须为不含用户名、查询参数或片段的 HTTPS 地址")
		}
	}
	if c.Enabled && c.APIToken == "" {
		return errors.New("请先填写 V2EX API Token")
	}
	st, err := e.Store.State()
	if err != nil {
		return err
	}
	if c.APIToken != old.APIToken {
		st.TokenIssue = ""
		st.TokenCheckedAt = time.Time{}
		st.NextTokenCheck = time.Time{}
		st.Verified = false
		st.AuthBlocked = false
		st.LastError = ""
		st.Failures = 0
	}
	if c.Cookie != old.Cookie {
		st.CookieIssue = ""
		st.CookieCheckedAt = time.Time{}
		if st.TokenIssue == "" {
			st.LastError = ""
		}
	}
	st.NextCheck = time.Time{}
	if !c.Enabled || c.schedule() != old.schedule() {
		st.CheckRequested = false
	}
	changed := c.RelayToken != old.RelayToken || c.RelayURL != old.RelayURL
	if err = e.Store.SaveConfig(c, st, changed, changed && old.RelayToken != ""); err != nil {
		return err
	}
	if (c.ProxyMode != old.ProxyMode || c.ProxyURL != old.ProxyURL) && e.network != nil {
		e.network.CloseIdleConnections()
	}
	return nil
}

func (e *Engine) RequestCheck() error { return e.requestCheck(time.Now()) }

func (e *Engine) requestCheck(now time.Time) error {
	entered := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed {
		return ErrAccountRemoved
	}
	effectiveNow := now.Add(time.Since(entered))
	cfg, err := e.Store.Config()
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return ErrSyncDisabled
	}
	if !cfg.allowsPush(effectiveNow) {
		return quietHoursError(cfg, effectiveNow)
	}
	st, err := e.Store.State()
	if err != nil {
		return err
	}
	st.NextCheck = time.Time{}
	st.CheckRequested = true
	return e.Store.SaveState(st) // Never override NextAPI, Retry-After or auth failures.
}

func (e *Engine) Step(ctx context.Context, now time.Time) (result error) {
	entered := time.Now()
	// Coalescing unsent summaries must not race delivery's read/reserve step.
	e.relayMu.Lock()
	defer e.relayMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed {
		return ErrAccountRemoved
	}
	effectiveNow := now.Add(time.Since(entered))
	cfg, err := e.Store.Config()
	if err != nil {
		return err
	}
	if !cfg.Enabled || cfg.APIToken == "" || !cfg.allowsPush(effectiveNow) {
		return nil
	}
	ctx, cancel := cfg.windowContext(ctx, effectiveNow)
	defer cancel()
	st, err := e.Store.State()
	if err != nil {
		return err
	}
	if canReadWeb(cfg, st) {
		if st.NextTokenCheck.IsZero() {
			st.NextTokenCheck = now.Add(tokenCheckInterval)
			if err := e.Store.SaveState(st); err != nil {
				return err
			}
		}
		if !st.AuthBlocked && !now.Before(st.NextTokenCheck) && !now.Before(st.NextAPI) {
			handled, err := e.checkTokenLocked(ctx, cfg, &st, now)
			if handled || err != nil {
				return err
			}
		}
		return e.stepHybridLocked(ctx, cfg, st, now)
	}
	if st.AuthBlocked || now.Before(st.NextAPI) || now.Before(st.NextCheck) {
		return nil
	}
	if st.Quota.Exhausted(now) {
		st.NextAPI = st.Quota.Reset.Add(time.Second)
		return e.Store.SaveState(st)
	}
	if e.Budget != nil {
		allowed, err := e.Budget.Reserve(now)
		if err != nil {
			return err
		}
		if !allowed {
			return nil
		}
	}
	st.Quota.Remaining--
	st.NextAPI = now.Add(st.Quota.Delay(now))
	if err = e.Store.SaveState(st); err != nil {
		return err
	}

	var headers http.Header
	if !st.Verified {
		var member Member
		headers, err = e.requestAPI(ctx, cfg.APIToken, "member", &member, now)
		st.Quota.Observe(headers, now)
		st.NextAPI = now.Add(st.Quota.Delay(now))
		if err != nil {
			return e.recordFailure(st, headers, err, now)
		}
		if member.ID <= 0 || member.Username == "" {
			return e.recordFailure(st, nil, errors.New("无法确认 Token 所属账号"), now)
		}
		if st.AccountID != 0 && member.ID != st.AccountID {
			st.AuthBlocked = true
			st.LastError = "Token 属于另一个账号。请恢复原账号 Token；其他账号请使用「添加账号」。"
			return e.Store.SaveState(st)
		}
		if e.ClaimAccount != nil {
			if err := e.ClaimAccount(member); err != nil {
				st.AuthBlocked = true
				st.LastError = err.Error()
				return e.Store.SaveState(st)
			}
		}
		st.AccountID = member.ID
		st.Username = member.Username
		st.Verified = true
		st.tokenValid(now)
		st.LastError = ""
		st.Failures = 0
		return e.Store.SaveState(st)
	}

	check := newCheckRecord(&st, now, "api")
	check.Web.Detail = "未配置网页 Cookie，旧版 API 同步不提供网页未读总数"
	if err = e.Store.SaveState(st); err != nil {
		return err
	}
	start := time.Now()
	defer e.Store.finishCheck(&check, start, &result)
	var page Page
	headers, err = e.requestAPI(ctx, cfg.APIToken, fmt.Sprintf("notifications?p=%d", st.Page), &page, now)
	st.Quota.Observe(headers, now)
	st.NextAPI = now.Add(st.Quota.Delay(now))
	if err != nil {
		check.API = failedCheckStage(err, start, "API 请求失败，请检查网络或响应格式")
		check.Summary = check.API.Detail
		return e.recordFailure(st, headers, err, now)
	}
	check.API = CheckStage{Status: "failed", HTTPStatus: 200, DurationMS: time.Since(start).Milliseconds(), Detail: "API 数据校验未通过，保留原有同步进度"}
	check.Summary = check.API.Detail
	st.tokenValid(now)
	for i, n := range page.Items {
		if n.ID <= 0 || n.ForMemberID != st.AccountID || n.Created <= 0 {
			return e.recordFailure(st, nil, errors.New("通知身份字段不匹配，未推进同步进度"), now)
		}
		if i > 0 && page.Items[i-1].ID <= n.ID {
			return e.recordFailure(st, nil, errors.New("通知 ID 未按倒序排列，无法安全推进同步进度"), now)
		}
	}
	if len(page.Items) > 0 {
		oldest := page.Items[len(page.Items)-1].ID
		if st.Page > 1 && st.LastPageMinID > 0 && oldest >= st.LastPageMinID {
			return e.recordFailure(st, nil, errors.New("分页暂未返回更早的通知，保留进度后重试"), now)
		}
		st.LastPageMinID = oldest
	}
	isHistory := st.Phase == "history"
	check.Status = "completed"
	check.API.Status = "completed"
	check.API.Detail = fmt.Sprintf("第 %d 页返回 %d 条通知；通知条数不等于网页未读总数", st.Page, len(page.Items))
	check.Summary = fmt.Sprintf("API 第 %d 页同步完成", st.Page)
	check.Outcome = "本轮已同步通知；符合提醒条件的事件可在推送历史查看"
	if isHistory {
		check.Outcome = "导入历史通知，本轮不创建上报事件"
	}
	if len(page.Items) > 0 {
		first := page.Items[0].ID
		check.FirstID = &first
	}
	if isHistory && !st.AnchorSet {
		if len(page.Items) > 0 {
			st.AnchorID = page.Items[0].ID
		}
		st.AnchorSet = true
		st.HighWater = st.AnchorID
		st.CycleMax = st.AnchorID
	}
	// HighWater remains fixed for the entire incremental pagination cycle.
	// Rows inserted by an earlier page are never mistaken for the old boundary.
	reachedBoundary := false
	for _, n := range page.Items {
		st.CycleMax = max(st.CycleMax, n.ID)
		if n.ID <= st.HighWater {
			reachedBoundary = true
		}
	}
	st.LastSuccess = now
	st.LastError = ""
	st.Failures = 0
	if isHistory {
		if page.End {
			st.Phase = "catchup"
			st.Page = 1
			st.CycleMax = st.AnchorID
			st.LastPageMinID = 0
		} else {
			st.Page++
		}
	} else if page.End || reachedBoundary {
		st.HighWater = max(st.HighWater, st.CycleMax)
		st.CycleMax = st.HighWater
		st.Phase = "live"
		st.Page = 1
		st.LastPageMinID = 0
		st.NextCheck = now.Add(time.Duration(cfg.IntervalSeconds) * time.Second)
	} else {
		st.Page++
	}
	return e.Store.ImportPage(st, page.Items, !isHistory)
}

func (e *Engine) recordFailure(st State, h http.Header, err error, now time.Time) error {
	st.Failures++
	st.LastError = err.Error()
	delay := max(backoff(st.Failures), retryAfter(h, now))
	var ae *APIError
	var we *webRequestError
	if errors.As(err, &we) {
		if we.auth {
			st.CookieIssue = we.message
		}
		st.NextWeb = maxTime(st.NextWeb, now.Add(max(3*time.Minute, we.retry)))
	}
	if errors.As(err, &ae) {
		if ae.Expired {
			st.TokenIssue = ae.Error()
			st.Verified = false
			st.AuthBlocked = true
		}
		if ae.Code == 429 {
			// Without a trustworthy reset, use the conservative local window.
			delay = max(delay, st.Quota.Reset.Sub(now)+time.Second)
			st.Quota.Remaining = 0
		}
	}
	st.NextAPI = maxTime(st.NextAPI, now.Add(delay))
	return e.Store.SaveState(st)
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func (e *Engine) requestAPI(ctx context.Context, token, path string, target any, now time.Time) (http.Header, error) {
	headers, err := e.API.get(ctx, token, path, target)
	if e.Budget != nil {
		if budgetError := e.Budget.Observe(headers, err, now); budgetError != nil {
			return headers, budgetError
		}
	}
	return headers, err
}
