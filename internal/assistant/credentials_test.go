package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAddRequiresBothCredentialsAndEncryptsCookie(t *testing.T) {
	a, _ := testAccounts(t)
	for _, pair := range [][2]string{{"", "A2=test"}, {"token", ""}, {"token", "other=value"}, {"token", "A2=value\r\nInjected: value"}} {
		if _, err := a.Add(context.Background(), pair[0], pair[1], ProxyConfig{}); err == nil {
			t.Fatal("incomplete credentials accepted")
		}
	}
	list, err := a.List()
	if err != nil || len(list) != 0 {
		t.Fatal("invalid input created an account")
	}
	mockAccountVerification(t, a, 7, "tester")
	id, err := a.Add(context.Background(), "synthetic-pat", "Cookie: A2=synthetic-cookie", ProxyConfig{})
	if err != nil {
		t.Fatal(err)
	}
	e, _ := a.Get(id)
	cfg, err := e.Store.Config()
	if err != nil || cfg.Cookie != "A2=synthetic-cookie" || cfg.APIToken != "synthetic-pat" {
		t.Fatal("credentials not saved")
	}
	var stored string
	if err := e.Store.DB.QueryRow("SELECT body FROM settings WHERE id=1").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "synthetic-cookie") || strings.Contains(stored, "synthetic-pat") {
		t.Fatal("credentials stored in plaintext")
	}
	st := stateOf(t, e.Store)
	st.CookieIssue = "Cookie 登录已失效"
	st.TokenIssue = "Token 已过期"
	if err := e.Store.SaveState(st); err != nil {
		t.Fatal(err)
	}
	list, err = a.List()
	if err != nil || len(list) != 1 || !list[0].CookieConfigured || list[0].CookieIssue == "" || list[0].TokenIssue == "" || !list[0].Blocked {
		t.Fatal("account management lost credential warnings")
	}
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), cfg.Cookie) || strings.Contains(string(raw), cfg.APIToken) {
		t.Fatal("list returned credentials")
	}
	mockAccountVerification(t, a, 8, "other")
	otherID, err := a.Add(context.Background(), "other-pat", "A2=other-cookie", ProxyConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Remove(id); err != nil {
		t.Fatal(err)
	}
	removed, err := e.Store.Config()
	if err != nil || removed.Cookie != "" || removed.APIToken != "" {
		t.Fatal("removed account retained credentials")
	}
	other, ok := a.Get(otherID)
	if !ok {
		t.Fatal("removal lost another account")
	}
	remaining, err := other.Store.Config()
	if err != nil || remaining.Cookie != "A2=other-cookie" {
		t.Fatal("removal changed another account's credentials")
	}
}

func TestTokenExpiryDiscoveredWithoutUnreadAndPersistsUntilReplacement(t *testing.T) {
	e, count, _, _ := hybridFixture(t)
	*count = 0
	hybridStep(t, e)
	now := stateOf(t, e.Store).NextTokenCheck.Add(time.Second)
	calls := 0
	e.API.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if !strings.HasSuffix(r.URL.Path, "/token") || r.Header.Get("Cookie") != "" {
			t.Fatal("unexpected credential probe")
		}
		return response(200, `{"success":false,"message":"Token expired"}`), nil
	})
	if err := e.Step(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	st := stateOf(t, e.Store)
	if calls != 1 || st.TokenIssue == "" || st.Verified || !st.AuthBlocked || st.HasPushAPIID {
		t.Fatal("quiet account did not detect expired Token")
	}
	restarted := NewEngine(e.Store)
	restarted.Web = e.Web
	if err := restarted.Step(context.Background(), now.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if stateOf(t, e.Store).TokenIssue == "" {
		t.Fatal("restart cleared credential warning")
	}
	st.CookieIssue = "Cookie also invalid"
	_ = e.Store.SaveState(st)
	cfg, _ := e.Store.Config()
	cfg.APIToken = "replacement-pat"
	if err := e.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	st = stateOf(t, e.Store)
	if st.TokenIssue != "" || st.AuthBlocked || st.CookieIssue == "" || st.Verified {
		t.Fatal("replacement reset unrelated credential state")
	}
	e.API.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/member") {
			t.Fatal("replacement skipped identity verification")
		}
		return response(200, `{"success":true,"result":{"id":7,"username":"tester"}}`), nil
	})
	if err := e.Step(context.Background(), maxTime(now, st.NextAPI).Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if !stateOf(t, e.Store).Verified {
		t.Fatal("replacement did not recover identity")
	}
}

func TestBothExpiredCredentialsRemainVisibleWithoutAPIRequests(t *testing.T) {
	e, _, _, _ := hybridFixture(t)
	st := stateOf(t, e.Store)
	st.TokenIssue = "Token 已过期"
	st.Verified, st.AuthBlocked = false, true
	if err := e.Store.SaveState(st); err != nil {
		t.Fatal(err)
	}
	e.API.Client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("expired Token was retried"); return nil, nil })
	e.Web.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `<a class="top" href="/signin">登录</a>`), nil
	})
	if err := e.Step(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	st = stateOf(t, e.Store)
	if st.TokenIssue == "" || st.CookieIssue == "" {
		t.Fatal("one expired credential hid the other")
	}
	if queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox") != 0 {
		t.Fatal("invalid credentials created a push")
	}
}

func TestCookieExpiryAndChallengesAreDistinctAndReplacementRecovers(t *testing.T) {
	for _, kind := range []string{"guest", "redirect", "mismatch", "challenge", "unrecognized"} {
		t.Run(kind, func(t *testing.T) {
			e, _, _, apiCalls := hybridFixture(t)
			e.Web.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
				switch kind {
				case "guest":
					return response(200, `<a class="top" href="/signin">Sign In</a>`), nil
				case "redirect":
					r := response(302, "")
					r.Header.Set("Location", "https://www.v2ex.com/signin")
					return r, nil
				case "mismatch":
					return response(200, webPage("other", 3)), nil
				case "challenge":
					r := response(403, "challenge")
					r.Header.Set("cf-mitigated", "challenge")
					return r, nil
				default:
					return response(200, "unrecognized page"), nil
				}
			})
			hybridStep(t, e)
			st := stateOf(t, e.Store)
			invalid := kind == "guest" || kind == "redirect" || kind == "mismatch"
			if (st.CookieIssue != "") != invalid || st.TokenIssue != "" || *apiCalls != 0 || st.HasWebUnread {
				t.Fatal("incorrect credential classification")
			}
			if invalid {
				// A new engine must preserve the pause without making a request.
				if err := NewEngine(e.Store).Step(context.Background(), time.Now().Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				if stateOf(t, e.Store).CookieIssue == "" {
					t.Fatal("restart cleared Cookie warning")
				}
			}
			cfg, _ := e.Store.Config()
			cfg.Cookie = "A2=refreshed"
			if err := e.Configure(cfg); err != nil {
				t.Fatal(err)
			}
			e.Web.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return response(200, webPage("tester", 3)), nil })
			hybridStep(t, e)
			st = stateOf(t, e.Store)
			if st.CookieIssue != "" || st.CookieCheckedAt.IsZero() || !st.HasPushAPIID {
				t.Fatal("updated Cookie did not recover polling")
			}
		})
	}
}

func TestTokenHealthCheckHonorsQuotaAndDoesNotClearCookieWarning(t *testing.T) {
	e, _, _, _ := hybridFixture(t)
	now := time.Now()
	st := stateOf(t, e.Store)
	st.CookieIssue = "Cookie 登录已失效"
	st.NextTokenCheck = now.Add(-time.Second)
	st.NextAPI = now.Add(time.Hour)
	if err := e.Store.SaveState(st); err != nil {
		t.Fatal(err)
	}
	calls := 0
	e.API.Client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return response(200, `{"success":true,"result":{"scope":"everything"}}`), nil
	})
	if err := e.Step(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("probe bypassed API cooldown")
	}
	if err := e.Step(context.Background(), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || stateOf(t, e.Store).CookieIssue == "" {
		t.Fatal("probe lost independent Cookie warning")
	}
	if err := e.Step(context.Background(), now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("probe ran more than once per day")
	}
}

func TestTokenErrorClassificationAcrossHTTPStatuses(t *testing.T) {
	for _, tc := range []struct {
		status  int
		body    string
		expired bool
	}{
		{401, "unauthorized", true}, {403, `{"success":false,"message":"Token expired"}`, true},
		{200, `{"success":false,"message":"Token revoked"}`, true}, {403, "challenge", false},
	} {
		e := NewEngine(testStore(t))
		e.API.Client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return response(tc.status, tc.body), nil })
		var member Member
		_, err := e.API.get(context.Background(), "synthetic", "member", &member)
		problem, ok := err.(*APIError)
		if !ok || problem.Expired != tc.expired {
			t.Fatal("incorrect API credential classification")
		}
	}
}
