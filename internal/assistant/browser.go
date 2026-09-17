package assistant

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

const browserRequiredMessage = "V2EX 首页需要浏览器验证或被拒绝访问，请打开验证窗口后重试"

// Browser cookies use the account's encryption key; the companion keeps its
// live profile in tmpfs. Neither cookies nor proxy credentials reach status APIs.
type browserState struct {
	Enabled  bool            `json:"enabled"`
	Required bool            `json:"required"`
	Binding  string          `json:"binding"`
	Cookies  json.RawMessage `json:"cookies,omitempty"`
}

type BrowserStatus struct {
	Available bool `json:"available"`
	Enabled   bool `json:"enabled"`
	Required  bool `json:"required"`
}

func (s *Store) browserState() (browserState, error) {
	var b browserState
	var sealed string
	err := s.DB.QueryRow("SELECT body FROM browser_session WHERE id=1").Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	raw, err := s.unseal(sealed)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &b)
	}
	return b, err
}
func (s *Store) saveBrowser(b browserState) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec("INSERT INTO browser_session(id,body) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", s.seal(string(raw)))
	return err
}

func (s *Store) disableBrowser(st State) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM browser_session WHERE id=1"); err != nil {
		return err
	}
	// Also recognize the older wording persisted before the warning was updated.
	if strings.Contains(st.LastError, "尚未部署浏览器配套容器") {
		st.LastError = ""
		if err = saveState(tx, st); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// One visible desktop is shared by the instance. A manual lease prevents any
// other account or scheduled check from navigating it while the user verifies.
type BrowserService struct {
	mu          sync.Mutex
	control     string
	desktop     *url.URL
	token       string
	client      *http.Client
	owner       string
	until       time.Time
	viewer      string
	cancelView  context.CancelFunc
	viewContext context.Context
}

func NewBrowserService(control, desktop, token string) (*BrowserService, error) {
	if control == "" && desktop == "" {
		return nil, nil
	}
	c, err := url.Parse(control)
	d, derr := url.Parse(desktop)
	valid := func(u *url.URL) bool {
		return u != nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/")
	}
	if err != nil || derr != nil || !valid(c) || !valid(d) || len(token) < 32 || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("浏览器服务需要有效的控制/桌面地址及至少 32 字符的共享密钥")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil // The private companion must never use the account/upstream proxy.
	client := secureClient(24 * time.Second)
	client.Transport = tr
	return &BrowserService{control: strings.TrimRight(control, "/"), desktop: d, token: token, client: client}, nil
}

type browserRequest struct {
	Account string          `json:"account"`
	Binding string          `json:"binding"`
	Proxy   string          `json:"proxy"`
	Cookie  string          `json:"cookie"`
	Cookies json.RawMessage `json:"cookies,omitempty"`
}
type browserResponse struct {
	HTML          string          `json:"html"`
	URL           string          `json:"url"`
	Status        int             `json:"status"`
	Challenged    bool            `json:"challenged"`
	LoginRequired bool            `json:"login_required"`
	Cookies       json.RawMessage `json:"cookies"`
}

func (b *BrowserService) call(ctx context.Context, operation string, in browserRequest) (browserResponse, error) {
	var out browserResponse
	raw, err := json.Marshal(in)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.control+"/"+operation, bytes.NewReader(raw))
	if err != nil {
		return out, &webRequestError{message: "浏览器服务配置不正确"}
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := b.client.Do(req)
	if err != nil {
		return out, &webRequestError{message: "无法连接浏览器服务，请检查配套容器后重试"}
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		if res.StatusCode == http.StatusUnauthorized {
			return out, &webRequestError{message: "浏览器服务认证失败，请检查两个容器的共享密钥是否一致"}
		}
		var diagnostic struct {
			Stage string `json:"stage"`
			Code  string `json:"code"`
		}
		// Only known identifiers are translated. Never surface arbitrary helper
		// error text, which could contain cookies, proxy credentials or HTML.
		if json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&diagnostic) == nil {
			stages := map[string]string{
				"request": "校验浏览器请求", "reset": "清理浏览器会话", "profile": "创建浏览器配置",
				"launch": "启动 Chromium", "devtools": "连接 Chromium 控制接口", "configure": "配置 Chromium",
				"cookies": "导入账号 Cookie", "navigate": "打开首页", "homepage": "读取首页",
			}
			codes := map[string]string{
				"operation_failed": "操作未完成", "browser_exited": "Chromium 已退出", "browser_not_ready": "Chromium 未就绪",
				"cdp_failed": "Chromium 控制命令失败", "cdp_timeout": "Chromium 控制命令超时",
				"navigation_failed": "页面导航失败", "proxy_tunnel_failed": "代理隧道连接失败",
				"dns_failed": "域名解析失败", "tls_failed": "站点证书验证失败", "homepage_timeout": "等待首页加载超时",
			}
			if stage, code := stages[diagnostic.Stage], codes[diagnostic.Code]; stage != "" && code != "" {
				return out, &webRequestError{message: stage + "失败：" + code + "。可重试；若持续失败，请检查浏览器容器日志"}
			}
		}
		return out, &webRequestError{message: "浏览器操作失败，请检查配套容器后重试"}
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 6<<20+1))
	if err != nil || len(data) > 6<<20 || json.Unmarshal(data, &out) != nil {
		return out, &webRequestError{message: "浏览器服务响应无效"}
	}
	return out, nil
}
func (b *BrowserService) releaseLocked() {
	if b.cancelView != nil {
		b.cancelView()
	}
	b.owner, b.viewer = "", ""
	b.until = time.Time{}
}

func (e *Engine) browserInput(cookie string) (browserRequest, browserState, error) {
	cfg, err := e.Store.Config()
	if err != nil {
		return browserRequest{}, browserState{}, err
	}
	st, err := e.Store.browserState()
	if err != nil {
		return browserRequest{}, st, err
	}
	mode, raw, err := normalizeProxy(cfg.ProxyMode, cfg.ProxyURL)
	if err != nil {
		return browserRequest{}, st, err
	}
	if mode == "environment" {
		req, _ := http.NewRequest(http.MethodGet, "https://www.v2ex.com/", nil)
		p, err := http.ProxyFromEnvironment(req)
		if err != nil {
			return browserRequest{}, st, errors.New("无法解析环境变量代理")
		}
		raw = ""
		if p != nil {
			raw = p.String()
		}
	}
	digest := sha256.Sum256([]byte(cookie + "\x00" + raw))
	binding := hex.EncodeToString(digest[:])
	if st.Binding != binding {
		st.Cookies = nil
		st.Binding = binding
	}
	return browserRequest{Account: e.profileID, Binding: binding, Proxy: raw, Cookie: cookie, Cookies: st.Cookies}, st, nil
}

func (e *Engine) browserHomepage(ctx context.Context, cookie, username, operation, viewer string) (WebUnreadSnapshot, error) {
	if e.Browser == nil {
		return WebUnreadSnapshot{}, &webRequestError{message: "当前尚未部署浏览器配套容器，如遇代理访问返回403、需要CF人机验证无法使用的情况，请按部署文档启用浏览器验证"}
	}
	b := e.Browser
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.until.After(time.Now()) {
		b.releaseLocked()
	}
	if b.owner != "" && (b.owner != e.profileID || (operation == "start" && b.viewer != viewer)) {
		return WebUnreadSnapshot{}, &webRequestError{message: "其他账号或管理窗口正在验证，请完成或关闭验证后重试"}
	}
	if operation == "read" && b.owner != "" {
		return WebUnreadSnapshot{}, &webRequestError{message: "正在等待人工验证，请完成后点击重试首页"}
	}
	if operation == "check" && (b.owner != e.profileID || b.viewer != viewer) {
		return WebUnreadSnapshot{}, &webRequestError{message: "验证窗口已到期，请重新打开"}
	}
	in, st, err := e.browserInput(cookie)
	if err != nil {
		return WebUnreadSnapshot{}, err
	}
	out, err := b.call(ctx, operation, in)
	if err != nil {
		return WebUnreadSnapshot{}, err
	}
	st.Enabled = true
	if len(out.Cookies) > 0 {
		st.Cookies = out.Cookies
	}
	if operation == "start" && (out.LoginRequired || out.Status == 401) {
		return WebUnreadSnapshot{}, &webRequestError{message: "Cookie 登录已失效，请更新同账号的 Cookie", status: out.Status, auth: true}
	}
	if operation == "start" {
		b.releaseLocked()
		b.owner, b.viewer, b.until = e.profileID, viewer, time.Now().Add(10*time.Minute)
		b.viewContext, b.cancelView = context.WithDeadline(context.Background(), b.until)
		st.Required = true
		return WebUnreadSnapshot{}, e.Store.saveBrowser(st)
	}
	st.Required = out.Challenged || out.Status == 403
	if out.LoginRequired || out.Status == 401 {
		if err = e.Store.saveBrowser(st); err != nil {
			return WebUnreadSnapshot{}, err
		}
		return WebUnreadSnapshot{}, &webRequestError{message: "Cookie 登录已失效，请更新同账号的 Cookie", status: out.Status, auth: true}
	}
	if err = e.Store.saveBrowser(st); err != nil {
		return WebUnreadSnapshot{}, err
	}
	if st.Required {
		return WebUnreadSnapshot{}, &webRequestError{message: browserRequiredMessage, status: out.Status}
	}
	if out.Status != 200 || out.URL != "https://www.v2ex.com/" {
		return WebUnreadSnapshot{}, &webRequestError{message: "浏览器未取得 V2EX 首页，请确认验证完成后重试"}
	}
	count, err := parseWebUnread(out.HTML, username)
	if err != nil {
		return WebUnreadSnapshot{}, err
	}
	if operation == "check" {
		b.releaseLocked()
	}
	return WebUnreadSnapshot{Count: count, ObservedAt: time.Now().UTC()}, nil
}

func (e *Engine) browserAction(ctx context.Context, operation, viewer string) (WebUnreadSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed {
		return WebUnreadSnapshot{}, ErrAccountRemoved
	}
	st, err := e.Store.State()
	if err != nil {
		return WebUnreadSnapshot{}, err
	}
	if operation == "disable" {
		if e.Browser != nil {
			b := e.Browser
			b.mu.Lock()
			defer b.mu.Unlock()
			if b.owner != "" && b.owner != e.profileID && b.until.After(time.Now()) {
				return WebUnreadSnapshot{}, errors.New("其他账号正在验证，请稍后重试")
			}
			if _, err = b.call(ctx, "reset", browserRequest{Account: e.profileID}); err != nil {
				return WebUnreadSnapshot{}, err
			}
			if b.owner == e.profileID {
				b.releaseLocked()
			}
		}
		return WebUnreadSnapshot{}, e.Store.disableBrowser(st)
	}
	cfg, err := e.Store.Config()
	if err != nil {
		return WebUnreadSnapshot{}, err
	}
	if _, err = normalizeWebCookie(cfg.Cookie); err != nil {
		return WebUnreadSnapshot{}, err
	}
	result, err := e.browserHomepage(ctx, cfg.Cookie, st.Username, operation, viewer)
	if err != nil || operation != "check" {
		return result, err
	}
	// Recovery only observes the homepage. It must not advance the API/push
	// baseline, bypass API cooldowns, or submit a notification.
	st.CookieIssue = ""
	st.CookieCheckedAt = result.ObservedAt
	st.HasWebUnread = true
	st.WebUnreadCount = result.Count
	st.WebObservedAt = result.ObservedAt
	st.NextWeb = time.Time{}
	st.LastError = ""
	return result, e.Store.SaveState(st)
}

func (s *Server) browserAction(e *Engine, w http.ResponseWriter, r *http.Request) {
	operation := r.PathValue("operation")
	if operation != "start" && operation != "check" && operation != "disable" {
		fail(w, 404, "操作不存在")
		return
	}
	c, _ := r.Cookie("notifier_session")
	result, err := e.browserAction(r.Context(), operation, c.Value)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "desktop_url": "/browser-desktop/", "unread_count": result.Count})
}

func (s *Server) browserDesktop(w http.ResponseWriter, r *http.Request) {
	b := s.Accounts.Browser
	if b == nil {
		http.Error(w, "尚未启用浏览器服务", 503)
		return
	}
	origin := r.Header.Get("Origin")
	if origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
			http.Error(w, "请求来源不匹配", 403)
			return
		}
	}
	c, _ := r.Cookie("notifier_session")
	b.mu.Lock()
	if b.owner == "" || b.viewer != c.Value || !b.until.After(time.Now()) {
		b.mu.Unlock()
		http.Error(w, "验证窗口已结束，请返回管理页重新打开", 409)
		return
	}
	viewCtx := b.viewContext
	b.mu.Unlock()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(viewCtx, cancel)
	defer stop()
	proxy := httputil.NewSingleHostReverseProxy(b.desktop)
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		director(req)
		req.Header.Del("Cookie")
		req.Header.Del("Authorization")
		req.Header.Del("X-V2Echo-Request")
	}
	proxy.Transport = b.client.Transport
	proxy.ModifyResponse = func(res *http.Response) error { res.Header.Del("Set-Cookie"); return nil }
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "验证窗口连接已结束或浏览器容器不可用", 502)
	}
	w.Header().Set("Cache-Control", "no-store")
	// Selkies owns its client CSP (workers, WASM and WebSocket streaming).
	w.Header().Del("Content-Security-Policy")
	_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	proxy.ServeHTTP(w, r.WithContext(ctx))
}

func (a *Accounts) SetBrowserService(b *BrowserService) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Browser = b
	for _, e := range a.engines {
		e.Browser = b
	}
}
func (e *Engine) browserStatus() BrowserStatus {
	st, err := e.Store.browserState()
	if err != nil {
		return BrowserStatus{Available: e.Browser != nil}
	}
	return BrowserStatus{Available: e.Browser != nil, Enabled: st.Enabled, Required: st.Required}
}

// Caller holds the account lock. Invalidate a live browser before changing
// credentials or proxy, so an open desktop cannot keep using stale settings.
func (e *Engine) invalidateBrowser() error {
	st, err := e.Store.browserState()
	if err != nil {
		return err
	}
	if st.Enabled && e.Browser != nil {
		b := e.Browser
		b.mu.Lock()
		defer b.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err = b.call(ctx, "reset", browserRequest{Account: e.profileID}); err != nil {
			return err
		}
		if b.owner == e.profileID {
			b.releaseLocked()
		}
	}
	return e.Store.saveBrowser(browserState{Enabled: st.Enabled, Required: st.Enabled})
}
