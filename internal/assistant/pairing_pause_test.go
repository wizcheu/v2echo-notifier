package assistant

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestInvalidPairingPausesChecksUntilSuccessfulPair(t *testing.T) {
	for _, status := range []int{401, 403} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			e, _, _, _ := hybridFixture(t)
			initial := stateOf(t, e.Store)
			initial.InitialPairingDone = true
			if err := e.Store.SaveState(initial); err != nil {
				t.Fatal(err)
			}
			hybridStep(t, e)
			e.Relay.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return response(status, ""), nil })
			if err := e.Deliver(context.Background(), time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			blocked, err := e.Store.RelayBlocked()
			if err != nil || !blocked {
				t.Fatalf("expected invalid pairing: %v %v", blocked, err)
			}
			before := stateOf(t, e.Store)
			records := queryInt(t, e.Store, "SELECT COUNT(*) FROM check_history")
			pending := queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox")
			// Recreate the engine: the pause comes from persisted state, not process memory.
			resumed := NewEngine(e.Store)
			deny := transportFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("blocked account made a network request")
				return nil, errors.New("unexpected network")
			})
			resumed.API.Client.Transport, resumed.Web.Transport, resumed.Relay.Transport = deny, deny, deny
			later := time.Now().Add(48 * time.Hour)
			if err := resumed.Step(context.Background(), later); err != nil {
				t.Fatal(err)
			}
			if err := resumed.Deliver(context.Background(), later); err != nil {
				t.Fatal(err)
			}
			if err := resumed.requestCheck(later); !errors.Is(err, ErrPairingRequired) {
				t.Fatalf("manual check: %v", err)
			}
			cfg, _ := resumed.Store.Config()
			if err := resumed.Configure(cfg); err != nil {
				t.Fatal(err)
			}
			if blocked, _ := resumed.Store.RelayBlocked(); !blocked {
				t.Fatal("ordinary config save cleared pairing pause")
			}
			after := stateOf(t, e.Store)
			if before.LastSuccess != after.LastSuccess || before.NextTokenCheck != after.NextTokenCheck || before.NextAPI != after.NextAPI || records != queryInt(t, e.Store, "SELECT COUNT(*) FROM check_history") || pending != queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox") {
				t.Fatal("paused checks mutated history or check progress")
			}
			resumed.Relay.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return response(400, ""), nil })
			code := "V2E-" + strings.Repeat("b", 24)
			if err := resumed.Pair(context.Background(), PushServiceURL, code, ""); err == nil {
				t.Fatal("failed pair succeeded")
			}
			if blocked, _ := resumed.Store.RelayBlocked(); !blocked {
				t.Fatal("failed pair cleared pause")
			}
			resumed.Relay.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
				return response(200, `{"paired":true,"binding_id":"new-binding","username":"tester"}`), nil
			})
			if err := resumed.Pair(context.Background(), PushServiceURL, code, ""); err != nil {
				t.Fatal(err)
			}
			if blocked, _ := resumed.Store.RelayBlocked(); blocked {
				t.Fatal("successful pair did not clear pause")
			}
			cfg, _ = resumed.Store.Config()
			if !cfg.Enabled {
				t.Fatal("pause changed the user's enabled setting")
			}
			webCalls := 0
			resumed.Web.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
				webCalls++
				return response(200, webPage("tester", 0)), nil
			})
			st := stateOf(t, resumed.Store)
			st.NextCheck = time.Time{}
			st.NextWeb = time.Time{}
			st.NextAPI = time.Time{}
			st.NextTokenCheck = later.Add(time.Hour)
			if err := resumed.Store.SaveState(st); err != nil {
				t.Fatal(err)
			}
			if err := resumed.Step(context.Background(), later); err != nil {
				t.Fatal(err)
			}
			if webCalls != 1 {
				t.Fatalf("checks did not resume: %d", webCalls)
			}
			if queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox WHERE status IN ('blocked','pending')") != 0 {
				t.Fatal("old binding events were revived")
			}
		})
	}
}

func TestPairingPauseDoesNotBlockOtherAccounts(t *testing.T) {
	a, _ := testAccounts(t)
	_, blocked := addTestAccount(t, a, "blocked-pat", 7, "alpha")
	_, active := addTestAccount(t, a, "active-pat", 8, "beta")
	if _, err := blocked.Store.DB.Exec("UPDATE relay_schedule SET blocked=1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	blocked.API.Client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("blocked legacy account requested API")
		return nil, errors.New("unexpected network")
	})
	now := time.Now()
	cursor := 0
	for second := 0; second < 2; second++ {
		if err := a.collect(context.Background(), now.Add(time.Duration(second)*time.Second), &cursor); err != nil {
			t.Fatal(err)
		}
	}
	if stateOf(t, blocked.Store).Verified {
		t.Fatal("blocked account ran credential verification")
	}
	if !stateOf(t, active.Store).Verified {
		t.Fatal("pairing pause stopped the other account")
	}
}

func TestPairingPauseDoesNotStarveWebChecksDuringAPICooldown(t *testing.T) {
	a, _ := testAccounts(t)
	addTestAccount(t, a, "legacy-pat", 7, "legacy")
	blockedID := seedTestAccount(t, a, "blocked-pat", "A2=synthetic")
	activeID := seedTestAccount(t, a, "active-pat", "A2=synthetic")
	blocked, _ := a.Get(blockedID)
	active, _ := a.Get(activeID)
	for _, e := range []*Engine{blocked, active} {
		if err := e.Store.SaveState(State{Verified: true, AccountID: 8, Username: "tester"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := blocked.Store.DB.Exec("UPDATE relay_schedule SET blocked=1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	blocked.Web.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("blocked account read homepage")
		return nil, errors.New("unexpected network")
	})
	reads := 0
	active.Web.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		reads++
		return response(200, webPage("tester", 0)), nil
	})
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
	if reads != 1 || cursor != 0 {
		t.Fatal("paused account starved another account during API cooldown")
	}
}
