package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestPairingRecoversLostResponseAndRetiresPreviousConnection(t *testing.T) {
	e, _ := relayFixture(t)
	st, _ := e.Store.State()
	st.Username, st.Verified, st.InitialPairingDone = "tester", true, true
	if err := e.Store.SaveState(st); err != nil {
		t.Fatal(err)
	}
	var firstToken string
	calls := 0
	e.Relay = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		var body struct {
			Code     string `json:"code"`
			Token    string `json:"sender_token"`
			Account  int64  `json:"source_account_id"`
			Username string `json:"source_username"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Account != 7 || body.Username != "tester" || r.Header.Get("Authorization") != "" {
			t.Fatal("wrong pairing identity")
		}
		if calls == 1 {
			firstToken = body.Token
			return nil, errors.New("lost response")
		}
		if body.Token != firstToken {
			t.Fatal("retry changed sender credential")
		}
		return response(200, `{"paired":true,"binding_id":"binding","username":"tester"}`), nil
	})}
	code := "V2E-" + strings.Repeat("a", 24)
	if err := e.Pair(context.Background(), "https://relay.example", code, ""); err == nil {
		t.Fatal("lost response must be reported")
	}
	cfg, _ := e.Store.Config()
	if cfg.RelayToken != "test-relay-secret" {
		t.Fatal("unconfirmed pairing replaced credentials")
	}
	var encrypted string
	_ = e.Store.DB.QueryRow("SELECT token FROM pending_pairing").Scan(&encrypted)
	if strings.Contains(encrypted, firstToken) {
		t.Fatal("stored plaintext credential")
	}
	if err := e.Pair(context.Background(), "https://relay.example", code, ""); err != nil {
		t.Fatal(err)
	}
	cfg, _ = e.Store.Config()
	if cfg.RelayToken != firstToken {
		t.Fatal("credential not committed")
	}
	d, _ := e.Store.RecentDeliveries()
	if d[0].Status != "rejected" {
		t.Fatal("old connection's queue must be retired")
	}
	if err := e.Pair(context.Background(), "https://relay.example", code, ""); err != nil {
		t.Fatal(err)
	}
}

func TestPairingRequiresVerifiedAccountAndMatchingReceipt(t *testing.T) {
	e, _ := relayFixture(t)
	code := "V2E-" + strings.Repeat("b", 24)
	if err := e.Pair(context.Background(), "https://relay.example", code, ""); err == nil {
		t.Fatal("unverified account paired")
	}
	st, _ := e.Store.State()
	st.Verified, st.Username, st.InitialPairingDone = true, "tester", true
	_ = e.Store.SaveState(st)
	e.Relay = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return response(200, `{"paired":true,"binding_id":"binding","username":"other"}`), nil
	})}
	if err := e.Pair(context.Background(), "https://relay.example", code, ""); err == nil {
		t.Fatal("mismatched receipt accepted")
	}
	cfg, _ := e.Store.Config()
	if cfg.RelayToken != "test-relay-secret" {
		t.Fatal("mismatch changed credentials")
	}
}
