package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"
)

func hybridFixture(t *testing.T) (*Engine, *int, *int64, *int) {
	t.Helper()
	s := testStore(t)
	if err := s.SaveConfig(Config{PushSchedule: allDaySchedule(), Enabled: true, APIToken: "fake-pat", Cookie: "A2=fake-cookie", IntervalSeconds: 180, RelayURL: PushServiceURL, RelayToken: "fake-relay"}, State{Verified: true, AccountID: 7, Username: "tester"}, false); err != nil {
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
		return pageResponse([]int64{first, first - 1, first - 2}, 3, 3), nil
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

func TestHybridNotificationIDDedupCountAndRestart(t *testing.T) {
	e, count, first, calls := hybridFixture(t)
	for _, round := range []struct {
		count       int
		first       int64
		events, api int
	}{
		{3, 30, 3, 1}, {3, 30, 3, 2}, {3, 31, 4, 3}, {2, 30, 4, 4},
		{0, 32, 4, 4}, {2, 30, 4, 5}, {3, 31, 4, 6},
	} {
		*count, *first = round.count, round.first
		hybridStep(t, e)
		if got := queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox"); got != round.events || *calls != round.api {
			t.Fatalf("round %+v: events %d API %d", round, got, *calls)
		}
		st := stateOf(t, e.Store)
		if !st.HasWebUnread || st.WebUnreadCount != round.count {
			t.Fatal("missing website snapshot")
		}
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
	if err := json.Unmarshal([]byte(raw), &event); err != nil || event.Type != "notification" || event.DetailsVersion != 1 || event.NotificationID != 31 {
		t.Fatal(event, err)
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
			if kind == "identity" {
				cfg, _ := e.Store.Config()
				cfg.Cookie = "A2=replaced-cookie"
				if err := e.Configure(cfg); err != nil {
					t.Fatal(err)
				}
				e.Web.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return response(200, webPage("tester", 3)), nil })
			}
			hybridStep(t, e)
			if !stateOf(t, e.Store).HasPushAPIID || queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox") != 3 {
				t.Fatal("recovery lost individual notifications")
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
	if err := hybrid.Store.SaveConfig(Config{PushSchedule: allDaySchedule(), Enabled: true, APIToken: "fake-pat", Cookie: "A2=fake-cookie", IntervalSeconds: 180}, State{Verified: true, AccountID: 7, Username: "tester"}, false); err != nil {
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

func notificationCapabilitiesResponse() *http.Response {
	return response(200, `{"notification_body_encryption":2,"notification_routing":1,"notification_details":1,"notification_test_samples":1,"max_events":20,"max_receipts":50,"max_sync_bytes":65536}`)
}
func detailedTransport(next transportFunc) transportFunc {
	return func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/v1/recipient-keys") {
			return response(200, `{"source_account_id":7,"recipients":[]}`), nil
		}
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/v1/health") {
			return notificationCapabilitiesResponse(), nil
		}
		return next(r)
	}
}

func TestHybridDetailedWireAndSubmittedReceiptSurviveZeroUnread(t *testing.T) {
	e, count, _, _ := hybridFixture(t)
	hybridStep(t, e)
	calls := 0
	e.Relay.Transport = detailedTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var batch syncRequest
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Fatal(err)
		}
		receipts := []Receipt{}
		if calls == 1 {
			if len(batch.Events) != 3 {
				t.Fatal("expected three individual events", batch)
			}
			for _, raw := range batch.Events {
				var event Event
				json.Unmarshal(raw, &event)
				if event.DetailsVersion != 3 || event.Type != "notification" || event.UnreadCount != 3 {
					t.Fatal(event)
				}
				receipts = append(receipts, Receipt{EventID: event.EventID, Status: "pending", RetryAfterSeconds: 30})
			}
		} else {
			if len(batch.Events) != 0 || len(batch.Receipts) != 3 {
				t.Fatal("submitted events must only query receipts")
			}
			for _, id := range batch.Receipts {
				receipts = append(receipts, Receipt{EventID: id, Status: "apns_accepted"})
			}
		}
		if r.Header.Get("Cookie") != "" {
			t.Fatal("cookie leaked")
		}
		raw, _ := json.Marshal(syncResponse{RetryAfterSeconds: 30, Receipts: receipts})
		return response(200, string(raw)), nil
	})
	now := time.Now()
	if err := e.Deliver(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	*count = 0
	hybridStep(t, e)
	if queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox WHERE status='pending' AND submitted=1") != 3 {
		t.Fatal("zero discarded pending receipts")
	}
	if err := e.Deliver(t.Context(), now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("missing receipt query")
	}
}

func TestHybridEnablingCookiePreservesImmutableEventsUntilUnreadRecheck(t *testing.T) {
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
	if queryInt(t, s, "SELECT COUNT(*) FROM outbox WHERE status='pending' AND attempts=0") != 1 || queryInt(t, s, "SELECT COUNT(*) FROM outbox WHERE status='pending' AND attempts=1") != 1 {
		t.Fatal("configuration save changed existing event delivery")
	}
}
