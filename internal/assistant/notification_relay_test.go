package assistant

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDetailedRelayWaitsForCapabilityAndDrainsTwentyPerBatch(t *testing.T) {
	e, now := pushTestFixture(t)
	st := stateOf(t, e.Store)
	for id := range 25 {
		event := notificationEvent(st, notification(int64(id+1)), now, 25)
		raw, _ := json.Marshal(event)
		if _, err := e.Store.DB.Exec("INSERT INTO outbox(event_id,payload) VALUES(?,?)", event.EventID, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	upgraded, rollback := false, false
	var batchSizes []int
	e.Relay.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/v1/recipient-keys") {
			return response(200, `{"source_account_id":7,"recipients":[]}`), nil
		}
		if r.URL.Path == "/api/push/v1/health" {
			if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
				t.Fatal("capability probe leaked credentials")
			}
			if !upgraded {
				return response(200, `{"status":"ok"}`), nil
			}
			return response(200, `{"notification_body_encryption":2,"notification_routing":1,"notification_details":1,"notification_test_samples":1,"max_events":20,"max_receipts":50,"max_sync_bytes":65536}`), nil
		}
		var batch syncRequest
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Fatal(err)
		}
		batchSizes = append(batchSizes, len(batch.Events))
		if rollback {
			return response(400, `{"error":"unknown_field"}`), nil
		}
		result := syncResponse{RetryAfterSeconds: 30}
		for _, raw := range batch.Events {
			var event Event
			if err := json.Unmarshal(raw, &event); err != nil || event.DetailsVersion != 3 {
				t.Fatal(event, err)
			}
			result.Receipts = append(result.Receipts, Receipt{EventID: event.EventID, Status: "apns_accepted"})
		}
		raw, _ := json.Marshal(result)
		return response(200, string(raw)), nil
	})
	if err := e.Deliver(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if len(batchSizes) != 0 || queryInt(t, e.Store, "SELECT SUM(attempts) FROM outbox") != 0 {
		t.Fatal("old service received unsupported events")
	}
	upgraded, rollback = true, true
	if err := e.Deliver(t.Context(), now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox WHERE status='pending'") != 25 {
		t.Fatal("rolling rollback lost events")
	}
	rollback = false
	for i := range 2 {
		if err := e.Deliver(t.Context(), now.Add(time.Duration(i+2)*4*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if fmt.Sprint(batchSizes) != "[20 20 5]" || queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox WHERE status='apns_accepted'") != 25 {
		t.Fatal(batchSizes)
	}
}

func TestDetailedRelayExpiresWhileServiceIsOld(t *testing.T) {
	e, now := pushTestFixture(t)
	if err := e.QueuePushTest(strings.Repeat("a", 64), now, "mention"); err != nil {
		t.Fatal(err)
	}
	e.Relay.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("expired event should not probe or send")
		return nil, nil
	})
	if err := e.Deliver(t.Context(), now.Add(16*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox WHERE status='expired'") != 1 {
		t.Fatal("event did not expire")
	}
}

func TestAllNotificationTestKindsHaveImmutableTemplatesAndSafeWire(t *testing.T) {
	for _, sample := range pushTestSamples() {
		t.Run(sample.Kind, func(t *testing.T) {
			e, now := pushTestFixture(t)
			id := strings.Repeat("b", 64)
			if err := e.QueuePushTest(id, now, sample.Kind); err != nil {
				t.Fatal(err)
			}
			if err := e.QueuePushTest(id, now, sample.Kind); err != nil {
				t.Fatal(err)
			}
			if err := e.QueuePushTest(id, now, ""); err == nil {
				t.Fatal("changed kind reused an ID")
			}
			state, err := e.Store.PushTestState()
			if err != nil || len(state.Samples) != 5 || state.Latest.Title != sample.Title || state.Latest.Body != sample.Body || state.Latest.AvatarURL != sample.AvatarURL {
				t.Fatal(state, err)
			}
			e.Relay.Transport = detailedTransport(func(r *http.Request) (*http.Response, error) {
				var batch syncRequest
				if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
					t.Fatal(err)
				}
				var wire map[string]any
				if len(batch.Events) != 1 {
					t.Fatal(batch)
				}
				json.Unmarshal(batch.Events[0], &wire)
				if len(wire) != 8 || wire["test_kind"] != sample.Kind || wire["avatar_url"] != nil || wire["title"] != nil || wire["unread_count"] != nil {
					t.Fatal(wire)
				}
				return response(200, `{"receipts":[{"event_id":"`+id+`","status":"apns_accepted"}]}`), nil
			})
			if err := e.Deliver(t.Context(), now); err != nil {
				t.Fatal(err)
			}
		})
	}
	e, now := pushTestFixture(t)
	if problem, ok := errors.AsType[*pushTestError](e.QueuePushTest(strings.Repeat("c", 64), now, "unknown")); !ok || problem.status != 400 {
		t.Fatal("invalid kind accepted")
	}
}
