package assistant

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestSavedConfigurationRequiresSessionAndSameOriginAndStaysAccountScoped(t *testing.T) {
	accounts, dir := testAccounts(t)
	id, engine := addTestAccount(t, accounts, "private-pat", 7, "tester")
	otherID, _ := addTestAccount(t, accounts, "other-pat", 8, "second")
	cfg := Config{PushSchedule: allDaySchedule(), APIToken: "private-pat", Cookie: "A2=private-cookie", ProxyMode: "custom",
		ProxyURL: "http://proxy-user:proxy-password@proxy.example:8080", RelayURL: PushServiceURL,
		RelayToken: "private-sender-token", IntervalSeconds: 240, Enabled: true}
	if err := engine.Store.SaveConfig(cfg, State{AccountID: 7, Username: "tester", Verified: true}, false); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(accounts, fstest.MapFS{"index.html": {Data: []byte("test")}}, dir)
	if err != nil {
		t.Fatal(err)
	}
	server.sessions["synthetic-session"] = time.Now().Add(time.Hour)
	handler := server.Handler()
	request := func(path string, session bool, origin, header string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "http://localhost/api/accounts/"+path, nil)
		if session {
			r.AddCookie(&http.Cookie{Name: "notifier_session", Value: "synthetic-session"})
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if header != "" {
			r.Header.Set("X-V2Echo-Request", header)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		session        bool
		origin, header string
		code           int
	}{
		{false, "http://localhost", "1", 401},
		{true, "http://attacker.example", "1", 403},
		{true, "http://localhost", "", 403},
	} {
		res := request(id+"/config", tc.session, tc.origin, tc.header)
		if res.Code != tc.code || strings.Contains(res.Body.String(), "private-") {
			t.Fatal("unauthorized configuration response", res.Code)
		}
	}
	res := request(id+"/config", true, "http://localhost", "1")
	var saved Config
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &saved) != nil || !reflect.DeepEqual(saved, cfg) {
		t.Fatal("saved configuration did not round trip")
	}
	if res.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("credentials could be cached")
	}
	other := request(otherID+"/config", true, "http://localhost", "1")
	var second Config
	if other.Code != 200 || json.Unmarshal(other.Body.Bytes(), &second) != nil || second.APIToken != "other-pat" || second.Cookie != "" || second.RelayToken != "" || second.ProxyURL != "" {
		t.Fatal("configuration crossed account boundary")
	}
	status := request(id+"/status", true, "http://localhost", "1")
	if status.Code != 200 {
		t.Fatal("status request failed")
	}
	for _, secret := range []string{cfg.APIToken, cfg.Cookie, cfg.RelayToken, "proxy-password"} {
		if strings.Contains(status.Body.String(), secret) {
			t.Fatal("periodic status returned a credential")
		}
	}
	if missing := request("missing/config", true, "http://localhost", "1"); missing.Code != 404 {
		t.Fatal("missing account fell back to another configuration")
	}
	// The form sends editable fields only. Re-saving a filled token must not
	// reset verification or overwrite the pairing/proxy configuration.
	raw, _ := json.Marshal(map[string]any{"api_token": saved.APIToken, "cookie": saved.Cookie, "interval_seconds": 300, "enabled": true})
	update := httptest.NewRequest(http.MethodPut, "http://localhost/api/accounts/"+id+"/config", strings.NewReader(string(raw)))
	update.Header.Set("X-V2Echo-Request", "1")
	update.AddCookie(&http.Cookie{Name: "notifier_session", Value: "synthetic-session"})
	updated := httptest.NewRecorder()
	handler.ServeHTTP(updated, update)
	if updated.Code != 200 {
		t.Fatal("prefilled configuration could not be saved")
	}
	after, err := engine.Store.Config()
	if err != nil || after.APIToken != cfg.APIToken || after.Cookie != cfg.Cookie || after.RelayToken != cfg.RelayToken || after.ProxyURL != cfg.ProxyURL || after.IntervalSeconds != 300 || !stateOf(t, engine.Store).Verified {
		t.Fatal("saving a prefilled form changed unrelated settings or verification")
	}
	server.sessions["synthetic-session"] = time.Now().Add(-time.Second)
	if expired := request(id+"/config", true, "http://localhost", "1"); expired.Code != 401 {
		t.Fatal("expired session could read credentials")
	}
}
