package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func latestCheck(t *testing.T, s *Store) CheckRecord {
	t.Helper()
	items, err := s.CheckHistory()
	if err != nil || len(items) == 0 {
		t.Fatalf("missing check: %v", err)
	}
	return items[0]
}

func TestCheckHistoryRetentionRestartAndErase(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 57; i++ {
		if err := s.SaveCheckRecord(CheckRecord{Status: "completed", StartedAt: time.Unix(int64(i), 0)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	items, err := s.CheckHistory()
	if err != nil || len(items) != 50 || items[0].ID != 57 || items[49].ID != 8 {
		t.Fatal(items, err)
	}
	if queryInt(t, s, "SELECT COUNT(*) FROM check_history") != 50 {
		t.Fatal("records not pruned from storage")
	}
	other := testStore(t)
	if records, err := other.CheckHistory(); err != nil || len(records) != 0 {
		t.Fatal("account history leaked", err)
	}
	if err := s.EraseAccount(); err != nil {
		t.Fatal(err)
	}
	if records, err := s.CheckHistory(); err != nil || len(records) != 0 {
		t.Fatal("history not erased", err)
	}
}

func TestCheckHistoryActualAttemptsAndManualRequest(t *testing.T) {
	e, count, _, _ := hybridFixture(t)
	*count = 0
	hybridStep(t, e)
	c := latestCheck(t, e.Store)
	if c.Status != "completed" || c.Trigger != "automatic" || c.UnreadCount == nil || *c.UnreadCount != 0 || c.API.Status != "skipped" {
		t.Fatal(c)
	}
	if err := e.RequestCheck(); err != nil {
		t.Fatal(err)
	}
	if err := e.Step(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	items, _ := e.Store.CheckHistory()
	if len(items) != 1 || !stateOf(t, e.Store).CheckRequested {
		t.Fatal("cooldown poll recorded or manual request lost")
	}
	hybridStep(t, e)
	c = latestCheck(t, e.Store)
	if c.Trigger != "manual" || stateOf(t, e.Store).CheckRequested {
		t.Fatal("manual trigger not consumed", c)
	}
	hybridStep(t, e)
	if latestCheck(t, e.Store).Trigger != "automatic" {
		t.Fatal("manual trigger carried into subsequent checks")
	}
}

func TestCheckHistoryHybridOutcomes(t *testing.T) {
	for _, kind := range []string{"queued", "same-id", "web-failed", "api-failed", "quota", "api-cooldown", "empty-api", "storage-failed"} {
		t.Run(kind, func(t *testing.T) {
			e, _, _, _ := hybridFixture(t)
			st := stateOf(t, e.Store)
			now := time.Now()
			st.NextTokenCheck = now.Add(time.Hour)
			switch kind {
			case "same-id":
				st.HasPushAPIID, st.LastPushAPIID = true, 30
			case "quota":
				st.Quota = Quota{Limit: 120, Remaining: 0, Reset: now.Add(time.Hour), Observed: true}
			case "api-cooldown":
				st.NextAPI = now.Add(time.Hour)
			case "web-failed":
				e.Web.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
					return nil, errors.New("private proxy-user:proxy-password A2=secret")
				})
			case "api-failed":
				e.API.Client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return response(401, `{"message":"invalid token"}`), nil })
			case "empty-api":
				e.API.Client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return pageResponse(nil, 100, 0), nil })
			case "storage-failed":
				if _, err := e.Store.DB.Exec("CREATE TRIGGER reject_event BEFORE INSERT ON outbox BEGIN SELECT RAISE(ABORT,'synthetic storage failure'); END"); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.Store.SaveState(st); err != nil {
				t.Fatal(err)
			}
			err := e.Step(context.Background(), now)
			if (err != nil) != (kind == "storage-failed") {
				t.Fatal(err)
			}
			c := latestCheck(t, e.Store)
			if c.StartedAt.IsZero() || c.FinishedAt.Before(c.StartedAt) || c.DurationMS < 0 {
				t.Fatal("invalid timing", c)
			}
			if kind == "web-failed" {
				if c.Status != "failed" || c.UnreadCount != nil || c.API.Status != "skipped" {
					t.Fatal(c)
				}
			} else if c.UnreadCount == nil || *c.UnreadCount != 3 {
				t.Fatal("lost actual unread observation", c)
			}
			switch kind {
			case "queued":
				if c.Status != "completed" || c.FirstID == nil || *c.FirstID != 30 || c.EventID == "" {
					t.Fatal(c)
				}
			case "same-id":
				if c.Status != "completed" || c.EventID != "" || c.PreviousFirstID == nil || *c.PreviousFirstID != 30 {
					t.Fatal(c)
				}
			case "api-failed", "empty-api":
				if c.Status != "partial" || c.API.Status != "failed" || c.EventID != "" {
					t.Fatal(c)
				}
			case "quota", "api-cooldown":
				if c.Status != "partial" || c.API.Status != "skipped" || c.FirstID != nil {
					t.Fatal(c)
				}
			case "storage-failed":
				if c.Status != "failed" || c.EventID != "" || stateOf(t, e.Store).HasPushAPIID {
					t.Fatal(c)
				}
			}
			raw, _ := json.Marshal(c)
			for _, secret := range []string{"fake-pat", "fake-cookie", "fake-relay", "proxy-password", "A2="} {
				if strings.Contains(string(raw), secret) {
					t.Fatal("secret in check history")
				}
			}
		})
	}
}

func TestCheckHistoryLegacyDoesNotInventUnreadCount(t *testing.T) {
	e, _, _, _ := hybridFixture(t)
	cfg, _ := e.Store.Config()
	cfg.Cookie = ""
	st := stateOf(t, e.Store)
	st.Page = 1
	if err := e.Store.SaveConfig(cfg, st, false); err != nil {
		t.Fatal(err)
	}
	if err := e.Step(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	c := latestCheck(t, e.Store)
	if c.Mode != "api" || c.Status != "completed" || c.UnreadCount != nil || c.Web.Status != "skipped" || c.FirstID == nil {
		t.Fatal(c)
	}
}

func TestCheckHistoryMissingBrowserReportsActionableCause(t *testing.T) {
	e, _, _, apiCalls := hybridFixture(t)
	st := stateOf(t, e.Store)
	st.HasPushAPIID, st.LastPushAPIID = true, 30
	st.NextTokenCheck = time.Now().Add(time.Hour)
	if err := e.Store.SaveState(st); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.saveBrowser(browserState{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	e.Web.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("missing browser must not silently fall back to normal HTTP")
		return nil, nil
	})
	hybridStep(t, e)
	check, after := latestCheck(t, e.Store), stateOf(t, e.Store)
	if check.Web.Detail != after.LastError || !strings.Contains(check.Web.Detail, "尚未部署浏览器配套容器") {
		t.Fatalf("specific browser cause was lost: %s", check.Web.Detail)
	}
	if check.Web.HTTPStatus != 0 || check.API.Status != "skipped" || *apiCalls != 0 || after.LastPushAPIID != 30 {
		t.Fatal("missing browser changed the baseline or claimed an upstream request", check)
	}
}
