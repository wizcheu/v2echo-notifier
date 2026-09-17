package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func webPage(username string, count int) string {
	return fmt.Sprintf(`<html><a class="top" href="/">首页</a><a class="top" href="/member/%s">本人</a><a href="/notifications" class="fade">%d 未读提醒</a><a href="/member/other">帖子作者</a></html>`, username, count)
}

const testAvatarURL = "https://cdn.v2ex.com/avatar/0000/0000/7_large.png?m=1"

func avatarCard(username, src string) string {
	return fmt.Sprintf(`<div id="Rightbar"><div class="box"><a href="/member/%s"><img class="avatar" src="%s"></a></div></div>`, username, src)
}

func TestHomepageAvatarBelongsToAuthenticatedAccount(t *testing.T) {
	for _, tc := range []struct{ name, card, want string }{
		{"https", avatarCard("tester", testAvatarURL), testAvatarURL},
		{"protocol relative", avatarCard("tester", "//cdn.v2ex.com/avatar/7.png?m=2&amp;x=1"), "https://cdn.v2ex.com/avatar/7.png?m=2&x=1"},
		{"root relative", avatarCard("tester", "/avatar/7.png"), "https://www.v2ex.com/avatar/7.png"},
		{"http", avatarCard("tester", "http://cdn.v2ex.com/avatar/7.png"), "https://cdn.v2ex.com/avatar/7.png"},
		{"absolute member", strings.ReplaceAll(avatarCard("tester", testAvatarURL), `href="/member/`, `href="https://www.v2ex.com/member/`), testAvatarURL},
		{"other member", avatarCard("other", testAvatarURL), ""},
		{"wrong case", avatarCard("TESTER", testAvatarURL), ""},
		{"post author", strings.ReplaceAll(avatarCard("tester", testAvatarURL), "Rightbar", "Main"), ""},
		{"saved local image", avatarCard("tester", "./V2EX_files/7_large.png"), ""},
		{"untrusted image", avatarCard("tester", "https://cdn.v2ex.com.evil.example/avatar/7.png"), ""},
		{"credentials", avatarCard("tester", "https://secret@cdn.v2ex.com/avatar/7.png"), ""},
		{"data URL", avatarCard("tester", "data:image/svg+xml,test"), ""},
		{"missing", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseWebUnread(webPage("tester", 3)+tc.card, "tester")
			if err != nil || got.Count != 3 || got.AvatarURL != tc.want {
				t.Fatalf("snapshot = %+v, error = %v", got, err)
			}
		})
	}
	if got, err := parseWebUnread(webPage("other", 3)+avatarCard("tester", testAvatarURL), "tester"); err == nil || got.AvatarURL != "" {
		t.Fatalf("wrong identity yielded avatar: %+v, %v", got, err)
	}
}

func TestAccountAvatarPersistsAndRefreshesWithHomepage(t *testing.T) {
	a, _ := testAccounts(t)
	mockAccountVerification(t, a, 7, "tester")
	transport := a.verificationTransport
	a.verificationTransport = func(proxy ProxyConfig) http.RoundTripper {
		base := transport(proxy)
		return transportFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/" {
				return response(200, webPage("tester", 0)+avatarCard("tester", testAvatarURL)), nil
			}
			return base.RoundTrip(r)
		})
	}
	id, err := a.Add(t.Context(), "synthetic-token", "A2=synthetic-cookie", ProxyConfig{})
	if err != nil {
		t.Fatal(err)
	}
	e, _ := a.Get(id)
	check := func(want string) {
		t.Helper()
		list, err := a.List()
		if err != nil || len(list) != 1 || list[0].AvatarURL != want || stateOf(t, e.Store).AvatarURL != want {
			t.Fatalf("avatar not persisted/exposed: %+v, %v", list, err)
		}
	}
	check(testAvatarURL)
	updated := "https://cdn.v2ex.com/avatar/0000/0000/7_large.png?m=2"
	for i, card := range []string{avatarCard("tester", updated), ""} {
		e.Web.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://www.v2ex.com/" {
				t.Fatal("unexpected extra request", r.URL)
			}
			return response(200, webPage("tester", 0)+card), nil
		})
		cfg, err := e.Store.Config()
		if err != nil {
			t.Fatal(err)
		}
		if err := e.stepHybridLocked(t.Context(), cfg, stateOf(t, e.Store), time.Now().Add(time.Duration(i+1)*4*time.Minute)); err != nil {
			t.Fatal(err)
		}
		check(updated)
	}
}

func TestWebUnreadOnlyAcceptsAuthenticatedSameAccountAndCount(t *testing.T) {
	for _, source := range []string{webPage("tester", 3), strings.ReplaceAll(webPage("tester", 3), `href="/`, `href="https://www.v2ex.com/`), strings.ReplaceAll(webPage("tester", 3), "未读提醒", "unread")} {
		if count, err := parseWebUnread(source, "tester"); err != nil || count.Count != 3 {
			t.Fatal(count, err)
		}
	}
	for _, source := range []string{
		webPage("other", 3), webPage("TESTER", 3), `<a href="/member/tester">作者</a><a href="/notifications">3 unread</a>`,
		strings.ReplaceAll(webPage("tester", 3), "3 未读提醒", "没有有效计数"),
		strings.ReplaceAll(webPage("tester", 3), `href="/member/tester"`, `href="https://evil.example/member/tester"`),
		webPage("tester", 3) + `<a href="/notifications">4 unread</a>`,
		webPage("tester", 3) + `<a class="top" href="/member/other">other</a>`,
	} {
		if _, err := parseWebUnread(source, "tester"); err == nil {
			t.Fatal("invalid page accepted")
		}
	}
	if count, err := parseWebUnread(webPage("tester", 0), "tester"); err != nil || count.Count != 0 {
		t.Fatal("zero should be valid")
	}
}

func TestCookieIsOnlySentToHomepageAndNeverFollowed(t *testing.T) {
	e := NewEngine(testStore(t))
	calls := 0
	// Retain the production no-redirect policy while injecting the network only.
	e.Web.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://www.v2ex.com/" || r.Method != "GET" || r.Header.Get("Cookie") != "A2=one-time-secret" || r.Header.Get("Authorization") != "" {
			t.Fatal("cookie crossed its allowed request")
		}
		res := response(302, "")
		res.Header.Set("Location", "https://other.example/")
		return res, nil
	})
	if _, err := e.readWebUnread(context.Background(), "A2=one-time-secret", "tester"); err == nil || calls != 1 {
		t.Fatal("redirect followed or accepted")
	}
	for _, cookie := range []string{"", "other=value", "A2=value\r\nX-Test: injected"} {
		if _, err := e.readWebUnread(context.Background(), cookie, "tester"); err == nil {
			t.Fatal("invalid cookie accepted")
		}
	}
	if calls != 1 {
		t.Fatal("invalid cookie sent")
	}
}

func TestFirstPairingEncryptsCookieAndDefersSummaryUntilAPICheck(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	st := State{AccountID: 7, Username: "tester", Verified: true}
	if err = s.SaveConfig(Config{Enabled: true, APIToken: "private-pat", IntervalSeconds: 180}, st, false); err != nil {
		t.Fatal(err)
	}
	e := NewEngine(s)
	cookieReads := 0
	e.Web.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		cookieReads++
		return response(200, webPage("tester", 3)), nil
	})
	binding := "binding-one"
	e.Relay.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		bytes, _ := io.ReadAll(r.Body)
		raw := string(bytes)
		if strings.Contains(raw, "one-time-secret") || r.Header.Get("Cookie") != "" || strings.Contains(raw, "private-pat") {
			t.Fatal("V2EX credentials sent to relay")
		}
		return response(200, `{"paired":true,"binding_id":"`+binding+`","username":"tester"}`), nil
	})
	code := "V2E-" + strings.Repeat("a", 24)
	if err = e.Pair(context.Background(), PushServiceURL, code, "A2=one-time-secret"); err != nil {
		t.Fatal(err)
	}
	state := stateOf(t, s)
	if !state.InitialPairingDone || state.InitialUnreadCount != 3 {
		t.Fatal("initial state missing")
	}
	if queryInt(t, s, "SELECT COUNT(*) FROM outbox") != 0 {
		t.Fatal("pairing must wait for the first API ID")
	}
	cfg, err := s.Config()
	if err != nil || cfg.Cookie != "A2=one-time-secret" {
		t.Fatal("encrypted cookie did not round trip", err)
	}
	e.API.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Cookie") != "" {
			t.Fatal("cookie leaked to API")
		}
		return pageResponse([]int64{30, 29, 28}, 3, 3), nil
	})
	if err = e.Step(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err = s.DB.QueryRow("SELECT payload FROM outbox").Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var event Event
	if err = json.Unmarshal([]byte(payload), &event); err != nil || event.Type != "unread_summary" || event.UnreadCount != 3 || event.NotificationID != 30 {
		t.Fatal(event, err)
	}

	if err = e.Pair(context.Background(), PushServiceURL, code, ""); err != nil {
		t.Fatal(err)
	}
	if cookieReads != 2 || queryInt(t, s, "SELECT COUNT(*) FROM outbox") != 1 {
		t.Fatal("pair retry repeated bootstrap")
	}
	s.DB.Close()
	restored, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.DB.Close()
	e2 := NewEngine(restored)
	e2.Relay = e.Relay
	e2.Web.Transport = transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("re-pair asked for cookie"); return nil, nil })
	binding = "binding-two"
	if err = e2.Pair(context.Background(), PushServiceURL, "V2E-"+strings.Repeat("b", 24), ""); err != nil {
		t.Fatal(err)
	}
	if queryInt(t, restored, "SELECT COUNT(*) FROM outbox") != 1 || queryInt(t, restored, "SELECT COUNT(*) FROM outbox WHERE status='pending'") != 0 {
		t.Fatal("re-pair repeated initial summary or retained old delivery")
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		raw, _ := os.ReadFile(filepath.Join(dir, entry.Name()))
		if strings.Contains(string(raw), "one-time-secret") {
			t.Fatal("plaintext cookie persisted")
		}
	}
}

func TestFirstPairingZeroUnreadStillCompletesInitialization(t *testing.T) {
	e := NewEngine(testStore(t))
	st := State{AccountID: 7, Username: "tester", Verified: true}
	_ = e.Store.SaveConfig(Config{Enabled: true, IntervalSeconds: 180}, st, false)
	e.Web.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return response(200, webPage("tester", 0)), nil })
	e.Relay.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `{"paired":true,"binding_id":"zero-binding","username":"tester"}`), nil
	})
	if err := e.Pair(context.Background(), PushServiceURL, "V2E-"+strings.Repeat("a", 24), "A2=test"); err != nil {
		t.Fatal(err)
	}
	if !stateOf(t, e.Store).InitialPairingDone || queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox") != 0 {
		t.Fatal("zero initialization failed")
	}
}

func TestCookieMismatchDoesNotChangeConnectionOrAttemptPairing(t *testing.T) {
	e := NewEngine(testStore(t))
	st := State{AccountID: 7, Username: "tester", Verified: true}
	_ = e.Store.SaveConfig(Config{Enabled: true, IntervalSeconds: 180}, st, false)
	e.Web.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return response(200, webPage("other", 3)), nil })
	e.Relay.Transport = transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("mismatch reached relay"); return nil, nil })
	if err := e.Pair(context.Background(), PushServiceURL, "V2E-"+strings.Repeat("a", 24), "A2=wrong-owner"); err == nil {
		t.Fatal("cookie mismatch accepted")
	}
	if stateOf(t, e.Store).InitialPairingDone || queryInt(t, e.Store, "SELECT COUNT(*) FROM pending_pairing") != 0 {
		t.Fatal("mismatch changed binding state")
	}
}

func TestInitialSummaryAndPairingCommitAtomically(t *testing.T) {
	e := NewEngine(testStore(t))
	original := State{AccountID: 7, Username: "tester", Verified: true}
	cfg := Config{Enabled: true, IntervalSeconds: 180, RelayToken: "old"}
	if err := e.Store.SaveConfig(cfg, original, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Store.DB.Exec("CREATE TRIGGER fail_summary BEFORE INSERT ON outbox BEGIN SELECT RAISE(ABORT, 'synthetic storage failure'); END"); err != nil {
		t.Fatal(err)
	}
	changed := original
	changed.InitialPairingDone = true
	cfg.RelayToken = "new"
	event := Event{EventID: "summary", Type: "initial_unread", UnreadCount: 3}
	if err := e.Store.saveConfig(cfg, changed, true, true, &event); err == nil {
		t.Fatal("expected transaction failure")
	}
	saved, _ := e.Store.Config()
	if saved.RelayToken != "old" || stateOf(t, e.Store).InitialPairingDone {
		t.Fatal("partial pairing commit lost the initial summary")
	}
}
