package assistant

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Server struct {
	Accounts      *Accounts
	Assets        fs.FS
	SecureCookies bool
	adminHash     [32]byte
	mu            sync.Mutex
	sessions      map[string]time.Time
	loginWindow   time.Time
	loginAttempts int
}

func NewServer(accounts *Accounts, assets fs.FS, dir string) (*Server, error) {
	secret, err := loadOrCreateSecret(filepath.Join(dir, "admin-token"), 32)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(base64.RawURLEncoding.EncodeToString(secret)))
	return &Server{Accounts: accounts, Assets: assets, adminHash: hash, sessions: map[string]time.Time{}}, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func decode(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected trailing data")
	}
	return nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/browser-desktop/", s.authorized(s.browserDesktop))
	mux.HandleFunc("POST /api/accounts/{accountID}/browser/{operation}", s.account(s.browserAction))
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.authorized(func(w http.ResponseWriter, r *http.Request) {
		c, _ := r.Cookie("notifier_session")
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
		if b := s.Accounts.Browser; b != nil {
			b.mu.Lock()
			if b.viewer == c.Value {
				b.releaseLocked()
			}
			b.mu.Unlock()
		}
		http.SetCookie(w, &http.Cookie{Name: "notifier_session", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.SecureCookies || r.TLS != nil, MaxAge: -1})
		writeJSON(w, 200, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("GET /api/accounts", s.authorized(func(w http.ResponseWriter, r *http.Request) {
		accounts, err := s.Accounts.List()
		if err != nil {
			fail(w, 500, "无法读取账号列表")
			return
		}
		writeJSON(w, 200, map[string]any{"accounts": accounts})
	}))
	mux.HandleFunc("POST /api/accounts", s.authorized(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Token  string `json:"api_token"`
			Cookie string `json:"cookie"`
			ProxyConfig
		}
		if err := decode(w, r, &input); err != nil {
			fail(w, 400, "账号配置格式不正确")
			return
		}
		id, err := s.Accounts.Add(r.Context(), input.Token, input.Cookie, input.ProxyConfig)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		writeJSON(w, 201, map[string]string{"id": id})
	}))
	mux.HandleFunc("DELETE /api/accounts/{accountID}", s.account(func(e *Engine, w http.ResponseWriter, r *http.Request) {
		if err := s.Accounts.Remove(r.PathValue("accountID")); err != nil {
			fail(w, 500, "无法完成账号移除，请刷新后重试")
			return
		}
		writeJSON(w, 200, map[string]bool{"removed": true})
	}))
	mux.HandleFunc("GET /api/accounts/{accountID}/status", s.account(s.status))
	mux.HandleFunc("GET /api/accounts/{accountID}/config", s.account(func(e *Engine, w http.ResponseWriter, r *http.Request) {
		cfg, err := e.Store.Config()
		if err != nil {
			fail(w, 500, "无法读取已保存配置")
			return
		}
		// Only the authenticated settings view requests decrypted credentials.
		// Periodic status responses continue returning configuration flags.
		writeJSON(w, 200, cfg)
	}))
	mux.HandleFunc("GET /api/accounts/{accountID}/deliveries", s.account(s.deliveryHistory))
	mux.HandleFunc("GET /api/accounts/{accountID}/check-history", s.account(s.checkHistory))
	mux.HandleFunc("POST /api/accounts/{accountID}/push-test", s.account(s.queuePushTest))
	mux.HandleFunc("PUT /api/accounts/{accountID}/config", s.account(func(e *Engine, w http.ResponseWriter, r *http.Request) {
		var c Config
		if err := decode(w, r, &c); err != nil {
			fail(w, 400, "配置格式不正确")
			return
		}
		if c.RelayURL != "" || c.RelayToken != "" {
			fail(w, 400, "推送服务固定，请通过配对接收设备设置推送连接")
			return
		}
		if err := e.configure(c, true); err != nil {
			fail(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("PUT /api/accounts/{accountID}/proxy", s.account(func(e *Engine, w http.ResponseWriter, r *http.Request) {
		var input ProxyConfig
		if err := decode(w, r, &input); err != nil {
			fail(w, 400, "代理配置格式不正确")
			return
		}
		if err := e.ConfigureProxy(input); err != nil {
			fail(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("POST /api/accounts/{accountID}/proxy/test", s.account(func(e *Engine, w http.ResponseWriter, r *http.Request) {
		var input ProxyConfig
		if err := decode(w, r, &input); err != nil {
			fail(w, 400, "代理配置格式不正确")
			return
		}
		result, err := e.TestProxy(r.Context(), input)
		if err != nil {
			if errors.Is(err, ErrProxyTestRunning) {
				fail(w, http.StatusConflict, err.Error())
				return
			}
			fail(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, result)
	}))
	mux.HandleFunc("POST /api/accounts/{accountID}/pair", s.account(func(e *Engine, w http.ResponseWriter, r *http.Request) {
		var input struct {
			RelayURL string `json:"relay_url"`
			Code     string `json:"code"`
			Cookie   string `json:"cookie"`
		}
		if err := decode(w, r, &input); err != nil {
			fail(w, 400, "配对请求格式不正确")
			return
		}
		if input.RelayURL != "" && input.RelayURL != PushServiceURL {
			fail(w, 400, "推送服务地址固定为 "+PushServiceURL)
			return
		}
		if err := e.Pair(r.Context(), PushServiceURL, input.Code, input.Cookie); err != nil {
			fail(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"paired": true})
	}))
	mux.HandleFunc("POST /api/accounts/{accountID}/pair/qr", s.account(func(e *Engine, w http.ResponseWriter, r *http.Request) {
		var input struct {
			Cookie string `json:"cookie"`
		}
		if err := decode(w, r, &input); err != nil {
			fail(w, 400, "配对请求格式不正确")
			return
		}
		result, err := e.CreatePairingInvitation(r.Context(), input.Cookie)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, result)
	}))
	mux.HandleFunc("POST /api/accounts/{accountID}/pair/qr/complete", s.account(func(e *Engine, w http.ResponseWriter, r *http.Request) {
		var input struct {
			Code string `json:"code"`
		}
		if err := decode(w, r, &input); err != nil {
			fail(w, 400, "配对请求格式不正确")
			return
		}
		paired, err := e.CompletePairingInvitation(r.Context(), input.Code)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"paired": paired})
	}))
	mux.HandleFunc("POST /api/accounts/{accountID}/check", s.account(func(e *Engine, w http.ResponseWriter, r *http.Request) {
		if err := e.RequestCheck(); err != nil {
			if errors.Is(err, ErrQuietHours) || errors.Is(err, ErrSyncDisabled) || errors.Is(err, ErrPairingRequired) {
				fail(w, 409, err.Error())
				return
			}
			fail(w, 500, "无法保存检查请求")
			return
		}
		writeJSON(w, 202, map[string]string{"status": "scheduled"})
	}))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "接口不存在") })
	files := http.FileServer(http.FS(s.Assets))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if _, err := fs.Stat(s.Assets, "index.html"); err != nil {
			http.Error(w, "React assets are missing. Run make build before starting the server.", 503)
			return
		}
		files.ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: https://v2ex.com https://www.v2ex.com https://cdn.v2ex.com https://*.cdn.v2ex.com; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			readsConfig := (r.Method == "GET" || r.Method == "HEAD") && strings.HasSuffix(r.URL.Path, "/config")
			if readsConfig || (r.Method != "GET" && r.Method != "HEAD") {
				if r.Header.Get("X-V2Echo-Request") != "1" {
					fail(w, 403, "缺少请求校验头")
					return
				}
				if origin := r.Header.Get("Origin"); origin != "" {
					u, err := url.Parse(origin)
					if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
						fail(w, 403, "请求来源不匹配")
						return
					}
				}
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) authorized(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("notifier_session")
		if err != nil {
			fail(w, 401, "请先登录管理页")
			return
		}
		s.mu.Lock()
		expiry, ok := s.sessions[c.Value]
		if ok && !expiry.After(time.Now()) {
			delete(s.sessions, c.Value)
			ok = false
		}
		s.mu.Unlock()
		if !ok {
			fail(w, 401, "管理会话已过期，请重新登录")
			return
		}
		next(w, r)
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	now := time.Now()
	if now.Sub(s.loginWindow) > time.Minute {
		s.loginWindow = now
		s.loginAttempts = 0
	}
	s.loginAttempts++
	limited := s.loginAttempts > 10
	s.mu.Unlock()
	if limited {
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "登录尝试过于频繁，请一分钟后再试")
		return
	}
	var input struct {
		Token string `json:"token"`
	}
	if err := decode(w, r, &input); err != nil {
		fail(w, 400, "登录请求格式不正确")
		return
	}
	hash := sha256.Sum256([]byte(strings.TrimSpace(input.Token)))
	if subtle.ConstantTimeCompare(hash[:], s.adminHash[:]) != 1 {
		fail(w, 401, "管理密钥不正确")
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		fail(w, 500, "无法创建管理会话")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	s.mu.Lock()
	for k, t := range s.sessions {
		if !t.After(now) {
			delete(s.sessions, k)
		}
	}
	if len(s.sessions) >= 32 {
		s.sessions = map[string]time.Time{}
	}
	s.sessions[token] = now.Add(12 * time.Hour)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "notifier_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.SecureCookies || r.TLS != nil, MaxAge: 43200})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) status(e *Engine, w http.ResponseWriter, r *http.Request) {
	cfg, err := e.Store.Config()
	if err != nil {
		fail(w, 500, "无法读取配置")
		return
	}
	st, err := e.Store.State()
	if err != nil {
		fail(w, 500, "无法读取同步状态")
		return
	}
	deliveries, err := e.Store.RecentDeliveries()
	if err != nil {
		fail(w, 500, "无法读取上报记录")
		return
	}
	var relayNext int64
	var relayBlocked bool
	if err = e.Store.DB.QueryRow("SELECT next_sync,blocked FROM relay_schedule WHERE id=1").Scan(&relayNext, &relayBlocked); err != nil {
		fail(w, 500, "无法读取推送调度状态")
		return
	}
	var count, pending int
	if err = e.Store.DB.QueryRow("SELECT COUNT(*) FROM notifications").Scan(&count); err != nil {
		fail(w, 500, "无法读取通知数量")
		return
	}
	if err = e.Store.DB.QueryRow("SELECT COUNT(*) FROM outbox WHERE status='pending'").Scan(&pending); err != nil {
		fail(w, 500, "无法读取待处理数量")
		return
	}
	rows, err := e.Store.DB.Query(`SELECT id,created,title,body FROM notifications ORDER BY id DESC LIMIT 50`)
	if err != nil {
		fail(w, 500, "无法读取通知")
		return
	}
	defer rows.Close()
	type item struct {
		ID      int64  `json:"id"`
		Created int64  `json:"created"`
		Title   string `json:"title"`
		Body    string `json:"body"`
	}
	items := []item{}
	for rows.Next() {
		var n item
		if err = rows.Scan(&n.ID, &n.Created, &n.Title, &n.Body); err != nil {
			fail(w, 500, "读取通知失败")
			return
		}
		items = append(items, n)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取通知失败")
		return
	}
	budget, err := s.Accounts.Budget.Snapshot()
	if err != nil {
		fail(w, 500, "无法读取共享请求额度")
		return
	}
	st.Quota = budget.Quota
	st.NextAPI = maxTime(st.NextAPI, budget.Next)
	pushTest, err := e.Store.PushTestState()
	if err != nil {
		fail(w, 500, "无法读取推送测试状态")
		return
	}
	writeJSON(w, 200, map[string]any{
		"push_test":            pushTest,
		"browser":              e.browserStatus(),
		"push_schedule_status": cfg.scheduleStatus(time.Now().UTC()),
		"account_id":           r.PathValue("accountID"),
		"config":               map[string]any{"push_schedule": cfg.schedule(), "proxy_mode": cfg.ProxyMode, "proxy_url_configured": cfg.ProxyURL != "", "proxy_address": proxyAddress(cfg.ProxyURL), "enabled": cfg.Enabled, "interval_seconds": cfg.IntervalSeconds, "relay_url": cfg.RelayURL, "api_token_configured": cfg.APIToken != "", "relay_token_configured": cfg.RelayToken != "", "cookie_configured": cfg.Cookie != ""},
		"relay_next_sync":      relayNext, "relay_blocked": relayBlocked,
		"state": st, "notification_count": count, "pending_count": pending, "notifications": items, "deliveries": deliveries,
	})
}

func (s *Server) account(next func(*Engine, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return s.authorized(func(w http.ResponseWriter, r *http.Request) {
		e, ok := s.Accounts.Get(r.PathValue("accountID"))
		if !ok {
			fail(w, 404, "账号配置不存在或已移除")
			return
		}
		next(e, w, r)
	})
}
