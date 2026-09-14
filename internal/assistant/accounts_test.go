package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestSchedulerDoesNotStarveAccountsAtSharedCadence(t *testing.T) {
	a, _ := testAccounts(t)
	now := time.Unix(1789200000, 0)
	var engines []*Engine
	for i := 0; i < 10; i++ {
		_, e := addTestAccount(t, a, fmt.Sprintf("pat-%d", i), int64(i+1), fmt.Sprintf("user%d", i))
		engines = append(engines, e)
	}
	cursor := 0
	for second := 0; second < 100; second++ {
		if err := a.collect(context.Background(), now.Add(time.Duration(second)*time.Second), &cursor); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range engines {
		if !stateOf(t, e.Store).Verified {
			t.Fatal("scheduler starved an account despite available turns")
		}
	}
}

func testAccounts(t *testing.T) (*Accounts, string) {
	t.Helper()
	dir := t.TempDir()
	a, err := OpenAccounts(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a, dir
}

// Seed legacy profiles without running the current onboarding workflow.
func seedTestAccount(t *testing.T, a *Accounts, pat, cookie string) string {
	t.Helper()
	id := fmt.Sprintf("%032x", len(a.engines)+1)
	s, err := OpenStore(filepath.Join(a.dir, "accounts", id))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveConfig(Config{APIToken: pat, Cookie: cookie, Enabled: true, IntervalSeconds: 180}, State{Phase: "history", Page: 1}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec("INSERT INTO managed_accounts(id) VALUES(?)", id); err != nil {
		t.Fatal(err)
	}
	a.attach(id, NewEngine(s))
	return id
}

func mockAccountVerification(t *testing.T, a *Accounts, id int64, username string) {
	t.Helper()
	a.verificationTransport = func(ProxyConfig) http.RoundTripper {
		return transportFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/api/v2/member" {
				if r.URL.String() != "https://www.v2ex.com/api/v2/member" || r.Method != "GET" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") == "" {
					t.Fatal("onboarding used the wrong API endpoint or credentials")
				}
				raw, _ := json.Marshal(map[string]any{"success": true, "result": Member{ID: id, Username: username}})
				return response(200, string(raw)), nil
			}
			if r.URL.String() != "https://www.v2ex.com/" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") == "" {
				t.Fatal("onboarding leaked credentials or visited notifications")
			}
			return response(200, webPage(username, 3)), nil
		})
	}
	// Each setup represents a fresh available budget window.
	if _, err := a.DB.Exec("DELETE FROM api_budget"); err != nil {
		t.Fatal(err)
	}
}

func addTestAccount(t *testing.T, a *Accounts, pat string, memberID int64, username string) (string, *Engine) {
	t.Helper()
	id := seedTestAccount(t, a, pat, "A2=synthetic")
	e, _ := a.Get(id)
	cfg, _ := e.Store.Config()
	cfg.Cookie = "" // Model a pre-existing API-only account.
	if err := e.Store.SaveConfig(cfg, stateOf(t, e.Store), false); err != nil {
		t.Fatal(err)
	}
	e.API.Client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer "+pat {
			t.Fatal("request used another account's token")
		}
		raw, _ := json.Marshal(map[string]any{"success": true, "result": map[string]any{"id": memberID, "username": username}})
		return response(200, string(raw)), nil
	})}
	return id, e
}

func TestAccountsIsolateCredentialsHistoryQueuesAndRemoval(t *testing.T) {
	a, dir := testAccounts(t)
	now := time.Unix(1789200000, 0)
	idA, eA := addTestAccount(t, a, "secret-a", 7, "alpha")
	idB, eB := addTestAccount(t, a, "secret-b", 8, "beta")
	if err := eA.Step(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := eB.Step(context.Background(), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if stateOf(t, eB.Store).Verified {
		t.Fatal("second account bypassed the shared request gap")
	}
	if err := eB.Step(context.Background(), now.Add(11*time.Second)); err != nil {
		t.Fatal(err)
	}
	for i, e := range []*Engine{eA, eB} {
		st := stateOf(t, e.Store)
		if !st.Verified {
			t.Fatal("identity not verified")
		}
		n := notification(2)
		n.ForMemberID = st.AccountID
		n.Text = st.Username
		st.AnchorID = 1
		st.AnchorSet = true
		st.Phase = "live"
		st.Page = 1
		if err := e.Store.ImportPage(st, []Notification{n}, true); err != nil {
			t.Fatal(err)
		}
		cfg, _ := e.Store.Config()
		cfg.RelayURL = "https://relay.example"
		cfg.RelayToken = []string{"sender-a", "sender-b"}[i]
		if err := e.Configure(cfg); err != nil {
			t.Fatal(err)
		}
	}
	if queryInt(t, eA.Store, "SELECT COUNT(*) FROM notifications WHERE account_id=8") != 0 || queryInt(t, eB.Store, "SELECT COUNT(*) FROM notifications WHERE account_id=7") != 0 {
		t.Fatal("notification history crossed accounts")
	}
	aState := stateOf(t, eA.Store)
	// An updated PAT from another account must not reassign existing state.
	cfg, _ := eA.Store.Config()
	cfg.APIToken = "wrong-owner"
	if err := eA.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	eA.API.Client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return response(200, `{"success":true,"result":{"id":8,"username":"beta"}}`), nil
	})}
	if err := eA.Step(context.Background(), now.Add(100*time.Second)); err != nil {
		t.Fatal(err)
	}
	st := stateOf(t, eA.Store)
	if !st.AuthBlocked || st.AccountID != aState.AccountID || st.Username != "alpha" {
		t.Fatal("PAT reassigned the account")
	}
	bcfg, _ := eB.Store.Config()
	if bcfg.APIToken != "secret-b" || bcfg.RelayToken != "sender-b" {
		t.Fatal("configuration crossed accounts")
	}
	if err := a.Remove(idA); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Get(idA); ok {
		t.Fatal("removed account still accessible")
	}
	if queryInt(t, eA.Store, "SELECT COUNT(*) FROM settings") != 0 || queryInt(t, eA.Store, "SELECT COUNT(*) FROM notifications") != 0 {
		t.Fatal("removed account retained credentials/history")
	}
	if queryInt(t, eB.Store, "SELECT COUNT(*) FROM notifications") != 1 || queryInt(t, eB.Store, "SELECT COUNT(*) FROM outbox") != 1 {
		t.Fatal("removal affected another account")
	}
	if err := eA.Configure(cfg); err != ErrAccountRemoved {
		t.Fatal("stale request resurrected removed account")
	}
	a.Close()
	reopened, err := OpenAccounts(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, ok := reopened.Get(idA); ok {
		t.Fatal("removed account restored on restart")
	}
	restored, ok := reopened.Get(idB)
	if !ok {
		t.Fatal("other account lost on restart")
	}
	bcfg, _ = restored.Store.Config()
	if bcfg.APIToken != "secret-b" {
		t.Fatal("credential lost on restart")
	}
}

func TestDuplicateVerifiedIdentityCannotCreateSecondCollector(t *testing.T) {
	a, _ := testAccounts(t)
	now := time.Unix(1789200000, 0)
	_, first := addTestAccount(t, a, "pat-one", 7, "alpha")
	_, second := addTestAccount(t, a, "pat-two", 7, "alpha")
	if err := first.Step(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := second.Step(context.Background(), now.Add(11*time.Second)); err != nil {
		t.Fatal(err)
	}
	if !stateOf(t, first.Store).Verified {
		t.Fatal("first identity not verified")
	}
	st := stateOf(t, second.Store)
	if st.Verified || !st.AuthBlocked || st.AccountID != 0 {
		t.Fatal("duplicate collector was allowed")
	}
	if err := second.Pair(context.Background(), "https://relay.example", "V2E-abcdefghijklmnopqrstuvwx", ""); err == nil {
		t.Fatal("unverified duplicate paired")
	}
}

func TestSharedBudgetSurvivesRestartAndHonorsRateLimit(t *testing.T) {
	a, dir := testAccounts(t)
	now := time.Unix(1789200000, 0)
	allowed, err := a.Budget.Reserve(now)
	if err != nil || !allowed {
		t.Fatal("initial reservation failed", err)
	}
	if allowed, err = a.Budget.Reserve(now.Add(time.Second)); err != nil || allowed {
		t.Fatal("request gap not enforced")
	}
	headers := http.Header{"Retry-After": []string{"7200"}}
	if err := a.Budget.Observe(headers, &APIError{Code: 429}, now); err != nil {
		t.Fatal(err)
	}
	a.Close()
	reopened, err := OpenAccounts(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if allowed, err = reopened.Budget.Reserve(now.Add(time.Hour)); err != nil || allowed {
		t.Fatal("restart bypassed global Retry-After")
	}
	if allowed, err = reopened.Budget.Reserve(now.Add(7201 * time.Second)); err != nil || !allowed {
		t.Fatal("budget did not recover", err)
	}
}

func TestChangingOneRelayDoesNotRetireAnotherAccountsQueue(t *testing.T) {
	a, _ := testAccounts(t)
	_, one := addTestAccount(t, a, "one", 7, "alpha")
	_, two := addTestAccount(t, a, "two", 8, "beta")
	for _, e := range []*Engine{one, two} {
		cfg, _ := e.Store.Config()
		cfg.RelayURL = "https://relay.example"
		cfg.RelayToken = "old-token"
		if err := e.Configure(cfg); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Store.DB.Exec("INSERT INTO outbox(event_id,payload) VALUES('test','{}')"); err != nil {
			t.Fatal(err)
		}
	}
	cfg, _ := one.Store.Config()
	cfg.RelayToken = "new-token"
	if err := one.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	if queryInt(t, one.Store, "SELECT COUNT(*) FROM outbox WHERE status='rejected'") != 1 || queryInt(t, two.Store, "SELECT COUNT(*) FROM outbox WHERE status='pending'") != 1 {
		t.Fatal("relay replacement crossed account queues")
	}
}
