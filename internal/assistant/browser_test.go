package assistant

import (
	"context"
	"encoding/json"
	"golang.org/x/net/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestBrowserFailureDiagnosticsDoNotExposeCompanionContent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"stage", 502, `{"stage":"homepage","code":"operation_failed","error":"A2=secret"}`, "读取首页"},
		{"navigation", 502, `{"stage":"navigate","code":"proxy_tunnel_failed"}`, "代理隧道"},
		{"auth", 401, `{"error":"secret"}`, "共享密钥"},
		{"unknown", 502, `{"stage":"secret","code":"secret"}`, "浏览器操作失败，请检查配套容器后重试"},
		{"legacy", 502, `<html>secret</html>`, "浏览器操作失败，请检查配套容器后重试"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := NewBrowserService("http://browser:8090", "http://browser:3000", strings.Repeat("x", 32))
			if err != nil {
				t.Fatal(err)
			}
			b.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return response(tc.status, tc.body), nil })
			_, err = b.call(t.Context(), "start", browserRequest{})
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe or missing diagnostic: %v", err)
			}
		})
	}
}

func browserFixture(t *testing.T) (*Accounts, *Engine, *BrowserService) {
	t.Helper()
	a, _ := testAccounts(t)
	id := seedTestAccount(t, a, "private-pat", "A2=private-login")
	e, _ := a.Get(id)
	cfg, _ := e.Store.Config()
	cfg.ProxyMode = "custom"
	cfg.ProxyURL = "https://user:private-proxy@proxy.example:443"
	st := State{AccountID: 7, Username: "tester", Verified: true, HasPushAPIID: true, LastPushAPIID: 123, NextAPI: time.Now().Add(time.Hour), CookieIssue: "old issue"}
	if err := e.Store.SaveConfig(cfg, st, false); err != nil {
		t.Fatal(err)
	}
	b, err := NewBrowserService("http://browser:8090", "http://browser:3000", strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	a.SetBrowserService(b)
	return a, e, b
}

func fakeBrowser(t *testing.T, b *BrowserService, fn func(string, browserRequest) browserResponse) {
	t.Helper()
	b.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("x", 32) {
			t.Error("missing private service auth")
		}
		var in browserRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		if in.Account == "" {
			t.Error("missing account isolation")
		}
		out := fn(r.URL.Path, in)
		raw, _ := json.Marshal(out)
		return response(200, string(raw)), nil
	})
}

func TestBrowserRecoveryKeepsSessionAndPushBaseline(t *testing.T) {
	_, e, b := browserFixture(t)
	calls := 0
	fakeBrowser(t, b, func(path string, in browserRequest) browserResponse {
		calls++
		if in.Cookie != "A2=private-login" || in.Proxy != "https://user:private-proxy@proxy.example:443" {
			t.Error("lost account credentials or upstream")
		}
		if calls > 1 && !strings.Contains(string(in.Cookies), "private-clearance") {
			t.Error("clearance not retained")
		}
		out := browserResponse{URL: "https://www.v2ex.com/", Status: 200, HTML: webPage("tester", 4), Cookies: json.RawMessage(`[{"name":"cf_clearance","value":"private-clearance","domain":"www.v2ex.com"}]`)}
		if path == "/start" {
			out.Status = 403
			out.Challenged = true
		}
		return out
	})
	e.API.Client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		t.Error("recovery called API")
		return response(500, ""), nil
	})
	e.Web.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		t.Error("browser mode fell back to Go")
		return response(500, ""), nil
	})
	before := stateOf(t, e.Store)
	if _, err := e.browserAction(t.Context(), "start", "viewer"); err != nil {
		t.Fatal(err)
	}
	view := b.viewContext
	if _, err := e.readWebUnread(t.Context(), "A2=private-login", "tester"); err == nil || calls != 1 {
		t.Fatal("automatic read interrupted verification")
	}
	got, err := e.browserAction(t.Context(), "check", "viewer")
	if err != nil || got.Count != 4 {
		t.Fatal(got, err)
	}
	if view.Err() == nil {
		t.Fatal("desktop stream not revoked")
	}
	after := stateOf(t, e.Store)
	if after.LastPushAPIID != before.LastPushAPIID || after.NextAPI != before.NextAPI || after.CookieIssue != "" {
		t.Fatal("recovery changed baseline/cooldown or failed to recover cookie")
	}
	if got, err = e.readWebUnread(t.Context(), "A2=private-login", "tester"); err != nil || got.Count != 4 {
		t.Fatal(got, err)
	}
	var sealed string
	if err = e.Store.DB.QueryRow("SELECT body FROM browser_session").Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "private-clearance") {
		t.Fatal("plaintext browser cookie stored")
	}
	raw, _ := json.Marshal(e.browserStatus())
	if strings.Contains(string(raw), "private-") {
		t.Fatal("status leaked credentials")
	}
}

func TestBrowserChallengeAndWrongIdentityCannotRecover(t *testing.T) {
	_, e, b := browserFixture(t)
	status, who := 403, "tester"
	fakeBrowser(t, b, func(_ string, _ browserRequest) browserResponse {
		return browserResponse{URL: "https://www.v2ex.com/", Status: status, Challenged: status == 403, HTML: webPage(who, 2)}
	})
	if _, err := e.browserAction(t.Context(), "start", "viewer"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.browserAction(t.Context(), "check", "viewer"); err == nil {
		t.Fatal("challenge accepted")
	}
	if !e.browserStatus().Required {
		t.Fatal("challenge lost")
	}
	status, who = 200, "someone_else"
	if _, err := e.browserAction(t.Context(), "check", "viewer"); err == nil {
		t.Fatal("wrong identity accepted")
	}
	if stateOf(t, e.Store).HasWebUnread {
		t.Fatal("invalid snapshot persisted")
	}
	if b.owner == "" {
		t.Fatal("failed verification released window")
	}
}

func TestManualBrowserLeaseIsolatedAndExpires(t *testing.T) {
	a, e, b := browserFixture(t)
	id := seedTestAccount(t, a, "other-pat", "A2=other")
	other, _ := a.Get(id)
	calls := 0
	fakeBrowser(t, b, func(_ string, _ browserRequest) browserResponse { calls++; return browserResponse{Status: 403} })
	if _, err := e.browserAction(t.Context(), "start", "viewer"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.browserAction(t.Context(), "start", "viewer"); err == nil {
		t.Fatal("another account stole window")
	}
	if _, err := e.browserAction(t.Context(), "start", "other-viewer"); err == nil {
		t.Fatal("another management session stole window")
	}
	if _, err := e.browserAction(t.Context(), "check", "other-viewer"); err == nil {
		t.Fatal("another management session finished verification")
	}
	if calls != 1 {
		t.Fatal("blocked actions reached browser")
	}
	b.until = time.Now().Add(-time.Second)
	if _, err := other.browserAction(t.Context(), "start", "viewer"); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserProxyChangeRevokesWindowAndCookies(t *testing.T) {
	_, e, b := browserFixture(t)
	resets := 0
	fakeBrowser(t, b, func(path string, _ browserRequest) browserResponse {
		if path == "/reset" {
			resets++
		}
		return browserResponse{Cookies: json.RawMessage(`[{"value":"private-clearance"}]`)}
	})
	if _, err := e.browserAction(t.Context(), "start", "viewer"); err != nil {
		t.Fatal(err)
	}
	view := b.viewContext
	if err := e.ConfigureProxy(ProxyConfig{Mode: "direct"}); err != nil {
		t.Fatal(err)
	}
	st, err := e.Store.browserState()
	if err != nil || len(st.Cookies) != 0 || resets != 1 || view.Err() == nil {
		t.Fatal("old proxy session survived", st, err)
	}
}

func TestDirectChallengeOffersRecoveryWithoutExpiringLogin(t *testing.T) {
	_, e, _ := browserFixture(t)
	for _, code := range []int{403, 200} {
		e.Web.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
			r := response(code, "challenge")
			r.Header.Set("cf-mitigated", "challenge")
			return r, nil
		})
		_, err := e.readWebUnread(t.Context(), "A2=private-login", "tester")
		if err == nil || !e.browserStatus().Required {
			t.Fatal("challenge not offered recovery")
		}
		webErr, ok := err.(*webRequestError)
		if !ok || webErr.auth {
			t.Fatal("challenge expired login")
		}
	}
}

func TestBrowserRoutesRequireSessionAndDesktopDoesNotForwardCredentials(t *testing.T) {
	a, e, b := browserFixture(t)
	fakeBrowser(t, b, func(_ string, _ browserRequest) browserResponse { return browserResponse{Status: 403} })
	s, err := NewServer(a, fstest.MapFS{"index.html": {Data: []byte("test")}}, a.dir)
	if err != nil {
		t.Fatal(err)
	}
	s.sessions["viewer"] = time.Now().Add(time.Hour)
	h := s.Handler()
	request := func(path, method, session, origin, header string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader("{}"))
		if session != "" {
			r.AddCookie(&http.Cookie{Name: "notifier_session", Value: session})
		}
		r.Header.Set("Origin", origin)
		r.Header.Set("X-V2Echo-Request", header)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	start := "/api/accounts/" + e.profileID + "/browser/start"
	for _, tc := range []struct {
		session, origin, header string
		want                    int
	}{{"", "", "1", 401}, {"viewer", "http://evil.example", "1", 403}, {"viewer", "", "", 403}} {
		if res := request(start, "POST", tc.session, tc.origin, tc.header); res.Code != tc.want {
			t.Fatal(res.Code, tc.want)
		}
	}
	if res := request(start, "POST", "viewer", "http://localhost", "1"); res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	b.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("management credential forwarded to desktop")
		}
		if r.URL.Path != "/browser-desktop/" {
			t.Error("subfolder stripped")
		}
		return response(200, "desktop"), nil
	})
	if res := request("/browser-desktop/", "GET", "", "", ""); res.Code != 401 {
		t.Fatal("unauthorized desktop")
	}
	if res := request("/browser-desktop/", "GET", "viewer", "http://evil.example", ""); res.Code != 403 {
		t.Fatal("cross-origin desktop")
	}
	if res := request("/browser-desktop/", "GET", "viewer", "", ""); res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	b.until = time.Now().Add(-time.Second)
	if res := request("/browser-desktop/", "GET", "viewer", "", ""); res.Code != 409 {
		t.Fatal("expired desktop reachable")
	}
}

func TestBrowserFailureNeverFallsBackToDirect(t *testing.T) {
	_, e, b := browserFixture(t)
	if err := e.Store.saveBrowser(browserState{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	b.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return nil, io.ErrUnexpectedEOF })
	e.Web.Transport = transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("direct fallback"); return nil, nil })
	if _, err := e.readWebUnread(t.Context(), "A2=private-login", "tester"); err == nil {
		t.Fatal("missing error")
	}
}

func TestDisableMissingBrowserPreservesAccountAndScheduling(t *testing.T) {
	for _, cookie := range []string{"A2=fake-cookie", "malformed-cookie"} {
		t.Run(cookie, func(t *testing.T) {
			e, _, _, apiCalls := hybridFixture(t)
			cfg, _ := e.Store.Config()
			cfg.Cookie, cfg.ProxyMode, cfg.ProxyURL = cookie, "custom", "http://user:private-proxy@proxy.example:8080"
			st := stateOf(t, e.Store)
			st.HasPushAPIID, st.LastPushAPIID = true, 30
			st.NextAPI, st.NextWeb, st.NextCheck = time.Now().Add(time.Hour), time.Now().Add(time.Minute), time.Now().Add(time.Minute)
			st.CookieIssue = "existing cookie issue"
			st.LastError = "尚未部署浏览器配套容器，请按部署文档启用浏览器验证"
			if err := e.Store.SaveConfig(cfg, st, false); err != nil {
				t.Fatal(err)
			}
			if err := e.Store.saveBrowser(browserState{Enabled: true, Required: true, Cookies: json.RawMessage(`[{"value":"private-clearance"}]`)}); err != nil {
				t.Fatal(err)
			}
			if _, err := e.browserAction(t.Context(), "disable", "viewer"); err != nil {
				t.Fatal(err)
			}
			browser, _ := e.Store.browserState()
			after := stateOf(t, e.Store)
			if browser.Enabled || browser.Required || len(browser.Cookies) != 0 || after.LastError != "" {
				t.Fatal("stale browser state remains")
			}
			if after.LastPushAPIID != 30 || !after.NextAPI.Equal(st.NextAPI) || !after.NextWeb.Equal(st.NextWeb) || !after.NextCheck.Equal(st.NextCheck) || after.CookieIssue != st.CookieIssue || *apiCalls != 0 {
				t.Fatal("disabling changed credentials, baseline or cooldown")
			}
			saved, _ := e.Store.Config()
			if saved.Cookie != cfg.Cookie || saved.ProxyURL != cfg.ProxyURL || saved.RelayToken != cfg.RelayToken {
				t.Fatal("saved configuration changed")
			}
			if cookie == "A2=fake-cookie" {
				if _, err := e.readWebUnread(t.Context(), cookie, "tester"); err != nil {
					t.Fatalf("ordinary homepage path did not recover: %v", err)
				}
			}
		})
	}
}

func TestDisableMissingBrowserKeepsUnrelatedError(t *testing.T) {
	e, _, _, _ := hybridFixture(t)
	st := stateOf(t, e.Store)
	st.LastError = "an unrelated failure"
	if err := e.Store.SaveState(st); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.saveBrowser(browserState{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.browserAction(t.Context(), "disable", "viewer"); err != nil {
		t.Fatal(err)
	}
	if stateOf(t, e.Store).LastError != st.LastError {
		t.Fatal("unrelated failure was hidden")
	}
}

func TestNewAccountWithChallengeCanInstallBrowserLaterWithoutStartingSync(t *testing.T) {
	a, _ := testAccounts(t)
	b, err := NewBrowserService("http://browser:8090", "http://browser:3000", strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	a.verificationTransport = func(ProxyConfig) http.RoundTripper {
		return transportFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/api/v2/member" {
				return response(200, `{"success":true,"result":{"id":7,"username":"tester"}}`), nil
			}
			return response(403, "challenge"), nil
		})
	}
	id, err := a.Add(t.Context(), "synthetic-pat", "A2=synthetic", ProxyConfig{Mode: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	e, _ := a.Get(id)
	cfg, _ := e.Store.Config()
	st := stateOf(t, e.Store)
	if cfg.Enabled || st.HasWebUnread || !st.CookieCheckedAt.IsZero() || !e.browserStatus().Required || st.InitialPairingDone {
		t.Fatal("pending account treated as verified webpage")
	}
	if e.browserStatus().Available {
		t.Fatal("companion advertised before installation")
	}
	// Installing the optional companion recreates notifier while preserving data.
	restarted, err := OpenAccounts(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restarted.SetBrowserService(b)
	e, _ = restarted.Get(id)
	if !e.browserStatus().Required || !e.browserStatus().Available {
		t.Fatal("pending verification did not survive companion installation")
	}
	fakeBrowser(t, b, func(_ string, _ browserRequest) browserResponse {
		return browserResponse{URL: "https://www.v2ex.com/", Status: 200, HTML: webPage("tester", 2)}
	})
	if _, err = e.browserAction(t.Context(), "start", "viewer"); err != nil {
		t.Fatal(err)
	}
	if _, err = e.browserAction(t.Context(), "check", "viewer"); err != nil {
		t.Fatal(err)
	}
	cfg, _ = e.Store.Config()
	st = stateOf(t, e.Store)
	if cfg.Enabled || !st.HasWebUnread || st.CookieCheckedAt.IsZero() || st.HasPushAPIID {
		t.Fatal("recovery started syncing or failed identity check")
	}
	if queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox") != 0 {
		t.Fatal("recovery sent push")
	}
}

func TestBrowserCookiesSurviveNotifierRestartAndAreErasedWithAccount(t *testing.T) {
	a, e, b := browserFixture(t)
	fakeBrowser(t, b, func(_ string, _ browserRequest) browserResponse {
		return browserResponse{URL: "https://www.v2ex.com/", Status: 200, HTML: webPage("tester", 2), Cookies: json.RawMessage(`[{"name":"cf_clearance","value":"synthetic-persisted","domain":"www.v2ex.com"}]`)}
	})
	if _, err := e.browserAction(t.Context(), "start", "viewer"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.browserAction(t.Context(), "check", "viewer"); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenAccounts(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restarted.SetBrowserService(b)
	restored, ok := restarted.Get(e.profileID)
	if !ok {
		t.Fatal("account missing after restart")
	}
	fakeBrowser(t, b, func(path string, in browserRequest) browserResponse {
		if path != "/reset" && !strings.Contains(string(in.Cookies), "synthetic-persisted") {
			t.Error("saved browser session lost")
		}
		return browserResponse{URL: "https://www.v2ex.com/", Status: 200, HTML: webPage("tester", 2)}
	})
	if _, err := restored.readWebUnread(t.Context(), "A2=private-login", "tester"); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Remove(e.profileID); err != nil {
		t.Fatal(err)
	}
	if queryInt(t, e.Store, "SELECT COUNT(*) FROM browser_session") != 0 {
		t.Fatal("removed account retained browser credentials")
	}
}

func TestDesktopWebSocketClosesWhenVerificationLeaseEnds(t *testing.T) {
	upstream := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		if ws.Request().Header.Get("Cookie") != "" {
			t.Error("management cookie leaked to Selkies")
		}
		_ = websocket.Message.Send(ws, "ready")
		_, _ = io.Copy(io.Discard, ws)
	}))
	defer upstream.Close()
	a, _ := testAccounts(t)
	b, err := NewBrowserService("http://control.invalid", upstream.URL, strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	a.SetBrowserService(b)
	b.owner, b.viewer, b.until = "account", "viewer", time.Now().Add(time.Minute)
	b.viewContext, b.cancelView = context.WithDeadline(t.Context(), b.until)
	defer b.cancelView()
	s, err := NewServer(a, fstest.MapFS{"index.html": {Data: []byte("test")}}, a.dir)
	if err != nil {
		t.Fatal(err)
	}
	s.sessions["viewer"] = time.Now().Add(time.Hour)
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	cfg, err := websocket.NewConfig("ws"+strings.TrimPrefix(server.URL, "http")+"/browser-desktop/websocket", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Header.Set("Cookie", "notifier_session=viewer")
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	var message string
	if err = websocket.Message.Receive(conn, &message); err != nil || message != "ready" {
		t.Fatal(message, err)
	}
	b.mu.Lock()
	b.releaseLocked()
	b.mu.Unlock()
	if err = websocket.Message.Receive(conn, &message); err == nil {
		t.Fatal("revoked desktop connection remained active")
	}
	if timeout, ok := err.(interface{ Timeout() bool }); ok && timeout.Timeout() {
		t.Fatal("revoked desktop only closed by test timeout")
	}
}

func TestBrowserLoginRedirectStillRequiresCookieUpdate(t *testing.T) {
	_, e, b := browserFixture(t)
	if err := e.Store.saveBrowser(browserState{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	fakeBrowser(t, b, func(_ string, _ browserRequest) browserResponse {
		return browserResponse{URL: "https://www.v2ex.com/", Status: 302, LoginRequired: true}
	})
	_, err := e.readWebUnread(t.Context(), "A2=private-login", "tester")
	problem, ok := err.(*webRequestError)
	if !ok || !problem.auth || e.browserStatus().Required {
		t.Fatal("login redirect misclassified as CF challenge", err)
	}
}
