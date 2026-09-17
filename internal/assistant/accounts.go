package assistant

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrAccountRemoved = errors.New("账号配置已移除")
var profileID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Each account owns its database and encryption key. The registry contains only
// profile membership and the budget shared by this notifier's outbound calls.
type Accounts struct {
	Browser               *BrowserService
	mu                    sync.Mutex
	DB                    *sql.DB
	dir                   string
	engines               map[string]*Engine
	retired               []*Store
	Budget                *APIBudget
	verificationTransport func(ProxyConfig) http.RoundTripper
}

type AccountSummary struct {
	AvatarURL        string `json:"avatar_url"`
	BrowserRequired  bool   `json:"browser_required"`
	ID               string `json:"id"`
	Username         string `json:"username"`
	MemberID         int64  `json:"member_id"`
	Enabled          bool   `json:"enabled"`
	Verified         bool   `json:"verified"`
	Blocked          bool   `json:"blocked"`
	CookieConfigured bool   `json:"cookie_configured"`
	CookieVerified   bool   `json:"cookie_verified"`
	TokenIssue       string `json:"token_issue"`
	CookieIssue      string `json:"cookie_issue"`
	LastError        string `json:"last_error"`
}

func OpenAccounts(dir string) (*Accounts, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "accounts.db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS managed_accounts (id TEXT PRIMARY KEY,member_id INTEGER UNIQUE,deleted INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS api_budget (id INTEGER PRIMARY KEY CHECK(id=1),body TEXT NOT NULL);`); err != nil {
		db.Close()
		return nil, err
	}
	a := &Accounts{DB: db, dir: dir, engines: map[string]*Engine{}, Budget: &APIBudget{DB: db}}
	a.verificationTransport = func(proxy ProxyConfig) http.RoundTripper {
		return proxyTransport(http.DefaultTransport.(*http.Transport), proxy.Mode, proxy.URL)
	}
	rows, err := db.Query("SELECT id,deleted FROM managed_accounts ORDER BY rowid")
	if err != nil {
		db.Close()
		return nil, err
	}
	type record struct {
		id      string
		deleted bool
	}
	var records []record
	for rows.Next() {
		var r record
		if err = rows.Scan(&r.id, &r.deleted); err != nil {
			rows.Close()
			db.Close()
			return nil, err
		}
		records = append(records, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		db.Close()
		return nil, err
	}
	for _, r := range records {
		if !profileID.MatchString(r.id) {
			a.Close()
			return nil, errors.New("invalid stored account profile")
		}
		store, err := OpenStore(filepath.Join(dir, "accounts", r.id))
		if err != nil {
			a.Close()
			return nil, err
		}
		if r.deleted {
			err = store.EraseAccount()
			store.DB.Close()
			if err != nil {
				a.Close()
				return nil, err
			}
			continue
		}
		a.attach(r.id, NewEngine(store))
	}
	return a, nil
}

func (a *Accounts) attach(id string, e *Engine) {
	e.Budget = a.Budget
	e.Browser = a.Browser
	e.profileID = id
	e.ClaimAccount = func(member Member) error {
		var owner string
		err := a.DB.QueryRow("SELECT id FROM managed_accounts WHERE member_id=? AND deleted=0", member.ID).Scan(&owner)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if owner != "" && owner != id {
			return errors.New("此 V2EX 账号已经添加，请使用已有配置并移除重复项")
		}
		result, err := a.DB.Exec("UPDATE managed_accounts SET member_id=? WHERE id=? AND deleted=0 AND (member_id IS NULL OR member_id=?)", member.ID, id, member.ID)
		if err != nil {
			return errors.New("此 V2EX 账号已经添加，无法绑定到重复配置")
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrAccountRemoved
		}
		return nil
	}
	a.engines[id] = e
}

func (a *Accounts) Add(ctx context.Context, token, cookie string, proxy ProxyConfig) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 4096 || strings.ContainsAny(token, "\r\n") {
		return "", errors.New("请填写有效的 V2EX API Token")
	}
	var err error
	cookie, err = normalizeWebCookie(cookie)
	if err != nil {
		return "", err
	}
	proxy, err = resolveProxy(proxy, Config{})
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", errors.New("账号验证已取消或超时，请重试")
	}
	a.mu.Lock()
	full := len(a.engines) >= 20
	a.mu.Unlock()
	if full {
		return "", errors.New("最多配置 20 个账号")
	}
	// Verify in memory before creating a profile or persisting either credential.
	// Do not hold the registry lock during network requests.
	now := time.Now().UTC()
	allowed, err := a.Budget.Reserve(now)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", errors.New("V2EX API 正在冷却或额度不足，账号尚未保存，请稍后重试")
	}
	transport := a.verificationTransport(proxy)
	if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
		defer closer.CloseIdleConnections()
	}
	apiClient, webClient := secureClient(20*time.Second), secureClient(10*time.Second)
	apiClient.Transport, webClient.Transport = transport, transport
	verifier := &Engine{API: &V2EX{"https://www.v2ex.com/api/v2/", apiClient}, Web: webClient, Budget: a.Budget}
	var member Member
	headers, err := verifier.requestAPI(ctx, token, "member", &member, now)
	if err != nil {
		return "", err
	}
	if member.ID <= 0 || !webUsername.MatchString(member.Username) {
		return "", errors.New("无法确认 API Token 所属账号，账号尚未保存")
	}
	snapshot, err := verifier.readWebUnread(ctx, cookie, member.Username)
	pendingBrowser := false
	if err != nil {
		problem, ok := errors.AsType[*webRequestError](err)
		pendingBrowser = ok && problem.challenge
		if !pendingBrowser {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", errors.New("账号验证已取消或超时，请重试")
	}
	st := State{AccountID: member.ID, Username: member.Username, AvatarURL: snapshot.AvatarURL, Verified: true, Phase: "history", Page: 1,
		CookieCheckedAt: snapshot.ObservedAt, HasWebUnread: true, WebUnreadCount: snapshot.Count, WebObservedAt: snapshot.ObservedAt}
	if pendingBrowser {
		st.HasWebUnread = false
		st.LastError = "API 身份已确认，首页尚待浏览器验证；完成同账号校验后再配对并开启同步"
	}
	st.tokenValid(now)
	st.Quota.Observe(headers, now)
	if !st.Quota.Observed {
		st.Quota.Remaining--
	}
	st.NextAPI = now.Add(st.Quota.Delay(now))

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.engines) >= 20 {
		return "", errors.New("最多配置 20 个账号")
	}
	var existing int
	if err := a.DB.QueryRow("SELECT COUNT(*) FROM managed_accounts WHERE member_id=? AND deleted=0", member.ID).Scan(&existing); err != nil {
		return "", err
	}
	if existing != 0 {
		return "", errors.New("此 V2EX 账号已经添加，请使用已有配置")
	}
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	id := hex.EncodeToString(bytes)
	s, err := OpenStore(filepath.Join(a.dir, "accounts", id))
	if err != nil {
		return "", err
	}
	err = s.SaveConfig(Config{APIToken: token, Cookie: cookie, ProxyMode: proxy.Mode, ProxyURL: proxy.URL, Enabled: !pendingBrowser, IntervalSeconds: 180}, st, false)
	if err != nil {
		_ = s.EraseAccount()
		s.DB.Close()
		return "", err
	}
	if pendingBrowser {
		if err = s.saveBrowser(browserState{Required: true}); err != nil {
			_ = s.EraseAccount()
			s.DB.Close()
			return "", err
		}
	}
	if _, err = a.DB.Exec("INSERT INTO managed_accounts(id,member_id) VALUES(?,?)", id, member.ID); err != nil {
		_ = s.EraseAccount()
		s.DB.Close()
		return "", err
	}
	a.attach(id, NewEngine(s))
	return id, nil
}

func (a *Accounts) Get(id string) (*Engine, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.engines[id]
	return e, ok
}

func (a *Accounts) List() ([]AccountSummary, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]AccountSummary, 0, len(a.engines))
	for id, e := range a.engines {
		cfg, err := e.Store.Config()
		if err != nil {
			return nil, err
		}
		st, err := e.Store.State()
		if err != nil {
			return nil, err
		}
		result = append(result, AccountSummary{BrowserRequired: e.browserStatus().Required, ID: id, Username: st.Username, AvatarURL: st.AvatarURL, MemberID: st.AccountID, Enabled: cfg.Enabled, Verified: st.Verified, Blocked: st.AuthBlocked || st.CookieIssue != "", CookieConfigured: cfg.Cookie != "", CookieVerified: !st.CookieCheckedAt.IsZero() && st.CookieIssue == "", TokenIssue: st.TokenIssue, CookieIssue: st.CookieIssue, LastError: st.LastError})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Username == result[j].Username {
			return result[i].ID < result[j].ID
		}
		return result[i].Username < result[j].Username
	})
	return result, nil
}

func (a *Accounts) Remove(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.engines[id]
	if !ok {
		return ErrAccountRemoved
	}
	e.relayMu.Lock()
	defer e.relayMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Browser != nil {
		b := e.Browser
		b.mu.Lock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, resetErr := b.call(ctx, "reset", browserRequest{Account: id})
		cancel()
		if b.owner == id {
			b.releaseLocked()
		}
		b.mu.Unlock()
		session, stateErr := e.Store.browserState()
		if stateErr != nil {
			return stateErr
		}
		if resetErr != nil && session.Enabled {
			return errors.New("请先恢复浏览器容器，以清除该账号的活动会话后再移除")
		}
	}
	// Persist the tombstone first; startup repeats erasure after any interruption.
	if _, err := a.DB.Exec("UPDATE managed_accounts SET deleted=1,member_id=NULL WHERE id=?", id); err != nil {
		return err
	}
	e.removed = true
	if e.network != nil {
		e.network.CloseIdleConnections()
	}
	delete(a.engines, id)
	a.retired = append(a.retired, e.Store)
	return e.Store.EraseAccount()
}

func (a *Accounts) Close() {
	for _, e := range a.engines {
		if e.network != nil {
			e.network.CloseIdleConnections()
		}
		e.Store.DB.Close()
	}
	for _, s := range a.retired {
		s.DB.Close()
	}
	a.DB.Close()
}

func (a *Accounts) next(cursor *int) *Engine {
	a.mu.Lock()
	defer a.mu.Unlock()
	ids := make([]string, 0, len(a.engines))
	for id := range a.engines {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return nil
	}
	e := a.engines[ids[*cursor%len(ids)]]
	*cursor = (*cursor + 1) % len(ids)
	return e
}

func (a *Accounts) collect(ctx context.Context, now time.Time, cursor *int) error {
	previous := *cursor
	e := a.next(cursor)
	if e == nil {
		return nil
	}
	cfg, err := e.Store.Config()
	if err != nil {
		return err
	}
	st, err := e.Store.State()
	if err != nil {
		return err
	}
	if !cfg.Enabled || !cfg.allowsPush(now) {
		return nil
	}
	if blocked, err := e.Store.RelayBlocked(); err != nil || blocked {
		return err
	}
	if canReadWeb(cfg, st) {
		return e.Step(ctx, now)
	}
	ready, err := a.Budget.Ready(now)
	if err != nil || !ready {
		*cursor = previous
		if err != nil {
			return err
		}
		// Keep the API account's turn, but let other accounts read their
		// homepage while the shared API window is cooling down.
		a.mu.Lock()
		remaining := len(a.engines)
		a.mu.Unlock()
		probe := previous
		for i := 0; i < remaining; i++ {
			candidate := a.next(&probe)
			if candidate == nil {
				break
			}
			c, err := candidate.Store.Config()
			if err != nil {
				return err
			}
			s, err := candidate.Store.State()
			if err != nil {
				return err
			}
			if c.Enabled && c.allowsPush(now) && canReadWeb(c, s) && s.CookieIssue == "" && !now.Before(s.NextWeb) && !now.Before(s.NextCheck) {
				blocked, err := candidate.Store.RelayBlocked()
				if err != nil {
					return err
				}
				if blocked {
					continue
				}
				return candidate.Step(ctx, now)
			}
		}
		return nil
	}
	return e.Step(ctx, now)
}

func (a *Accounts) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, deliver := range []bool{false, true} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			cursor := 0
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				var err error
				if deliver {
					if e := a.next(&cursor); e != nil {
						err = e.Deliver(ctx, time.Now())
					}
				} else {
					err = a.collect(ctx, time.Now(), &cursor)
				}
				if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrAccountRemoved) {
					slog.Error("account background operation failed", "error", err)
				}
			}
		}()
	}
	wg.Wait()
}
