package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestManagementAuthenticationAndSecretRedaction(t *testing.T) {
	dir := t.TempDir()
	accounts, err := OpenAccounts(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(accounts.Close)
	id, err := accounts.Add("fake-test-token")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(accounts, fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>test</title>")}}, dir)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	request := func(method, path, body, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		req.Header.Set("X-V2Echo-Request", "1")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}
	if res := request("GET", "/api/accounts/"+id+"/status", "", "", nil); res.Code != 401 {
		t.Fatal("private status exposed")
	}
	if res := request("GET", "/api/accounts/"+id+"/deliveries", "", "", nil); res.Code != 401 {
		t.Fatal("push history exposed without login")
	}
	for _, operation := range []struct{ method, path string }{{"PUT", "proxy"}, {"POST", "proxy/test"}} {
		if res := request(operation.method, "/api/accounts/"+id+"/"+operation.path, `{"proxy_mode":"direct"}`, "", nil); res.Code != 401 {
			t.Fatal("proxy route exposed without authentication")
		}
	}
	token, err := os.ReadFile(filepath.Join(dir, "admin-token"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"token": strings.TrimSpace(string(token))})
	if res := request("POST", "/api/login", string(raw), "http://attacker.example", nil); res.Code != 403 {
		t.Fatal("cross-origin login allowed")
	}
	login := request("POST", "/api/login", string(raw), "http://localhost", nil)
	if login.Code != 200 {
		t.Fatal(login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe session cookie")
	}
	res := request("GET", "/api/accounts/"+id+"/status", "", "", cookies[0])
	if res.Code != 200 || strings.Contains(res.Body.String(), "fake-test-token") {
		t.Fatal("status leaked secrets or failed")
	}
	if !strings.Contains(res.Body.String(), `"api_token_configured":true`) {
		t.Fatal("missing safe config state")
	}
	if res = request("PUT", "/api/accounts/"+id+"/config", `{"interval_seconds":180,"relay_url":"http://unsafe.example"}`, "", cookies[0]); res.Code != 400 {
		t.Fatal("insecure relay URL accepted")
	}
	if res = request("PUT", "/api/accounts/"+id+"/config", `{"interval_seconds":180,"relay_url":"https://safe.example?token=secret"}`, "", cookies[0]); res.Code != 400 {
		t.Fatal("query credential URL accepted")
	}

	if res = request("PUT", "/api/accounts/"+id+"/config", `{"interval_seconds":180,"enabled":true,"proxy_mode":"custom","proxy_url":"http://proxy-login:proxy-secret@proxy.example:8080"}`, "", cookies[0]); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	proxyStatus := request("GET", "/api/accounts/"+id+"/status", "", "", cookies[0])
	if proxyStatus.Code != 200 || strings.Contains(proxyStatus.Body.String(), "proxy-login") || strings.Contains(proxyStatus.Body.String(), "proxy-secret") || !strings.Contains(proxyStatus.Body.String(), `"proxy_address":"http://proxy.example:8080"`) {
		t.Fatal("proxy status redaction failed")
	}
	added := request("POST", "/api/accounts", `{"api_token":"second-secret"}`, "", cookies[0])
	var second struct {
		ID string `json:"id"`
	}
	if added.Code != 201 || json.Unmarshal(added.Body.Bytes(), &second) != nil || second.ID == id || second.ID == "" {
		t.Fatal("account creation failed")
	}
	list := request("GET", "/api/accounts", "", "", cookies[0])
	if list.Code != 200 || !strings.Contains(list.Body.String(), second.ID) || strings.Contains(list.Body.String(), "secret") || strings.Contains(list.Body.String(), "fake-test-token") {
		t.Fatal("list missing account or exposing credentials")
	}
	if res = request("PUT", "/api/accounts/"+second.ID+"/config", `{"interval_seconds":300,"enabled":false}`, "", cookies[0]); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	one, _ := accounts.Get(id)
	one.network.base.DialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("synthetic proxy-secret failure")
	}
	if res = request("PUT", "/api/accounts/"+id+"/proxy", `{"proxy_mode":"custom"}`, "", cookies[0]); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	if res = request("POST", "/api/accounts/"+id+"/proxy/test", `{"proxy_mode":"custom","proxy_url":"invalid"}`, "", cookies[0]); res.Code != 400 {
		t.Fatal("invalid probe accepted")
	}
	if res = request("POST", "/api/accounts/"+id+"/proxy/test", `{"proxy_mode":"custom"}`, "", cookies[0]); res.Code != 200 || strings.Contains(res.Body.String(), "proxy-secret") || !strings.Contains(res.Body.String(), `"checks":[`) {
		t.Fatal("test response invalid or leaked credentials")
	}
	if res = request("POST", "/api/accounts/"+id+"/proxy/test", `{"proxy_mode":"custom"}`, "", cookies[0]); res.Code != 429 || res.Header().Get("Retry-After") == "" {
		t.Fatal("probe route did not enforce cooldown")
	}
	oneConfig, _ := one.Store.Config()
	if !oneConfig.Enabled || oneConfig.IntervalSeconds != 180 {
		t.Fatal("config changed the wrong account")
	}
	historyEvent, _ := json.Marshal(Event{EventID: strings.Repeat("e", 64), Type: "notification", Title: "历史测试", Body: "本地上报摘要"})
	if _, err := one.Store.DB.Exec("INSERT INTO outbox(event_id,payload) VALUES(?,?)", strings.Repeat("e", 64), string(historyEvent)); err != nil {
		t.Fatal(err)
	}
	history := request("GET", "/api/accounts/"+id+"/deliveries?limit=20", "", "", cookies[0])
	if history.Code != 200 || !strings.Contains(history.Body.String(), "历史测试") || strings.Contains(history.Body.String(), "fake-test-token") || strings.Contains(history.Body.String(), "proxy-secret") {
		t.Fatal("history response failed or leaked credentials")
	}
	otherHistory := request("GET", "/api/accounts/"+second.ID+"/deliveries", "", "", cookies[0])
	if otherHistory.Code != 200 || !strings.Contains(otherHistory.Body.String(), `"items":[]`) {
		t.Fatal("history crossed account boundary")
	}
	for _, query := range []string{"limit=0", "limit=51", "before=-1", "before=bad", "status=unknown"} {
		if r := request("GET", "/api/accounts/"+id+"/deliveries?"+query, "", "", cookies[0]); r.Code != 400 {
			t.Fatal("invalid history query accepted", query)
		}
	}

	// Old snapshots must not cause the notifier to infer local read state.
	snapshot := stateOf(t, one.Store)
	snapshot.InitialPairingDone, snapshot.InitialUnreadCount, snapshot.InitialUnreadAt = true, 2, time.Unix(1000, 0)
	var items []Notification
	for i := int64(1); i <= 4; i++ {
		n := notification(i)
		n.Created = 997 + i
		items = append(items, n)
	}
	if err := one.Store.ImportPage(snapshot, items, false); err != nil {
		t.Fatal(err)
	}
	marked := request("GET", "/api/accounts/"+id+"/status", "", "", cookies[0])
	var view struct {
		Notifications []struct {
			ID      int64 `json:"id"`
			Initial bool  `json:"initial_unread_estimate"`
		} `json:"notifications"`
	}
	if marked.Code != 200 || json.Unmarshal(marked.Body.Bytes(), &view) != nil || len(view.Notifications) != 4 {
		t.Fatal(marked.Body.String())
	}
	for _, n := range view.Notifications {
		if n.Initial {
			t.Fatal("unexpected local unread estimate", n)
		}
	}
	for _, path := range []string{"/api/status", "/api/accounts/unknown/status"} {
		if res = request("GET", path, "", "", cookies[0]); res.Code != 404 {
			t.Fatal("missing account route fell back to another account")
		}
	}
	if res = request("DELETE", "/api/accounts/"+id, "{}", "", cookies[0]); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	if res = request("GET", "/api/accounts/"+id+"/status", "", "", cookies[0]); res.Code != 404 {
		t.Fatal("deleted account accessible")
	}
	if res = request("GET", "/api/accounts/"+second.ID+"/status", "", "", cookies[0]); res.Code != 200 {
		t.Fatal("deletion affected the other account")
	}
	if res = request("POST", "/api/logout", "{}", "", cookies[0]); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	if res = request("GET", "/api/accounts/"+id+"/status", "", "", cookies[0]); res.Code != 401 {
		t.Fatal("logout did not revoke session")
	}
}

func TestPushTestManagementRoutesAndFixedService(t *testing.T) {
	dir := t.TempDir()
	accounts, err := OpenAccounts(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(accounts.Close)
	id, err := accounts.Add("synthetic-pat")
	if err != nil {
		t.Fatal(err)
	}
	e, _ := accounts.Get(id)
	cfg := Config{APIToken: "synthetic-pat", Enabled: true, IntervalSeconds: 180, RelayURL: PushServiceURL, RelayToken: "synthetic-sender"}
	st := State{AccountID: 7, Username: "tester", Verified: true, InitialPairingDone: true}
	if err := e.Store.SaveConfig(cfg, st, false); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(accounts, fstest.MapFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	server.sessions["synthetic-session"] = time.Now().Add(time.Hour)
	request := func(method, path, body string, auth bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://localhost/api/accounts/"+id+"/"+path, strings.NewReader(body))
		req.Header.Set("X-V2Echo-Request", "1")
		if auth {
			req.AddCookie(&http.Cookie{Name: "notifier_session", Value: "synthetic-session"})
		}
		out := httptest.NewRecorder()
		server.Handler().ServeHTTP(out, req)
		return out
	}
	eventID := strings.Repeat("a", 64)
	body := `{"event_id":"` + eventID + `"}`
	if r := request("POST", "push-test", body, false); r.Code != 401 {
		t.Fatal("test accessible without session")
	}
	if r := request("POST", "push-test", `{"event_id":"invalid"}`, true); r.Code != 400 {
		t.Fatal(r.Body.String())
	}
	for i := 0; i < 2; i++ {
		if r := request("POST", "push-test", body, true); r.Code != 202 {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	if r := request("POST", "push-test", `{"event_id":"`+strings.Repeat("b", 64)+`"}`, true); r.Code != 429 || r.Header().Get("Retry-After") == "" {
		t.Fatal("rate limit missing", r.Code)
	}
	r := request("GET", "status", "", true)
	if r.Code != 200 || !strings.Contains(r.Body.String(), eventID) || strings.Contains(r.Body.String(), "synthetic-sender") {
		t.Fatal("test result missing or secret leaked")
	}
	for _, fields := range []string{`"relay_url":"https://other.example"`, `"relay_url":"` + PushServiceURL + `"`, `"relay_token":"manual-secret"`} {
		if r := request("PUT", "config", `{"interval_seconds":180,`+fields+`}`, true); r.Code != 400 {
			t.Fatal("manual relay override accepted")
		}
	}
	if r := request("PUT", "config", `{"interval_seconds":300,"enabled":true}`, true); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	saved, _ := e.Store.Config()
	if saved.RelayURL != cfg.RelayURL || saved.RelayToken != cfg.RelayToken {
		t.Fatal("settings save lost pairing")
	}
	if r := request("POST", "pair", `{"relay_url":"https://other.example","code":"V2E-`+strings.Repeat("a", 24)+`"}`, true); r.Code != 400 {
		t.Fatal("custom pairing destination accepted")
	}
	e.Relay = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != PushServiceURL+"/v1/pairings/exchange" {
			t.Fatal("pair did not use fixed service")
		}
		return response(200, `{"paired":true,"binding_id":"synthetic-binding","username":"tester"}`), nil
	})}
	if r := request("POST", "pair", `{"code":"V2E-`+strings.Repeat("a", 24)+`"}`, true); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
}
