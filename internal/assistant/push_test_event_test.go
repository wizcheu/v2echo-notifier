package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func pushTestFixture(t *testing.T) (*Engine, time.Time) {
	t.Helper()
	s := testStore(t)
	st := State{Verified: true, Username: "tester", AccountID: 7, Phase: "live", HighWater: 42, AnchorID: 40}
	cfg := Config{APIToken: "synthetic-pat", Enabled: true, IntervalSeconds: 180, RelayURL: PushServiceURL, RelayToken: "synthetic-sender"}
	if err := s.SaveConfig(cfg, st, false); err != nil {
		t.Fatal(err)
	}
	return NewEngine(s), time.Now().UTC().Truncate(time.Second)
}

func TestPushTestReplayCooldownAndHistory(t *testing.T) {
	e, now := pushTestFixture(t)
	id := strings.Repeat("a", 64)
	before := stateOf(t, e.Store)
	if _, err := e.Store.DB.Exec("UPDATE relay_schedule SET next_sync=? WHERE id=1", now.Add(time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := e.QueuePushTest(id, now); err != nil {
			t.Fatal(err)
		}
	}
	test, err := e.Store.PushTestState()
	if err != nil || test.Latest == nil || test.Latest.EventID != id || test.Latest.Type != "test" || test.Latest.Body != PushTestBody || test.Latest.Title != "@tester" || test.Latest.NotificationID != 0 || test.NextAllowed != now.Add(relayInterval).Unix() {
		t.Fatal(test, err)
	}
	var next, count int64
	e.Store.DB.QueryRow("SELECT next_sync FROM relay_schedule WHERE id=1").Scan(&next)
	e.Store.DB.QueryRow("SELECT COUNT(*) FROM outbox").Scan(&count)
	if next != now.Add(time.Minute).Unix() || count != 1 {
		t.Fatal("replay reset relay schedule or duplicated event")
	}
	if !reflect.DeepEqual(before, stateOf(t, e.Store)) {
		t.Fatal("test changed V2EX progress")
	}
	var problem *pushTestError
	if err := e.QueuePushTest(strings.Repeat("b", 64), now.Add(time.Second)); !errors.As(err, &problem) || problem.status != 429 {
		t.Fatal("missing cooldown", err)
	}
	if err := e.QueuePushTest(strings.Repeat("b", 64), now.Add(4*time.Minute)); !errors.As(err, &problem) || problem.status != 409 {
		t.Fatal("accepted another pending test", err)
	}
	// Re-creation of the engine must not lose the persisted cooldown.
	if err := NewEngine(e.Store).QueuePushTest(strings.Repeat("c", 64), now.Add(time.Second)); !errors.As(err, &problem) || problem.status != 429 {
		t.Fatal("cooldown lost", err)
	}
	if err := e.QueuePushTest(strings.Repeat("b", 64), now.Add(16*time.Minute)); err != nil {
		t.Fatal("expired test prevented a new test", err)
	}
}

func TestPushTestUsesRelayAndReceiptWithoutUploadingLocalTemplate(t *testing.T) {
	e, now := pushTestFixture(t)
	id := strings.Repeat("d", 64)
	if err := e.QueuePushTest(id, now); err != nil {
		t.Fatal(err)
	}
	calls := 0
	var original string
	e.Relay = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != PushServiceURL+"/v1/sync" || r.Header.Get("Authorization") != "Bearer synthetic-sender" {
			t.Fatal("wrong destination or credential")
		}
		var batch syncRequest
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Fatal(err)
		}
		if calls <= 2 {
			if len(batch.Events) != 1 || len(batch.Receipts) != 0 {
				t.Fatal(batch)
			}
			var event map[string]any
			json.Unmarshal(batch.Events[0], &event)
			if len(event) != 5 || event["type"] != "test" || event["event_id"] != id || event["title"] != nil || event["notification_id"] != nil {
				t.Fatal("test wire shape", event)
			}
			if calls == 1 {
				original = string(batch.Events[0])
				return nil, errors.New("synthetic timeout")
			}
			if original != string(batch.Events[0]) {
				t.Fatal("retry changed payload")
			}
			return response(200, `{"receipts":[{"event_id":"`+id+`","status":"pending"}]}`), nil
		}
		if len(batch.Events) != 0 || len(batch.Receipts) != 1 || batch.Receipts[0] != id {
			t.Fatal("expected receipt lookup", batch)
		}
		return response(200, `{"receipts":[{"event_id":"`+id+`","status":"apns_accepted"}]}`), nil
	})}
	for i := 0; i < 3; i++ {
		if err := e.Deliver(context.Background(), now.Add(time.Duration(i)*4*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	test, err := e.Store.PushTestState()
	if err != nil || test.Latest.Status != "apns_accepted" || test.Latest.Attempts != 3 || test.Latest.FirstAttemptAt == 0 || test.Latest.FinishedAt == 0 {
		t.Fatal(test, err)
	}
	if err := e.QueuePushTest(id, now.Add(12*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := e.Deliver(context.Background(), now.Add(12*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("accepted test resent")
	}
}

func TestPushTestReadinessAndConcurrentClicks(t *testing.T) {
	for _, kind := range []string{"paused", "unverified", "unpaired", "blocked", "custom-url"} {
		t.Run(kind, func(t *testing.T) {
			e, now := pushTestFixture(t)
			cfg, _ := e.Store.Config()
			st := stateOf(t, e.Store)
			switch kind {
			case "paused":
				cfg.Enabled = false
			case "unverified":
				st.Verified = false
			case "unpaired":
				cfg.RelayToken = ""
			case "custom-url":
				cfg.RelayURL = "https://other.example"
			case "blocked":
				e.Store.DB.Exec("UPDATE relay_schedule SET blocked=1 WHERE id=1")
			}
			if err := e.Store.SaveConfig(cfg, st, false); err != nil {
				t.Fatal(err)
			}
			var problem *pushTestError
			if err := e.QueuePushTest(strings.Repeat("e", 64), now); !errors.As(err, &problem) || problem.status != 409 {
				t.Fatal(err)
			}
		})
	}
	e, now := pushTestFixture(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := e.QueuePushTest(strings.Repeat("f", 64), now); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var count int
	e.Store.DB.QueryRow("SELECT COUNT(*) FROM outbox").Scan(&count)
	if count != 1 {
		t.Fatal("concurrent duplicate tests", count)
	}
	other, _ := pushTestFixture(t)
	state, err := other.Store.PushTestState()
	if err != nil || state.Latest != nil || state.NextAllowed != 0 {
		t.Fatal("test crossed account boundary", state, err)
	}
}
