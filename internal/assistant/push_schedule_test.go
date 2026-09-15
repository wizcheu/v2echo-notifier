package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func beijing(s string) time.Time {
	v, err := time.ParseInLocation("2006-01-02 15:04:05", s, pushTimezone)
	if err != nil {
		panic(err)
	}
	return v
}

func TestPushScheduleBoundaries(t *testing.T) {
	for _, tc := range []struct {
		mode, start, end, now string
		allowed               bool
		next                  string
	}{
		{"window", "08:00", "24:00", "2026-09-15 07:59:59", false, "2026-09-15 08:00:00"},
		{"window", "08:00", "24:00", "2026-09-15 08:00:00", true, "2026-09-15 08:00:00"},
		{"window", "08:00", "24:00", "2026-09-15 23:59:59", true, "2026-09-15 08:00:00"},
		{"window", "08:00", "24:00", "2026-09-16 00:00:00", false, "2026-09-16 08:00:00"},
		{"window", "22:00", "08:00", "2026-09-15 07:59:59", true, "2026-09-14 22:00:00"},
		{"window", "22:00", "08:00", "2026-09-15 08:00:00", false, "2026-09-15 22:00:00"},
		{"window", "22:00", "08:00", "2026-09-15 22:00:00", true, "2026-09-15 22:00:00"},
		{"window", "23:59", "00:00", "2026-12-31 23:59:00", true, "2026-12-31 23:59:00"},
		{"window", "23:59", "00:00", "2027-01-01 00:00:00", false, "2027-01-01 23:59:00"},
		{"window", "00:00", "24:00", "2026-09-15 00:00:00", true, ""},
		{"all_day", "08:00", "24:00", "2026-09-15 02:00:00", true, ""},
	} {
		t.Run(tc.start+"-"+tc.end+"/"+tc.now, func(t *testing.T) {
			s := PushSchedule{Mode: tc.mode, Start: tc.start, End: tc.end}
			for _, zone := range []*time.Location{time.UTC, time.FixedZone("other", -7*3600)} {
				start, _, allowed := s.window(beijing(tc.now).In(zone))
				if allowed != tc.allowed || tc.next != "" && !start.Equal(beijing(tc.next)) {
					t.Fatalf("window=%v %v", start, allowed)
				}
			}
		})
	}
	for _, s := range []PushSchedule{
		{Mode: "bad", Start: "08:00", End: "24:00"}, {Mode: "window", Start: "24:00", End: "08:00"},
		{Mode: "window", Start: "08:00", End: "24:01"}, {Mode: "window", Start: "8:00", End: "24:00"},
		{Mode: "window", Start: "08:00", End: "08:00"}, {Mode: "window", Start: "-1:00", End: "24:00"},
	} {
		if s.validate() == nil {
			t.Fatalf("accepted invalid schedule: %+v", s)
		}
	}
}

func setSchedule(t *testing.T, e *Engine, s *PushSchedule, enabled bool) {
	t.Helper()
	cfg, err := e.Store.Config()
	if err != nil {
		t.Fatal(err)
	}
	cfg.PushSchedule, cfg.Enabled = s, enabled
	if err = e.Store.SaveConfig(cfg, stateOf(t, e.Store), false); err != nil {
		t.Fatal(err)
	}
}

func TestPushScheduleMigrationAndPreservedEdits(t *testing.T) {
	s := testStore(t)
	for _, raw := range []string{`{"interval_seconds":180}`, `{"interval_seconds":180,"push_schedule":null}`} {
		if _, err := s.DB.Exec("INSERT OR REPLACE INTO settings(id,body) VALUES(1,?)", raw); err != nil {
			t.Fatal(err)
		}
		cfg, err := s.Config()
		if err != nil || cfg.schedule() != *defaultPushSchedule() {
			t.Fatal(cfg, err)
		}
	}
	e := NewEngine(s)
	saved := &PushSchedule{Mode: "window", Start: "22:00", End: "08:30"}
	setSchedule(t, e, saved, false)
	if err := e.Configure(Config{IntervalSeconds: 240}); err != nil {
		t.Fatal(err)
	}
	cfg, err := NewEngine(s).Store.Config()
	if err != nil || cfg.schedule() != *saved {
		t.Fatal("omitted schedule was lost", cfg, err)
	}
	invalid := cfg
	invalid.PushSchedule = &PushSchedule{Mode: "window", Start: "09:00", End: "09:00"}
	if err := e.Configure(invalid); err == nil {
		t.Fatal("accepted empty window")
	}
	after, _ := s.Config()
	if !reflect.DeepEqual(cfg, after) {
		t.Fatal("invalid save changed configuration")
	}
	status := cfg.scheduleStatus(beijing("2026-09-15 12:00:00"))
	if status.Resting || !status.NextStart.IsZero() {
		t.Fatal("disabled sync advertised automatic resume", status)
	}
	cfg.Enabled = true
	status = cfg.scheduleStatus(beijing("2026-09-15 12:00:00"))
	if !status.Resting || !status.NextStart.Equal(beijing("2026-09-15 22:00:00")) {
		t.Fatal(status)
	}
}

func TestQuietHoursSkipChecksAndResumeAfterRestart(t *testing.T) {
	e, count, _, calls := hybridFixture(t)
	*count = 0
	setSchedule(t, e, defaultPushSchedule(), true)
	webCalls := 0
	transport := e.Web.Transport
	e.Web.Transport = transportFunc(func(r *http.Request) (*http.Response, error) { webCalls++; return transport.RoundTrip(r) })
	before := stateOf(t, e.Store)
	for _, now := range []time.Time{beijing("2026-09-15 00:00:00"), beijing("2026-09-15 07:59:59")} {
		if err := e.Step(context.Background(), now); err != nil {
			t.Fatal(err)
		}
		if err := e.requestCheck(now); !errors.Is(err, ErrQuietHours) {
			t.Fatal(err)
		}
	}
	if webCalls != 0 || *calls != 0 || queryInt(t, e.Store, "SELECT COUNT(*) FROM check_history") != 0 || !reflect.DeepEqual(before, stateOf(t, e.Store)) {
		t.Fatal("rest triggered work or mutated state")
	}
	restarted := NewEngine(e.Store)
	restarted.Web = e.Web
	restarted.API = e.API
	now := beijing("2026-09-15 08:00:00")
	if err := restarted.Step(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if webCalls != 1 || queryInt(t, e.Store, "SELECT COUNT(*) FROM check_history") != 1 {
		t.Fatal("resume failed")
	}
	if err := restarted.Step(context.Background(), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if webCalls != 1 {
		t.Fatal("resume bypassed normal cooldown")
	}
	setSchedule(t, restarted, defaultPushSchedule(), false)
	if err := restarted.Step(context.Background(), now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if webCalls != 1 || !errors.Is(restarted.requestCheck(now), ErrSyncDisabled) {
		t.Fatal("disabled account resumed")
	}
}

func TestQuietHoursBlockCredentialProbeAndPushTest(t *testing.T) {
	e, _, _, calls := hybridFixture(t)
	setSchedule(t, e, defaultPushSchedule(), true)
	st := stateOf(t, e.Store)
	st.NextTokenCheck = beijing("2026-09-14 00:00:00")
	e.Store.SaveState(st)
	if err := e.Step(context.Background(), beijing("2026-09-15 02:00:00")); err != nil || *calls != 0 {
		t.Fatal("credential probe ran during rest", err)
	}
	e, _ = pushTestFixture(t)
	setSchedule(t, e, defaultPushSchedule(), true)
	var problem *pushTestError
	err := e.QueuePushTest(strings.Repeat("a", 64), beijing("2026-09-15 02:00:00"))
	if !errors.As(err, &problem) || problem.status != 409 || queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox") != 0 {
		t.Fatal("rest accepted test", err)
	}
}

func TestQuietHoursRelayWaitsForFreshCheck(t *testing.T) {
	e, _ := relayFixture(t)
	setSchedule(t, e, defaultPushSchedule(), true)
	calls := 0
	e.Relay.Transport = transportFunc(func(r *http.Request) (*http.Response, error) { calls++; return response(200, `{"receipts":[]}`), nil })
	if err := e.Deliver(context.Background(), beijing("2026-09-13 02:00:00")); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || queryInt(t, e.Store, "SELECT SUM(attempts) FROM outbox") != 0 {
		t.Fatal("rest sent an event")
	}
	now := beijing("2026-09-13 08:00:00")
	if err := e.Deliver(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("sent before fresh check")
	}
	st := stateOf(t, e.Store)
	st.LastSuccess = now
	e.Store.SaveState(st)
	if err := e.Deliver(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("fresh check did not release pending event")
	}
	// Submitted receipt polling is paused as well.
	e.Store.DB.Exec("UPDATE outbox SET submitted=1,next_attempt=0")
	e.Store.DB.Exec("UPDATE relay_schedule SET next_sync=0")
	if err := e.Deliver(context.Background(), beijing("2026-09-14 02:00:00")); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("rest polled receipts")
	}
}

func TestScheduleContextEndsAtBoundary(t *testing.T) {
	cfg := Config{PushSchedule: defaultPushSchedule()}
	ctx, cancel := cfg.windowContext(context.Background(), beijing("2026-09-15 23:59:59").Add(990*time.Millisecond))
	defer cancel()
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatal(ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("window did not end")
	}
}

func TestResumeSkipsOldZeroUnreadSummaryAndExpiredEvents(t *testing.T) {
	for _, expired := range []bool{false, true} {
		e, count, _, _ := hybridFixture(t)
		*count = 0
		setSchedule(t, e, defaultPushSchedule(), true)
		now := beijing("2026-09-15 08:00:00")
		created := now.Add(-5 * time.Minute)
		if expired {
			created = now.Add(-time.Hour)
		}
		st := stateOf(t, e.Store)
		event := Event{EventID: strings.Repeat("e", 64), Type: "unread_summary", SourceAccountID: 7, UnreadCount: 3, CreatedAt: created, ExpiresAt: created.Add(15 * time.Minute)}
		if err := e.Store.ImportPageAndUnread(st, nil, &event); err != nil {
			t.Fatal(err)
		}
		calls := 0
		e.Relay.Transport = transportFunc(func(*http.Request) (*http.Response, error) { calls++; return response(200, `{}`), nil })
		if !expired {
			if err := e.Step(context.Background(), now); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.Deliver(context.Background(), now); err != nil {
			t.Fatal(err)
		}
		ds, err := e.Store.RecentDeliveries()
		if err != nil || len(ds) != 1 {
			t.Fatal(ds, err)
		}
		want := "skipped"
		if expired {
			want = "expired"
		}
		if calls != 0 || ds[0].Status != want {
			t.Fatalf("old summary: calls=%d deliveries=%+v", calls, ds)
		}
	}
}

func TestCheckCrossingWindowEndDoesNotStartAPI(t *testing.T) {
	e, _, _, calls := hybridFixture(t)
	setSchedule(t, e, defaultPushSchedule(), true)
	e.Web.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		// Model a completed response body arriving at the cancellation boundary.
		<-r.Context().Done()
		return response(200, webPage("tester", 3)), nil
	})
	before := stateOf(t, e.Store)
	now := beijing("2026-09-15 23:59:59").Add(980 * time.Millisecond)
	if err := e.Step(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	after := stateOf(t, e.Store)
	if *calls != 0 || !after.NextAPI.Equal(before.NextAPI) {
		t.Fatal("started API work after closing time")
	}
	records, err := e.Store.CheckHistory()
	if err != nil || len(records) != 1 || records[0].Status != "partial" {
		t.Fatal(records, err)
	}
}

func TestPushScheduleHTTPConfigAndRestResponses(t *testing.T) {
	accounts, dir := testAccounts(t)
	id, e := addTestAccount(t, accounts, "synthetic-pat", 7, "tester")
	if err := e.Store.SaveState(State{Verified: true, AccountID: 7, Username: "tester", Phase: "live"}); err != nil {
		t.Fatal(err)
	}
	_, other := addTestAccount(t, accounts, "synthetic-other", 8, "second")
	setSchedule(t, other, allDaySchedule(), true)
	server, err := NewServer(accounts, fstest.MapFS{"index.html": {Data: []byte("test")}}, dir)
	if err != nil {
		t.Fatal(err)
	}
	server.sessions["schedule-session"] = time.Now().Add(time.Hour)
	request := func(method, suffix, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost/api/accounts/"+id+suffix, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: "notifier_session", Value: "schedule-session"})
		r.Header.Set("Origin", "http://localhost")
		r.Header.Set("X-V2Echo-Request", "1")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		return w
	}
	// A one-minute future interval leaves this test in rest regardless of local clock or date.
	local := time.Now().In(pushTimezone)
	start, end := local.Add(2*time.Minute).Format("15:04"), local.Add(3*time.Minute).Format("15:04")
	raw := fmt.Sprintf(`{"enabled":true,"interval_seconds":180,"push_schedule":{"mode":"window","start":%q,"end":%q}}`, start, end)
	if w := request("PUT", "/config", raw); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cfg, _ := e.Store.Config()
	if cfg.schedule().Start != start {
		t.Fatal("schedule did not persist")
	}
	untouched, _ := other.Store.Config()
	if untouched.schedule().Mode != "all_day" {
		t.Fatal("schedule crossed accounts")
	}
	w := request("GET", "/status", "")
	var status struct {
		Schedule PushScheduleStatus `json:"push_schedule_status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil || w.Code != 200 || !status.Schedule.Resting || status.Schedule.Timezone != "UTC+8" {
		t.Fatal(w.Code, status, err)
	}
	for _, endpoint := range []string{"/check", "/push-test"} {
		body := `{}`
		if endpoint == "/push-test" {
			body = `{"event_id":"` + strings.Repeat("a", 64) + `"}`
		}
		if w := request("POST", endpoint, body); w.Code != 409 || !strings.Contains(w.Body.String(), "休息时段") {
			t.Fatal(endpoint, w.Code, w.Body.String())
		}
	}
	if w := request("PUT", "/config", `{"enabled":true,"interval_seconds":180,"push_schedule":{"mode":"window","start":"08:00","end":"08:00"}}`); w.Code != 400 {
		t.Fatal("invalid interval accepted", w.Code)
	}
}
