package assistant

import (
	"context"
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

func assertNoAccountSaved(t *testing.T, a *Accounts) {
	t.Helper()
	list, err := a.List()
	if err != nil || len(list) != 0 {
		t.Fatal("failed verification created an account", err)
	}
	var count int
	if err := a.DB.QueryRow("SELECT COUNT(*) FROM managed_accounts").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed verification persisted registry entry", err)
	}
	entries, err := os.ReadDir(filepath.Join(a.dir, "accounts"))
	if (err != nil && !os.IsNotExist(err)) || len(entries) != 0 {
		t.Fatal("failed verification created credential storage", err)
	}
}

func TestOnboardingRequiresExactIdentityBeforePersisting(t *testing.T) {
	for _, scenario := range []string{"same", "different", "case", "expired-token", "missing-username", "guest", "cancelled", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			a, _ := testAccounts(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			apiCalls, webCalls := 0, 0
			a.verificationTransport = func(proxy ProxyConfig) http.RoundTripper {
				if proxy.Mode != "custom" || proxy.URL != "http://proxy-user:proxy-secret@proxy.example:8080" {
					t.Fatal("verification did not use draft proxy")
				}
				return transportFunc(func(r *http.Request) (*http.Response, error) {
					// Neither account registry nor credential files may exist while verifying.
					assertNoAccountSaved(t, a)
					if r.URL.Path == "/api/v2/member" {
						apiCalls++
						if r.URL.String() != "https://www.v2ex.com/api/v2/member" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "Bearer synthetic-pat" {
							t.Fatal("incorrect member request")
						}
						switch scenario {
						case "expired-token":
							return response(401, `{"success":false,"message":"Token expired"}`), nil
						case "missing-username":
							return response(200, `{"success":true,"result":{"id":7}}`), nil
						}
						return response(200, `{"success":true,"result":{"id":7,"username":"tester"}}`), nil
					}
					webCalls++
					if r.URL.String() != "https://www.v2ex.com/" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "A2=synthetic-cookie" {
						t.Fatal("incorrect homepage request")
					}
					switch scenario {
					case "different":
						return response(200, webPage("other", 3)), nil
					case "case":
						return response(200, webPage("Tester", 3)), nil
					case "guest":
						return response(200, `<a class="top" href="/signin">Sign In</a>`), nil
					case "redirect":
						res := response(302, "")
						res.Header.Set("Location", "https://other.example/")
						return res, nil
					case "cancelled":
						cancel()
					}
					return response(200, webPage("tester", 3)), nil
				})
			}
			id, err := a.Add(ctx, "synthetic-pat", "Cookie: A2=synthetic-cookie", ProxyConfig{Mode: "custom", URL: "http://proxy-user:proxy-secret@proxy.example:8080/"})
			if apiCalls != 1 || webCalls > 1 {
				t.Fatal("unexpected validation requests", apiCalls, webCalls)
			}
			if scenario != "same" {
				if err == nil || id != "" {
					t.Fatal("invalid credentials accepted")
				}
				if (scenario == "expired-token" || scenario == "missing-username") && webCalls != 0 {
					t.Fatal("Cookie used before API identity was known")
				}
				assertNoAccountSaved(t, a)
				return
			}
			if err != nil || webCalls != 1 {
				t.Fatal("matching credentials rejected", err)
			}
			e, _ := a.Get(id)
			cfg, err := e.Store.Config()
			if err != nil || cfg.ProxyMode != "custom" || cfg.ProxyURL != "http://proxy-user:proxy-secret@proxy.example:8080" || cfg.APIToken != "synthetic-pat" || cfg.Cookie != "A2=synthetic-cookie" {
				t.Fatal("verified configuration was not saved", err)
			}
			st := stateOf(t, e.Store)
			if !st.Verified || st.AccountID != 7 || st.Username != "tester" || st.TokenCheckedAt.IsZero() || st.CookieCheckedAt.IsZero() || !st.HasWebUnread || st.WebUnreadCount != 3 || st.HasPushAPIID {
				t.Fatal("verified identity state not saved correctly")
			}
			if queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox") != 0 {
				t.Fatal("onboarding generated a push")
			}
			var stored string
			if err := e.Store.DB.QueryRow("SELECT body FROM settings WHERE id=1").Scan(&stored); err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"synthetic-pat", "synthetic-cookie", "proxy-secret", "proxy.example"} {
				if strings.Contains(stored, secret) {
					t.Fatal("onboarding persisted plaintext credentials")
				}
			}
			mockAccountVerification(t, a, 7, "tester")
			if _, err := a.Add(context.Background(), "replacement-pat", "A2=other", ProxyConfig{}); err == nil {
				t.Fatal("duplicate verified member accepted")
			}
			list, err := a.List()
			if err != nil || len(list) != 1 || list[0].ID != id {
				t.Fatal("duplicate validation modified saved account", err)
			}
		})
	}
}

func TestOnboardingProxyFailureNeverFallsBackOrSaves(t *testing.T) {
	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme, func(t *testing.T) {
			a, _ := testAccounts(t)
			calls := 0
			a.verificationTransport = func(proxy ProxyConfig) http.RoundTripper {
				base := http.DefaultTransport.(*http.Transport).Clone()
				base.DialContext = func(_ context.Context, _, address string) (net.Conn, error) {
					calls++
					if address != "draft.example:8443" {
						t.Error("draft proxy bypassed", address)
					}
					return nil, errors.New("synthetic proxy-secret failure")
				}
				return proxyTransport(base, proxy.Mode, proxy.URL)
			}
			_, err := a.Add(context.Background(), "synthetic-pat", "A2=synthetic-cookie", ProxyConfig{Mode: "custom", URL: scheme + "://user:proxy-secret@draft.example:8443"})
			if err == nil || strings.Contains(err.Error(), "proxy-secret") || calls != 1 {
				t.Fatal("proxy failure not handled safely", err, calls)
			}
			assertNoAccountSaved(t, a)
		})
	}
}

func TestOnboardingHonorsSharedBudgetAndValidatesProxyFirst(t *testing.T) {
	a, _ := testAccounts(t)
	a.verificationTransport = func(ProxyConfig) http.RoundTripper { t.Fatal("unexpected outbound request"); return nil }
	if _, err := a.Add(context.Background(), "synthetic", "A2=synthetic", ProxyConfig{Mode: "custom"}); err == nil {
		t.Fatal("empty custom proxy accepted")
	}
	budget, err := a.Budget.Snapshot()
	if err != nil || !budget.Next.IsZero() {
		t.Fatal("invalid proxy consumed API budget", err)
	}
	if allowed, err := a.Budget.Reserve(time.Now()); err != nil || !allowed {
		t.Fatal(err)
	}
	if _, err := a.Add(context.Background(), "synthetic", "A2=synthetic", ProxyConfig{}); err == nil || !strings.Contains(err.Error(), "冷却") {
		t.Fatal("onboarding bypassed shared cooldown", err)
	}
	assertNoAccountSaved(t, a)
}

func TestAccountHTTPRejectsMismatchAndSavesDraftProxyAfterVerification(t *testing.T) {
	a, dir := testAccounts(t)
	s, err := NewServer(a, fstest.MapFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	s.sessions["synthetic-session"] = time.Now().Add(time.Hour)
	handler := s.Handler()
	for _, username := range []string{"Tester", "tester"} {
		mockAccountVerification(t, a, 7, username)
		factory := a.verificationTransport
		a.verificationTransport = func(proxy ProxyConfig) http.RoundTripper {
			if proxy.Mode != "custom" || proxy.URL != "http://proxy.example:8080" {
				t.Fatal("HTTP route lost draft proxy")
			}
			original := factory(proxy)
			return transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/" {
					return response(200, webPage("tester", 3)), nil
				}
				return original.RoundTrip(r)
			})
		}
		req := httptest.NewRequest("POST", "http://localhost/api/accounts", strings.NewReader(`{"api_token":"synthetic","cookie":"A2=synthetic","proxy_mode":"custom","proxy_url":"http://proxy.example:8080"}`))
		req.Header.Set("X-V2Echo-Request", "1")
		req.AddCookie(&http.Cookie{Name: "notifier_session", Value: "synthetic-session"})
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if username == "Tester" {
			if res.Code != 400 || !strings.Contains(res.Body.String(), "用户名不完全一致") {
				t.Fatal("HTTP accepted mismatched identity", res.Code, res.Body.String())
			}
			assertNoAccountSaved(t, a)
		} else if res.Code != 201 {
			t.Fatal("HTTP rejected matching credentials", res.Code, res.Body.String())
		}
	}
}
