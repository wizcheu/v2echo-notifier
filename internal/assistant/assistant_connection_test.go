package assistant

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func addReuseAccount(t *testing.T, a *Accounts, pat string, memberID int64, username string) (string, *Engine) {
	t.Helper()
	id, e := addTestAccount(t, a, pat, memberID, username)
	cfg, _ := e.Store.Config()
	st, _ := e.Store.State()
	cfg.Cookie = "A2=synthetic-" + username
	st.Verified = true
	st.AccountID = memberID
	st.Username = username
	if err := e.Store.SaveConfig(cfg, st, false); err != nil {
		t.Fatal(err)
	}
	return id, e
}

func TestAssistantReusePublishesVerifiedAccountsAndPreservesBaselines(t *testing.T) {
	a, _ := testAccounts(t)
	idA, eA := addReuseAccount(t, a, "pat-a", 1, "alice")
	idB, eB := addReuseAccount(t, a, "pat-b", 2, "bob")
	_ = idA
	cfgA, _ := eA.Store.Config()
	stA, _ := eA.Store.State()
	cfgA.RelayToken = "existing-alice-sender"
	cfgA.RelayURL = PushServiceURL
	stA.InitialPairingDone = true
	if err := eA.Store.SaveConfig(cfgA, stA, false); err != nil {
		t.Fatal(err)
	}
	stB, _ := eB.Store.State()
	stB.HasPushAPIID = true
	stB.LastPushAPIID = 77
	if err := eB.Store.SaveState(stB); err != nil {
		t.Fatal(err)
	}
	calls := 0
	readyB := false
	tokens := map[int64]string{}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Cookie") != "" || !strings.HasPrefix(r.URL.String(), PushServiceURL+"/v1/assistants/") {
			t.Fatal("private credential leak or wrong endpoint")
		}
		raw, _ := io.ReadAll(r.Body)
		for _, secret := range []string{"pat-a", "pat-b", "A2="} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("source credentials leaked")
			}
		}
		var body struct {
			MemberID int64  `json:"source_account_id"`
			Sender   string `json:"sender_token"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if old := tokens[body.MemberID]; old != "" && old != body.Sender {
			t.Fatal("retry rotated sender credential")
		}
		tokens[body.MemberID] = body.Sender
		ready := body.MemberID == 1 || readyB
		encoded, _ := json.Marshal(map[string]any{"published": true, "ready": ready, "connection_id": "11111111-1111-4111-8111-111111111111"})
		return response(200, string(encoded)), nil
	})}
	eA.Relay = client
	eB.Relay = client
	eB.Web = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { return response(200, webPage("bob", 4)), nil })}
	now := time.Now()
	if err := a.SyncConnection(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("implicit opt-in")
	}
	if err := a.EnableConnection(); err != nil {
		t.Fatal(err)
	}
	if err := a.SyncConnection(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("published %d sources", calls)
	}
	cfgB, _ := eB.Store.Config()
	if cfgB.RelayToken != "" {
		t.Fatal("unsubscribed account started sending")
	}
	pending, err := a.loadConnection()
	if err != nil {
		t.Fatal(err)
	}
	if pending.Sources[idB].Token == "" {
		t.Fatal("sender not durable")
	}
	var sealed string
	if err = a.DB.QueryRow("SELECT body FROM assistant_connection WHERE id=1").Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, pending.Secret) || strings.Contains(sealed, tokens[2]) {
		t.Fatal("unencrypted capabilities")
	}
	if err = a.SyncConnection(t.Context(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("ignored control cadence")
	}
	readyB = true
	if err = a.SyncConnection(t.Context(), now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	cfgB, _ = eB.Store.Config()
	stB, _ = eB.Store.State()
	if cfgB.RelayToken != tokens[2] || !stB.InitialPairingDone || stB.InitialUnreadCount != 4 || stB.LastPushAPIID != 77 {
		t.Fatal("automatic subscription lost baseline or initial snapshot")
	}
}

func TestAssistantReuseDoesNotPublishUnverifiedOrBlockedAccounts(t *testing.T) {
	a, _ := testAccounts(t)
	_, e := addReuseAccount(t, a, "pat", 1, "alice")
	st, _ := e.Store.State()
	st.AuthBlocked = true
	if err := e.Store.SaveState(st); err != nil {
		t.Fatal(err)
	}
	e.Relay = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("blocked account was published")
		return nil, errors.New("unexpected")
	})}
	if err := a.EnableConnection(); err != nil {
		t.Fatal(err)
	}
	if err := a.SyncConnection(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestAssistantReusePersistsFailedPublicationForRetry(t *testing.T) {
	a, _ := testAccounts(t)
	id, e := addReuseAccount(t, a, "pat", 1, "alice")
	e.Relay = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("lost response") })}
	if err := a.EnableConnection(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if a.SyncConnection(t.Context(), now) == nil {
		t.Fatal("expected failure")
	}
	saved, err := a.loadConnection()
	if err != nil {
		t.Fatal(err)
	}
	token := saved.Sources[id].Token
	if token == "" || saved.LastError == "" {
		t.Fatal("lost durable pending credential or error")
	}
	e.Relay = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["sender_token"] != token {
			t.Fatal("lost-response retry changed credential")
		}
		return response(200, `{"published":true,"ready":false,"connection_id":"11111111-1111-4111-8111-111111111111"}`), nil
	})}
	if err := a.SyncConnection(t.Context(), now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	view, err := a.ConnectionView()
	if err != nil || view.LastError != "" {
		t.Fatal("successful retry did not clear error")
	}
}

func TestRemovedAssistantSourceKeepsDurableRevocationUntilConfirmed(t *testing.T) {
	a, _ := testAccounts(t)
	id, e := addReuseAccount(t, a, "pat", 1, "alice")
	e.Relay = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `{"published":true,"ready":false,"connection_id":"11111111-1111-4111-8111-111111111111"}`), nil
	})}
	if err := a.EnableConnection(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := a.SyncConnection(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if err := a.Remove(id); err != nil {
		t.Fatal(err)
	}
	fail := true
	a.connectionClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "DELETE" || !strings.HasSuffix(r.URL.Path, "/accounts/1") || r.Header.Get("Cookie") != "" {
			t.Fatal("bad removal request")
		}
		if fail {
			return response(503, `{}`), nil
		}
		return response(200, `{"revoked":true}`), nil
	})}
	if a.SyncConnection(t.Context(), now.Add(3*time.Minute)) == nil {
		t.Fatal("expected retryable deletion")
	}
	value, _ := a.loadConnection()
	if len(value.Sources) != 1 {
		t.Fatal("lost cleanup task")
	}
	fail = false
	if err := a.SyncConnection(t.Context(), now.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	value, _ = a.loadConnection()
	if len(value.Sources) != 0 {
		t.Fatal("cleanup task not removed")
	}
}
