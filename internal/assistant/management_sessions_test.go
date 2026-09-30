package assistant

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func seedManagementSession(t *testing.T, s *Server, token string, expiry time.Time) {
	t.Helper()
	hash := sha256.Sum256([]byte(token))
	_, err := s.Accounts.DB.Exec(`INSERT INTO management_sessions(token_hash,admin_binding,created_at,expires_at) VALUES(?,?,?,?)
		ON CONFLICT(token_hash) DO UPDATE SET expires_at=excluded.expires_at`, hash[:], s.managementSessionBinding(), time.Now().UnixNano(), expiry.UnixNano())
	if err != nil {
		t.Fatal(err)
	}
}

func managementSessionExpiry(t *testing.T, s *Server, token string) time.Time {
	t.Helper()
	hash := sha256.Sum256([]byte(token))
	var expiry int64
	if err := s.Accounts.DB.QueryRow("SELECT expires_at FROM management_sessions WHERE token_hash=?", hash[:]).Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	return time.Unix(0, expiry)
}

func openManagementServer(t *testing.T, dir string) *Server {
	t.Helper()
	a, err := OpenAccounts(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	s, err := NewServer(a, fstest.MapFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func managementRequest(s *Server, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
	r.Header.Set("X-V2Echo-Request", "1")
	r.Header.Set("Origin", "http://localhost")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func managementLogin(t *testing.T, s *Server, dir string) *httptest.ResponseRecorder {
	t.Helper()
	secret, err := os.ReadFile(filepath.Join(dir, "admin-token"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"token": strings.TrimSpace(string(secret))})
	if err != nil {
		t.Fatal(err)
	}
	return managementRequest(s, "POST", "/api/login", string(body), nil)
}

func TestManagementSessionSurvivesRestartAndLogoutStaysRevoked(t *testing.T) {
	dir := t.TempDir()
	s := openManagementServer(t, dir)
	login := managementLogin(t, s, dir)
	if login.Code != 200 || len(login.Result().Cookies()) != 1 {
		t.Fatal("login failed", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	expiry := managementSessionExpiry(t, s, cookie.Value)
	var stored []byte
	if err := s.Accounts.DB.QueryRow("SELECT token_hash FROM management_sessions").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256([]byte(cookie.Value))
	if !bytes.Equal(stored, wantHash[:]) {
		t.Fatal("session credential was not stored as a hash")
	}
	s.Accounts.Close()
	s = openManagementServer(t, dir)
	if res := managementRequest(s, "GET", "/api/accounts", "", cookie); res.Code != 200 {
		t.Fatal("restart lost the login", res.Code)
	}
	if !managementSessionExpiry(t, s, cookie.Value).Equal(expiry) {
		t.Fatal("restart or access extended the session")
	}
	other := managementLogin(t, s, dir).Result().Cookies()[0]
	logout := managementRequest(s, "POST", "/api/logout", "{}", cookie)
	if logout.Code != 200 || logout.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout failed to clear the cookie", logout.Code)
	}
	s.Accounts.Close()
	s = openManagementServer(t, dir)
	if res := managementRequest(s, "GET", "/api/accounts", "", cookie); res.Code != 401 {
		t.Fatal("revoked session survived restart", res.Code)
	}
	if res := managementRequest(s, "GET", "/api/accounts", "", other); res.Code != 200 {
		t.Fatal("logout revoked a different browser", res.Code)
	}
}

func TestManagementSessionExpiryCleanup(t *testing.T) {
	for _, when := range []string{"startup", "login", "request"} {
		t.Run(when, func(t *testing.T) {
			dir := t.TempDir()
			s := openManagementServer(t, dir)
			seedManagementSession(t, s, "expired", time.Now().Add(-time.Second))
			seedManagementSession(t, s, "valid", time.Now().Add(time.Hour))
			switch when {
			case "startup":
				s.Accounts.Close()
				s = openManagementServer(t, dir)
			case "login":
				if res := managementLogin(t, s, dir); res.Code != 200 {
					t.Fatal("login failed", res.Code)
				}
			case "request":
				if res := managementRequest(s, "GET", "/api/accounts", "", &http.Cookie{Name: "notifier_session", Value: "expired"}); res.Code != 401 {
					t.Fatal("expired login accepted", res.Code)
				}
			}
			var expired int
			if err := s.Accounts.DB.QueryRow("SELECT COUNT(*) FROM management_sessions WHERE expires_at<=?", time.Now().UnixNano()).Scan(&expired); err != nil || expired != 0 {
				t.Fatal("expired sessions not removed", expired, err)
			}
			if ok, err := s.validManagementSession("valid", time.Now()); err != nil || !ok {
				t.Fatal("cleanup removed a valid session", err)
			}
		})
	}
}

func TestManagementSecretRotationRevokesPersistedSessions(t *testing.T) {
	dir := t.TempDir()
	s := openManagementServer(t, dir)
	seedManagementSession(t, s, "old-login", time.Now().Add(time.Hour))
	s.Accounts.Close()
	if err := os.Remove(filepath.Join(dir, "admin-token")); err != nil {
		t.Fatal(err)
	}
	s = openManagementServer(t, dir)
	var count int
	if err := s.Accounts.DB.QueryRow("SELECT COUNT(*) FROM management_sessions").Scan(&count); err != nil || count != 0 {
		t.Fatal("secret rotation did not remove old sessions", count, err)
	}
	if res := managementRequest(s, "GET", "/api/accounts", "", &http.Cookie{Name: "notifier_session", Value: "old-login"}); res.Code != 401 {
		t.Fatal("old login accepted after secret rotation", res.Code)
	}
}

func TestManagementSessionLimitEvictsOnlyOldest(t *testing.T) {
	s := openManagementServer(t, t.TempDir())
	now := time.Now()
	for i := range maxManagementSessions + 1 {
		if err := s.createManagementSession(fmt.Sprintf("login-%d", i), now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	for i := range maxManagementSessions + 1 {
		ok, err := s.validManagementSession(fmt.Sprintf("login-%d", i), now)
		if err != nil || ok != (i > 0) {
			t.Fatal("unexpected eviction", i, ok, err)
		}
	}
}

func TestManagementSessionStorageFailuresDoNotReportSuccess(t *testing.T) {
	for _, operation := range []string{"login", "logout", "authorize"} {
		t.Run(operation, func(t *testing.T) {
			dir := t.TempDir()
			s := openManagementServer(t, dir)
			seedManagementSession(t, s, "existing", time.Now().Add(time.Hour))
			cookie := &http.Cookie{Name: "notifier_session", Value: "existing"}
			var res *httptest.ResponseRecorder
			switch operation {
			case "login", "logout":
				event := "INSERT"
				if operation == "logout" {
					event = "DELETE"
				}
				if _, err := s.Accounts.DB.Exec("CREATE TRIGGER fail_session_write BEFORE " + event + " ON management_sessions BEGIN SELECT RAISE(ABORT, 'synthetic storage failure'); END"); err != nil {
					t.Fatal(err)
				}
				if operation == "login" {
					res = managementLogin(t, s, dir)
				} else {
					res = managementRequest(s, "POST", "/api/logout", "{}", cookie)
				}
			case "authorize":
				s.Accounts.Close()
				res = managementRequest(s, "GET", "/api/accounts", "", cookie)
			}
			if res.Code != 500 || len(res.Result().Cookies()) != 0 {
				t.Fatal("storage failure reported success or changed cookie", res.Code)
			}
		})
	}
}
