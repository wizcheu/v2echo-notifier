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

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	return s
}
func seed(t *testing.T, s *Store, st State) {
	t.Helper()
	if err := s.SaveConfig(Config{APIToken: "fake-test-token", Enabled: true, IntervalSeconds: 180}, st, false); err != nil {
		t.Fatal(err)
	}
}
func notification(id int64) Notification {
	n := Notification{ID: id, ForMemberID: 7, Created: 1789200000, Text: "<a>example</a> 回复了你", PayloadRendered: "hello"}
	n.Member.Username = "example"
	return n
}
func pageResponse(ids []int64, total, end int) *http.Response {
	items := []Notification{}
	for _, id := range ids {
		items = append(items, notification(id))
	}
	raw, _ := json.Marshal(map[string]any{"success": true, "message": fmtRange(end, total), "result": items})
	return response(200, string(raw))
}
func fmtRange(end, total int) string { return "Notifications 1-" + itoa(end) + "/" + itoa(total) }
func itoa(n int) string              { raw, _ := json.Marshal(n); return string(raw) }
func queryInt(t *testing.T, s *Store, query string) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func stateOf(t *testing.T, s *Store) State {
	t.Helper()
	st, err := s.State()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestHistoryCatchupAndIncrementalResume(t *testing.T) {
	s := testStore(t)
	seed(t, s, State{AccountID: 7, Username: "example", Verified: true, Phase: "history", Page: 1})
	calls := 0
	pages := []struct {
		page       string
		ids        []int64
		total, end int
	}{
		{"1", []int64{100, 99}, 4, 2},
		// A new notification shifted page 2; overlap must not duplicate rows.
		{"2", []int64{99, 98}, 5, 4},
		{"3", []int64{97}, 5, 5},
		// Catchup discovers the notification created during bootstrap.
		{"1", []int64{101, 100}, 5, 2},
		{"1", []int64{104, 103}, 8, 2},
		{"2", []int64{102, 101}, 8, 4},
	}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if calls >= len(pages) {
			t.Fatal("unexpected request")
		}
		p := pages[calls]
		calls++
		if r.URL.Query().Get("p") != p.page {
			t.Fatalf("page=%s want=%s", r.URL.Query().Get("p"), p.page)
		}
		if r.Header.Get("Authorization") != "Bearer fake-test-token" {
			t.Fatal("missing token")
		}
		return pageResponse(p.ids, p.total, p.end), nil
	})}
	now := time.Unix(1789200000, 0)
	step := func() {
		t.Helper()
		st := stateOf(t, s)
		now = maxTime(now.Add(time.Second), maxTime(st.NextAPI, st.NextCheck).Add(time.Second))
		e := NewEngine(s)
		e.API.Client = client
		if err := e.Step(context.Background(), now); err != nil {
			t.Fatal(err)
		}
	}
	step()
	step()
	step()
	if n := queryInt(t, s, "SELECT COUNT(*) FROM notifications"); n != 4 {
		t.Fatalf("historical count %d", n)
	}
	if n := queryInt(t, s, "SELECT COUNT(*) FROM outbox"); n != 0 {
		t.Fatal("history created push events")
	}
	step()
	if st := stateOf(t, s); st.Phase != "live" || st.HighWater != 101 {
		t.Fatalf("bad catchup state %+v", st)
	}
	if n := queryInt(t, s, "SELECT COUNT(*) FROM outbox"); n != 1 {
		t.Fatal("missed catchup notification")
	}
	step()
	if st := stateOf(t, s); st.Page != 2 || st.HighWater != 101 {
		t.Fatal("cursor advanced before all incremental pages were stored")
	}
	// step constructs a new engine; all pagination progress comes from SQLite.
	step()
	if st := stateOf(t, s); st.HighWater != 104 || st.Page != 1 {
		t.Fatalf("bad final cursor %+v", st)
	}
	if n := queryInt(t, s, "SELECT COUNT(*) FROM notifications"); n != 8 {
		t.Fatalf("notification count %d", n)
	}
	if n := queryInt(t, s, "SELECT COUNT(*) FROM outbox"); n != 4 {
		t.Fatalf("push count %d", n)
	}
}

func TestEmptyHistoryAndRepeatedPage(t *testing.T) {
	s := testStore(t)
	seed(t, s, State{AccountID: 7, Verified: true, Phase: "history", Page: 1})
	e := NewEngine(s)
	e.API.Client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { return pageResponse(nil, 0, 0), nil })}
	now := time.Now()
	if err := e.Step(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	st := stateOf(t, s)
	if !st.AnchorSet || st.AnchorID != 0 || st.Phase != "catchup" {
		t.Fatal(st)
	}
	st.Phase = "history"
	st.Page = 2
	st.LastPageMinID = 99
	st.NextAPI = time.Time{}
	st.NextCheck = time.Time{}
	s.SaveState(st)
	e.API.Client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { return pageResponse([]int64{100, 99}, 20, 4), nil })}
	if err := e.Step(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if st = stateOf(t, s); st.Page != 2 || st.LastError == "" {
		t.Fatal("non-progressing page was accepted")
	}
}

func TestQuotaAnd429AreRespectedByManualCheck(t *testing.T) {
	s := testStore(t)
	seed(t, s, State{AccountID: 7, Verified: true, Phase: "history", Page: 1})
	e := NewEngine(s)
	calls := 0
	e.API.Client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		resp := response(429, "")
		resp.Header.Set("Retry-After", "600")
		return resp, nil
	})}
	now := time.Now()
	if err := e.Step(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	e.RequestCheck()
	if err := e.Step(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("manual check bypassed rate limit")
	}
	if st := stateOf(t, s); st.Page != 1 || !st.NextAPI.After(now.Add(599*time.Second)) {
		t.Fatal("429 lost cursor or cooldown")
	}
}

func TestWrongAccountAndExpiredTokenDoNotAdvance(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"wrong account", `{"success":true,"result":{"id":8,"username":"other"}}`},
		{"expired", `{"success":false,"message":"Token expired"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			seed(t, s, State{AccountID: 7, Phase: "live", Page: 1, HighWater: 100})
			e := NewEngine(s)
			e.API.Client = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return response(200, tc.body), nil })}
			if err := e.Step(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			st := stateOf(t, s)
			if !st.AuthBlocked || st.AccountID != 7 || st.HighWater != 100 {
				t.Fatal(st)
			}
		})
	}
}

func TestFailedPageKeepsCheckpointAndEvents(t *testing.T) {
	s := testStore(t)
	seed(t, s, State{AccountID: 7, Verified: true, Phase: "live", Page: 2, AnchorSet: true, AnchorID: 10, HighWater: 20, CycleMax: 30, LastPageMinID: 29})
	e := NewEngine(s)
	e.API.Client = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("secret URL that must not be logged")
	})}
	if err := e.Step(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	st := stateOf(t, s)
	if st.Page != 2 || st.HighWater != 20 || st.CycleMax != 30 {
		t.Fatal(st)
	}
	if strings.Contains(st.LastError, "secret") {
		t.Fatal("network details leaked")
	}
}
