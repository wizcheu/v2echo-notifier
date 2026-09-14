package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func historyOne(t *testing.T, s *Store) DeliveryHistoryItem {
	t.Helper()
	page, err := s.DeliveryHistory(0, 20, "", "")
	if err != nil || len(page.Items) != 1 {
		t.Fatal("history read", err, len(page.Items))
	}
	return page.Items[0]
}

func TestDeliveryHistoryTracksAttemptsReceiptsAndAtomicity(t *testing.T) {
	e, now := relayFixture(t)
	queued := historyOne(t, e.Store)
	if queued.QueuedAt == 0 || queued.FirstAttemptAt != 0 || queued.CreatedAt.Unix() != now.Unix() || queued.Body != "hello" {
		t.Fatal("incorrect initial history", queued)
	}
	tx, _ := e.Store.DB.Begin()
	if _, err := tx.Exec("UPDATE outbox SET attempts=1"); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	if item := historyOne(t, e.Store); item.Attempts != 0 || item.FirstAttemptAt != 0 {
		t.Fatal("rolled-back attempt left history")
	}
	calls := 0
	e.Relay = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		// Reservation and timing must already be durable before network I/O.
		item := historyOne(t, e.Store)
		if item.FirstAttemptAt == 0 || item.LastAttemptAt == 0 || item.Attempts != calls {
			t.Fatal("attempt not recorded before request")
		}
		status := "pending"
		if calls == 2 {
			status = "apns_accepted"
		}
		return response(200, fmt.Sprintf(`{"receipts":[{"event_id":%q,"status":%q}]}`, item.EventID, status)), nil
	})}
	if err := e.Deliver(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	pending := historyOne(t, e.Store)
	if pending.SubmittedAt == 0 || pending.FinishedAt != 0 || !pending.Submitted {
		t.Fatal("incorrect pending history")
	}
	if err := e.Deliver(context.Background(), now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	accepted := historyOne(t, e.Store)
	if accepted.Status != "apns_accepted" || accepted.FinishedAt == 0 || accepted.FirstAttemptAt != pending.FirstAttemptAt || accepted.SubmittedAt != pending.SubmittedAt || accepted.QueuedAt != queued.QueuedAt {
		t.Fatal("history timeline changed incorrectly")
	}
	if err := e.Store.ImportPage(State{AccountID: 7, AnchorID: 1}, []Notification{notification(2)}, true); err != nil {
		t.Fatal(err)
	}
	if after := historyOne(t, e.Store); after.Sequence != accepted.Sequence || after.FinishedAt != accepted.FinishedAt || after.Attempts != 2 {
		t.Fatal("replayed import reset history")
	}
}

func TestDeliveryHistoryPaginationSearchAndSummary(t *testing.T) {
	s := testStore(t)
	for i := 1; i <= 61; i++ {
		n := notification(int64(i))
		n.Text = fmt.Sprintf("消息 %d", i)
		if i == 60 {
			n.Text = "100% literal_needle"
		}
		if err := s.ImportPage(State{AccountID: 7}, []Notification{n}, true); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.DeliveryHistory(0, 20, "", "")
	if err != nil || len(page.Items) != 20 || page.NextCursor == 0 || page.Items[0].NotificationID != 61 {
		t.Fatal("first page failed", err)
	}
	// New records between pages must not shift the cursor into duplicates.
	if err := s.ImportPage(State{AccountID: 7}, []Notification{notification(62)}, true); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for {
		for _, item := range page.Items {
			if seen[item.EventID] {
				t.Fatal("duplicate page item")
			}
			seen[item.EventID] = true
		}
		if page.NextCursor == 0 {
			break
		}
		page, err = s.DeliveryHistory(page.NextCursor, 20, "", "")
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 61 {
		t.Fatal("history truncated", len(seen))
	}
	page, err = s.DeliveryHistory(0, 20, "pending", "100% LITERAL_needle")
	if err != nil || len(page.Items) != 1 || page.Items[0].NotificationID != 60 {
		t.Fatal("literal/case search failed", err)
	}
	page, err = s.DeliveryHistory(0, 20, "", "' OR 1=1 --")
	if err != nil || len(page.Items) != 0 {
		t.Fatal("query was not treated as text")
	}
	initial := Event{EventID: strings.Repeat("f", 64), Type: "initial_unread", UnreadCount: 5, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(5 * time.Minute)}
	if err := s.saveConfig(Config{}, State{}, false, false, &initial); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE outbox SET status='skipped' WHERE event_id=?", initial.EventID); err != nil {
		t.Fatal(err)
	}
	page, err = s.DeliveryHistory(0, 20, "skipped", "")
	if err != nil || len(page.Items) != 1 || page.Items[0].Type != "initial_unread" || page.Items[0].UnreadCount != 5 || page.Items[0].FinishedAt == 0 {
		t.Fatal("summary/filter history failed", err)
	}
	if err := s.EraseAccount(); err != nil {
		t.Fatal(err)
	}
	if queryInt(t, s, "SELECT COUNT(*) FROM delivery_history") != 0 {
		t.Fatal("account removal retained push history")
	}
}

func TestDeliveryHistoryOpensExistingV4WithoutInventingTimestamps(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce the deployed v4 schema before this additive history feature.
	if _, err = s.DB.Exec("DROP TRIGGER outbox_history_insert; DROP TRIGGER outbox_history_update; DROP TABLE delivery_history"); err != nil {
		t.Fatal(err)
	}
	st := State{AccountID: 7, Verified: true, NextAPI: time.Now().Add(time.Hour)}
	if err = s.SaveConfig(Config{APIToken: "preserved-pat"}, st, false); err != nil {
		t.Fatal(err)
	}
	ev := Event{EventID: strings.Repeat("a", 64), Type: "notification", Title: "旧消息", Body: "旧摘要", CreatedAt: time.Unix(1234, 0)}
	raw, _ := json.Marshal(ev)
	if _, err = s.DB.Exec("INSERT INTO outbox(event_id,payload,status,submitted,attempts) VALUES(?,?,'apns_accepted',1,3)", ev.EventID, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE relay_schedule SET next_sync=9999999999,failures=3,blocked=1"); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	item := historyOne(t, s)
	if item.Title != "旧消息" || item.Status != "apns_accepted" || item.CreatedAt.Unix() != 1234 || item.QueuedAt != 0 || item.FirstAttemptAt != 0 || item.SubmittedAt != 0 || item.FinishedAt != 0 {
		t.Fatal("old metadata invented or lost", item)
	}
	cfg, _ := s.Config()
	after := stateOf(t, s)
	if cfg.APIToken != "preserved-pat" || !after.NextAPI.Equal(st.NextAPI) || queryInt(t, s, "SELECT next_sync FROM relay_schedule") != 9999999999 || queryInt(t, s, "SELECT blocked FROM relay_schedule") != 1 {
		t.Fatal("upgrade changed config or cooldown")
	}
	if _, err = s.DB.Exec("UPDATE outbox SET status='pending',submitted=0 WHERE event_id=?", ev.EventID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE outbox SET attempts=4 WHERE event_id=?", ev.EventID); err != nil {
		t.Fatal(err)
	}
	item = historyOne(t, s)
	if item.FirstAttemptAt != 0 || item.QueuedAt != 0 || item.LastAttemptAt == 0 {
		t.Fatal("legacy retry fabricated first attempt")
	}
}
