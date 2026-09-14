package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func relayFixture(t *testing.T) (*Engine, time.Time) {
	t.Helper()
	s := testStore(t)
	now := time.Unix(1789200000, 0)
	st := State{AccountID: 7, AnchorID: 1}
	if err := s.SaveConfig(Config{Enabled: true, IntervalSeconds: 180, RelayURL: "https://relay.example", RelayToken: "test-relay-secret"}, st, false); err != nil {
		t.Fatal(err)
	}
	if err := s.ImportPage(st, []Notification{notification(2)}, true); err != nil {
		t.Fatal(err)
	}
	return NewEngine(s), now
}

func TestRelayTimeoutReplaysThenPollsPendingReceipt(t *testing.T) {
	e, now := relayFixture(t)
	calls := 0
	var eventID, original string
	e.Relay = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer test-relay-secret" {
			t.Fatal("relay auth missing")
		}
		switch calls {
		case 1, 2:
			if r.Method != "POST" || r.URL.Path != "/v1/sync" {
				t.Fatal("wrong bounded-wait request")
			}
			raw, _ := io.ReadAll(r.Body)
			var batch syncRequest
			var ev Event
			if err := json.Unmarshal(raw, &batch); err != nil {
				t.Fatal(err)
			}
			if len(batch.Events) != 1 {
				t.Fatal("missing batched event")
			}
			json.Unmarshal(batch.Events[0], &ev)
			if calls == 1 {
				eventID = ev.EventID
				original = string(raw)
				return nil, errors.New("timeout")
			}
			if string(raw) != original {
				t.Fatal("retry changed the event")
			}
			return response(200, `{"receipts":[{"event_id":"`+eventID+`","status":"pending","retry_after_seconds":5}]}`), nil
		case 3:
			var batch syncRequest
			json.NewDecoder(r.Body).Decode(&batch)
			if r.Method != "POST" || len(batch.Events) != 0 || len(batch.Receipts) != 1 || batch.Receipts[0] != eventID {
				t.Fatal("pending receipt was not polled")
			}
			return response(200, `{"receipts":[{"event_id":"`+eventID+`","status":"apns_accepted","apns_id":"test-apns-id"}]}`), nil
		default:
			t.Fatal("unexpected extra delivery")
			return nil, nil
		}
	})}
	for i := 0; i < 3; i++ {
		if err := e.Deliver(context.Background(), now.Add(time.Duration(i)*4*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	ds, _ := e.Store.RecentDeliveries()
	if ds[0].Status != "apns_accepted" || !ds[0].Submitted {
		t.Fatal(ds)
	}
	if err := e.Deliver(context.Background(), now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("accepted event was resent")
	}
}

func TestRelayRejectsMismatchedAndUnknownReceipts(t *testing.T) {
	for _, body := range []string{`{"event_id":"other","status":"apns_accepted"}`, `{not json}`} {
		e, now := relayFixture(t)
		e.Relay = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return response(200, body), nil })}
		if err := e.Deliver(context.Background(), now); err != nil {
			t.Fatal(err)
		}
		ds, _ := e.Store.RecentDeliveries()
		if ds[0].Status != "pending" || ds[0].Submitted {
			t.Fatal("invalid response marked accepted")
		}
	}
}

func TestRelayBlockedAndExpiredEvents(t *testing.T) {
	e, now := relayFixture(t)
	calls := 0
	e.Relay = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { calls++; return response(401, ""), nil })}
	if err := e.Deliver(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	st, _ := e.Store.State()
	e.Store.ImportPage(st, []Notification{notification(3)}, true)
	if err := e.Deliver(context.Background(), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	ds, _ := e.Store.RecentDeliveries()
	if calls != 1 || ds[len(ds)-1].Status != "blocked" {
		t.Fatal(ds)
	}
	e, now = relayFixture(t)
	e.Relay = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("expired event sent"); return nil, nil })}
	if err := e.Deliver(context.Background(), now.Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	ds, _ = e.Store.RecentDeliveries()
	if ds[0].Status != "expired" {
		t.Fatal(ds)
	}
}

func TestRelaySkipsPreBindingNotificationWithoutRetry(t *testing.T) {
	e, now := relayFixture(t)
	calls := 0
	e.Relay = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		var batch syncRequest
		var event Event
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Fatal(err)
		}
		json.Unmarshal(batch.Events[0], &event)
		return response(200, `{"receipts":[{"event_id":"`+event.EventID+`","status":"rejected","reason":"before_binding"}]}`), nil
	})}
	for _, at := range []time.Time{now, now.Add(time.Hour)} {
		if err := e.Deliver(context.Background(), at); err != nil {
			t.Fatal(err)
		}
	}
	ds, _ := e.Store.RecentDeliveries()
	if calls != 1 || ds[0].Status != "skipped" {
		t.Fatal("pre-binding notification was retried", ds)
	}
}

func TestRelayCooldownSurvivesRestartNewEventsAndReconfiguration(t *testing.T) {
	e, now := relayFixture(t)
	calls := 0
	transport := transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		var batch syncRequest
		json.NewDecoder(r.Body).Decode(&batch)
		var ev Event
		json.Unmarshal(batch.Events[0], &ev)
		return response(200, `{"receipts":[{"event_id":"`+ev.EventID+`","status":"apns_accepted"}]}`), nil
	})
	e.Relay = &http.Client{Transport: transport}
	if err := e.Deliver(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	st, _ := e.Store.State()
	e.Store.ImportPage(st, []Notification{notification(3)}, true)
	// Same persisted store, fresh scheduler/engine and a replaced sender token.
	cfg, _ := e.Store.Config()
	cfg.RelayToken = "another-token"
	e.Store.SaveConfig(cfg, st, false)
	e = NewEngine(e.Store)
	e.Relay = &http.Client{Transport: transport}
	if err := e.Deliver(context.Background(), now.Add(179*time.Second)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("restart or new event bypassed cooldown")
	}
	if err := e.Deliver(context.Background(), now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("next round did not resume")
	}
}

func TestRelayBatchesNewEventsAndReceiptsAndHonorsDailyRetryAfter(t *testing.T) {
	e, now := relayFixture(t)
	st, _ := e.Store.State()
	for id := int64(3); id <= 22; id++ {
		if err := e.Store.ImportPage(st, []Notification{notification(id)}, true); err != nil {
			t.Fatal(err)
		}
	}
	// More than a batch worth of each kind; neither kind can starve the other.
	e.Store.DB.Exec("UPDATE outbox SET submitted=1 WHERE rowid<=17")
	calls := 0
	e.Relay = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		var batch syncRequest
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Fatal(err)
		}
		if len(batch.Events) != 4 || len(batch.Receipts) != 16 {
			t.Fatalf("wrong batch sizes: %+v", batch)
		}
		res := response(429, `{"error":"device_daily_budget"}`)
		res.Header.Set("Retry-After", "86400")
		return res, nil
	})}
	if err := e.Deliver(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	// The remaining queued item must not sneak through during the account-wide wait.
	e = NewEngine(e.Store)
	e.Relay = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("daily wait bypassed"); return nil, nil })}
	if err := e.Deliver(context.Background(), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var next int64
	e.Store.DB.QueryRow("SELECT next_sync FROM relay_schedule").Scan(&next)
	if next < now.Add(24*time.Hour).Unix() || calls != 1 {
		t.Fatal("Retry-After lost")
	}
}

func TestRelayReceiptLongBackoffDoesNotDelayFreshEvents(t *testing.T) {
	e, now := relayFixture(t)
	calls := 0
	e.Relay = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		var batch syncRequest
		json.NewDecoder(r.Body).Decode(&batch)
		if len(batch.Events) != 1 || len(batch.Receipts) != 0 {
			t.Fatal("backed-off receipt polled early")
		}
		var ev Event
		json.Unmarshal(batch.Events[0], &ev)
		return response(200, `{"retry_after_seconds":180,"receipts":[{"event_id":"`+ev.EventID+`","status":"pending","retry_after_seconds":3600}]}`), nil
	})}
	e.Deliver(context.Background(), now)
	st, _ := e.Store.State()
	e.Store.ImportPage(st, []Notification{notification(3)}, true)
	if err := e.Deliver(context.Background(), now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("fresh event stalled")
	}
}

func TestRelayPacksEscapedEventsWithinWireLimit(t *testing.T) {
	e, now := relayFixture(t)
	st, _ := e.Store.State()
	for id := int64(3); id <= 5; id++ {
		e.Store.ImportPage(st, []Notification{notification(id)}, true)
	}
	rows, err := e.Store.DB.Query("SELECT payload FROM outbox")
	if err != nil {
		t.Fatal(err)
	}
	events := []Event{}
	for rows.Next() {
		var raw string
		rows.Scan(&raw)
		var ev Event
		json.Unmarshal([]byte(raw), &ev)
		events = append(events, ev)
	}
	rows.Close()
	for _, ev := range events {
		ev.Title = strings.Repeat("&", 220)
		ev.Body = strings.Repeat("&", 500)
		raw, _ := json.Marshal(ev)
		e.Store.DB.Exec("UPDATE outbox SET payload=? WHERE event_id=?", string(raw), ev.EventID)
	}
	e.Relay = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 16<<10 {
			t.Fatal("oversized batch")
		}
		var batch syncRequest
		json.Unmarshal(raw, &batch)
		if len(batch.Events) != 3 {
			t.Fatal("overflow was not retained", len(batch.Events))
		}
		return nil, errors.New("synthetic network failure")
	})}
	if err := e.Deliver(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var untouched int
	e.Store.DB.QueryRow("SELECT COUNT(*) FROM outbox WHERE attempts=0").Scan(&untouched)
	if untouched != 1 {
		t.Fatal("overflow event was consumed")
	}
}
