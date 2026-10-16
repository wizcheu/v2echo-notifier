package assistant

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

func continueUnread(t *testing.T, e *Engine) {
	t.Helper()
	st := stateOf(t, e.Store)
	now := maxTime(time.Now(), st.NextAPI.Add(time.Second))
	if err := e.Step(t.Context(), now); err != nil {
		t.Fatal(err)
	}
}

func TestUnreadPagesRestartFailureAndAtomicOutbox(t *testing.T) {
	e, count, _, _ := hybridFixture(t)
	*count = 5
	failed := false
	pages := []int{}
	e.API.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		p := r.URL.Query().Get("p")
		if p == "1" {
			pages = append(pages, 1)
			return pageResponse([]int64{50, 49, 48}, 6, 3), nil
		}
		pages = append(pages, 2)
		if !failed {
			failed = true
			return nil, errors.New("network interrupted")
		}
		return pageResponse([]int64{48, 47, 46, 45}, 7, 7), nil
	})
	hybridStep(t, e)
	if queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox") != 0 || stateOf(t, e.Store).UnreadScan.Page != 2 {
		t.Fatal("partial range became deliverable")
	}
	restart := NewEngine(e.Store)
	restart.API = e.API
	restart.Web = e.Web
	e = restart
	continueUnread(t, e)
	if stateOf(t, e.Store).UnreadScan.Page != 2 || queryInt(t, e.Store, "SELECT COUNT(*) FROM unread_scan_items") != 3 {
		t.Fatal("failed page consumed progress")
	}
	continueUnread(t, e)
	if !stateOf(t, e.Store).UnreadScan.Verify || queryInt(t, e.Store, "SELECT COUNT(*) FROM unread_scan_items") != 5 {
		t.Fatal("overlap counted twice or failed to stage page")
	}
	if _, err := e.Store.DB.Exec("CREATE TRIGGER fail_unread BEFORE INSERT ON outbox BEGIN SELECT RAISE(ABORT,'disk full'); END"); err != nil {
		t.Fatal(err)
	}
	st := stateOf(t, e.Store)
	if err := e.Step(t.Context(), st.NextAPI.Add(time.Second)); err == nil {
		t.Fatal("expected atomic storage failure")
	}
	if queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox") != 0 || !stateOf(t, e.Store).UnreadScan.Verify {
		t.Fatal("checkpoint committed without events")
	}
	e.Store.DB.Exec("DROP TRIGGER fail_unread")
	continueUnread(t, e)
	if queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox") != 5 || stateOf(t, e.Store).UnreadScan != nil {
		t.Fatal("restart lost notifications")
	}
	var before string
	e.Store.DB.QueryRow("SELECT payload FROM outbox ORDER BY rowid LIMIT 1").Scan(&before)
	// A stable range is collected again, but existing payloads/IDs never change.
	hybridStep(t, e)
	continueUnread(t, e)
	continueUnread(t, e)
	var after string
	e.Store.DB.QueryRow("SELECT payload FROM outbox ORDER BY rowid LIMIT 1").Scan(&after)
	if before != after || queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox") != 5 {
		t.Fatal("replay mutated an event")
	}
}

func TestUnreadMovingHeadDefersCommitUntilFreshSnapshot(t *testing.T) {
	e, count, _, _ := hybridFixture(t)
	*count = 4
	firstCalls := 0
	e.API.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("p") == "2" {
			return pageResponse([]int64{28, 27}, 4, 4), nil
		}
		firstCalls++
		if firstCalls == 1 {
			return pageResponse([]int64{30, 29}, 4, 2), nil
		}
		return pageResponse([]int64{31, 30}, 5, 2), nil
	})
	hybridStep(t, e)
	continueUnread(t, e)
	continueUnread(t, e)
	if stateOf(t, e.Store).UnreadScan != nil || queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox") != 0 {
		t.Fatal("moving head committed stale range")
	}
	*count = 0
	hybridStep(t, e)
	if queryInt(t, e.Store, "SELECT COUNT(*) FROM unread_scan_items") != 0 {
		t.Fatal("read notification candidates retained")
	}
}

func TestUpgradeCurrentUnreadPreservesAttemptedLegacyAndDeduplicatesIDs(t *testing.T) {
	e, count, _, _ := hybridFixture(t)
	st := stateOf(t, e.Store)
	st.HasPushAPIID = true
	st.LastPushAPIID = 30
	old := Event{EventID: "old-summary", Type: "unread_summary", UnreadCount: 3, SourceAccountID: 7, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	raw, _ := json.Marshal(old)
	e.Store.DB.Exec("INSERT INTO outbox(event_id,payload,attempts) VALUES(?,?,1)", old.EventID, string(raw))
	e.Store.SaveState(st)
	hybridStep(t, e)
	if queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox WHERE json_extract(payload,'$.type')='notification'") != 3 {
		t.Fatal("summary baseline suppressed first detail scan")
	}
	*count = 0
	hybridStep(t, e)
	if queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox WHERE status='skipped'") != 3 || queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox WHERE event_id='old-summary' AND status='pending'") != 1 {
		t.Fatal("zero changed uncertain attempt or retained unsent details")
	}
}
