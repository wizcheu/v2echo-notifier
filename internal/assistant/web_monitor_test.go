package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"testing"
	"time"
)

func hybridFixture(t *testing.T) (*Engine, *int, *int64, *int) {
	t.Helper()
	s := testStore(t)
	if err := s.SaveConfig(Config{Enabled: true, APIToken: "fake-pat", Cookie: "A2=fake-cookie", IntervalSeconds: 180, RelayURL: PushServiceURL, RelayToken: "fake-relay"}, State{Verified: true, AccountID: 7, Username: "tester"}, false); err != nil {
		t.Fatal(err)
	}
	e := NewEngine(s)
	count, first, calls := 3, int64(30), 0
	e.Web.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/" || r.Header.Get("Cookie") != "A2=fake-cookie" || r.Header.Get("Authorization") != "" {
			t.Fatal("invalid homepage request")
		}
		return response(200, webPage("tester", count)), nil
	})
	e.API.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Get("p") != "1" || r.Header.Get("Cookie") != "" {
			t.Fatal("invalid API request")
		}
		return pageResponse([]int64{first, first - 1, first - 2}, 100, 3), nil
	})
	return e, &count, &first, &calls
}

func hybridStep(t *testing.T, e *Engine) {
	t.Helper()
	st := stateOf(t, e.Store)
	now := maxTime(time.Now(), maxTime(st.NextWeb, maxTime(st.NextCheck, st.NextAPI)).Add(time.Second))
	if err := e.Step(context.Background(), now); err != nil {
		t.Fatal(err)
	}
}

func TestHybridFirstIDDedupCountAndRestart(t *testing.T) {
	e, count, first, calls := hybridFixture(t)
	for _, round := range []struct {
		count       int
		first       int64
		events, api int
	}{
		{3, 30, 1, 1}, {3, 30, 1, 2}, {4, 30, 1, 3}, // Count changes alone never trigger.
		{4, 31, 2, 4}, {4, 29, 3, 5}, // Deleting the first ID also triggers.
		{0, 32, 3, 5}, {4, 29, 3, 6}, // Zero skips API and preserves the baseline.
		{4, 31, 4, 7}, // Returning to a previously seen ID is a new transition.
	} {
		*count, *first = round.count, round.first
		hybridStep(t, e)
		if got := queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox"); got != round.events || *calls != round.api {
			t.Fatalf("round %+v: events %d API %d", round, got, *calls)
		}
		st := stateOf(t, e.Store)
		if !st.HasWebUnread || st.WebUnreadCount != round.count {
			t.Fatal("missing webpage snapshot")
		}
		// A new engine must recover the comparison baseline from SQLite.
		next := NewEngine(e.Store)
		next.Web = e.Web
		next.API = e.API
		e = next
	}
	var raw string
	if err := e.Store.DB.QueryRow("SELECT payload FROM outbox ORDER BY rowid DESC LIMIT 1").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var event Event
	if err := json.Unmarshal([]byte(raw), &event); err != nil || event.Type != "unread_summary" || event.UnreadCount != 4 || event.NotificationID != 31 {
		t.Fatal(event, err)
	}
	if queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox WHERE status='pending'") != 1 {
		t.Fatal("stale unsent summaries were not superseded")
	}
}

func TestHybridFailureAndAtomicBaseline(t *testing.T) {
	for _, kind := range []string{"homepage", "api", "empty", "identity", "transaction"} {
		t.Run(kind, func(t *testing.T) {
			e, _, _, _ := hybridFixture(t)
			web, api := e.Web.Transport, e.API.Client.Transport
			switch kind {
			case "homepage":
				e.Web.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return response(200, "login page"), nil })
			case "api":
				e.API.Client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("synthetic failure") })
			case "empty":
				e.API.Client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return pageResponse(nil, 0, 0), nil })
			case "identity":
				e.Web.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return response(200, webPage("other", 3)), nil })
			case "transaction":
				if _, err := e.Store.DB.Exec("CREATE TRIGGER fail_summary BEFORE INSERT ON outbox BEGIN SELECT RAISE(ABORT,'synthetic failure'); END"); err != nil {
					t.Fatal(err)
				}
			}
			err := e.Step(context.Background(), time.Now())
			if kind == "transaction" && err == nil {
				t.Fatal("transaction should fail")
			}
			if stateOf(t, e.Store).HasPushAPIID || queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox") != 0 {
				t.Fatal("failure consumed push baseline")
			}
			if kind == "transaction" {
				if _, err := e.Store.DB.Exec("DROP TRIGGER fail_summary"); err != nil {
					t.Fatal(err)
				}
			}
			e.Web.Transport, e.API.Client.Transport = web, api
			hybridStep(t, e)
			if !stateOf(t, e.Store).HasPushAPIID || queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox") != 1 {
				t.Fatal("recovery lost summary")
			}
		})
	}
}

func TestHybridManualCheckCannotBypassWebRetryAfter(t *testing.T) {
	e, _, _, calls := hybridFixture(t)
	reads := 0
	e.Web.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		reads++
		r := response(429, "")
		r.Header.Set("Retry-After", "3600")
		return r, nil
	})
	now := time.Now()
	if err := e.Step(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := e.RequestCheck(); err != nil {
		t.Fatal(err)
	}
	if err := e.Step(context.Background(), now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if reads != 1 || *calls != 0 {
		t.Fatal("manual check bypassed webpage cooldown")
	}
}

func TestHybridSharedQuotaDoesNotBlockHomepageBehindLegacyAccount(t *testing.T) {
	a, _ := testAccounts(t)
	firstID, _ := addTestAccount(t, a, "first-pat", 7, "tester")
	secondID, _ := addTestAccount(t, a, "second-pat", 8, "second")
	ids := []string{firstID, secondID}
	sort.Strings(ids)
	hybrid, _ := a.Get(ids[1])
	if err := hybrid.Store.SaveConfig(Config{Enabled: true, APIToken: "fake-pat", Cookie: "A2=fake-cookie", IntervalSeconds: 180}, State{Verified: true, AccountID: 7, Username: "tester"}, false); err != nil {
		t.Fatal(err)
	}
	count, reads := 0, 0
	hybrid.Web.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		reads++
		return response(200, webPage("tester", count)), nil
	})
	hybrid.API.Client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("API bypassed shared quota"); return nil, nil })
	now := time.Now()
	headers := make(http.Header)
	headers.Set("Retry-After", "3600")
	if err := a.Budget.Observe(headers, &APIError{Code: 429}, now); err != nil {
		t.Fatal(err)
	}
	cursor := 0
	if err := a.collect(context.Background(), now, &cursor); err != nil {
		t.Fatal(err)
	}
	if reads != 1 || cursor != 0 || !stateOf(t, hybrid.Store).HasWebUnread {
		t.Fatal("legacy account blocked homepage check")
	}
	count = 4
	if err := a.collect(context.Background(), now.Add(4*time.Minute), &cursor); err != nil {
		t.Fatal(err)
	}
	if reads != 2 || stateOf(t, hybrid.Store).WebUnreadCount != 4 {
		t.Fatal("webpage snapshot not updated during API cooldown")
	}
}

func TestHybridSummaryWireAndSubmittedReceiptSurviveZeroUnread(t *testing.T) {
	e, count, _, _ := hybridFixture(t)
	hybridStep(t, e)
	calls := 0
	e.Relay.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		var batch struct {
			Events   []map[string]any `json:"events"`
			Receipts []string         `json:"receipts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Fatal(err)
		}
		var id string
		if calls == 1 {
			if len(batch.Events) != 1 || len(batch.Events[0]) != 6 || batch.Events[0]["unread_count"] != float64(3) {
				t.Fatal("unexpected summary wire", batch)
			}
			id = batch.Events[0]["event_id"].(string)
		} else {
			if len(batch.Events) != 0 || len(batch.Receipts) != 1 {
				t.Fatal("submitted summary must only query receipt")
			}
			id = batch.Receipts[0]
		}
		if r.Header.Get("Cookie") != "" {
			t.Fatal("cookie leaked")
		}
		raw, _ := json.Marshal(syncResponse{RetryAfterSeconds: 180, Receipts: []Receipt{{EventID: id, Status: "pending", RetryAfterSeconds: 180}}})
		return response(200, string(raw)), nil
	})
	now := time.Now()
	if err := e.Deliver(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	*count = 0
	hybridStep(t, e)
	if queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox WHERE status='pending' AND submitted=1") != 1 {
		t.Fatal("zero discarded pending receipt")
	}
	if err := e.Deliver(context.Background(), now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("missing receipt query")
	}
}

func TestHybridEnablingCookieRetiresOnlyUnattemptedLegacyEvents(t *testing.T) {
	s := testStore(t)
	seed(t, s, State{Verified: true, AccountID: 7, Username: "tester"})
	if err := s.ImportPage(stateOf(t, s), []Notification{notification(30), notification(29)}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE outbox SET attempts=1 WHERE rowid=(SELECT MAX(rowid) FROM outbox)"); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Config()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Cookie = "A2=synthetic"
	if err := NewEngine(s).Configure(cfg); err != nil {
		t.Fatal(err)
	}
	if queryInt(t, s, "SELECT COUNT(*) FROM outbox WHERE status='skipped' AND attempts=0") != 1 || queryInt(t, s, "SELECT COUNT(*) FROM outbox WHERE status='pending' AND attempts=1") != 1 {
		t.Fatal("migration changed an uncertain send or retained an unattempted legacy event")
	}
}
