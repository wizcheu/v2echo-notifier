package assistant

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPairingInvitationPersistsSecretsAndRecoversOnExplicitConfirmation(t *testing.T) {
	e, _ := relayFixture(t)
	st, _ := e.Store.State()
	st.Username, st.Verified = "tester", true
	if err := e.Store.SaveState(st); err != nil {
		t.Fatal(err)
	}
	e.Web = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://www.v2ex.com/" || r.Header.Get("Cookie") != "A2=qr-private-cookie" {
			t.Fatal("incorrect cookie request")
		}
		return response(200, webPage("tester", 3)), nil
	})}
	var token, code string
	creates, confirms := 0, 0
	expiry := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
	e.Relay = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "qr-private-cookie") || r.Header.Get("Cookie") != "" {
			t.Fatal("cookie leaked")
		}
		if token == "" {
			token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Fatal("retry changed credential")
		}
		if r.URL.Path == "/api/push/v1/invitations" {
			creates++
			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			if code == "" {
				code = body.Code
			}
			if body.Code != code {
				t.Fatal("lost response changed code")
			}
			if creates == 1 {
				return nil, errors.New("lost create response")
			}
			return response(200, `{"expires_at":"`+expiry+`"}`), nil
		}
		if r.URL.Path != "/api/push/v1/invitations/exchange" {
			t.Fatal("unexpected endpoint")
		}
		confirms++
		if confirms == 1 {
			return nil, errors.New("lost confirmation response")
		}
		return response(200, `{"paired":true,"binding_id":"binding","username":"tester"}`), nil
	})}
	if _, err := e.CreatePairingInvitation(t.Context(), "A2=qr-private-cookie"); err == nil {
		t.Fatal("expected uncertain create")
	}
	view, err := e.CreatePairingInvitation(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if view.Code != code || view.Payload != "v2echo://push-pair?code="+code || strings.Contains(view.Payload, token) {
		t.Fatal("invalid QR payload")
	}
	var stored string
	if err := e.Store.DB.QueryRow("SELECT body FROM pairing_invitation").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{code, token, "qr-private-cookie"} {
		if strings.Contains(stored, secret) {
			t.Fatal("plaintext pairing secret stored")
		}
	}
	if confirms != 0 || stateOf(t, e.Store).InitialPairingDone {
		t.Fatal("creation must not confirm or initialize")
	}
	if _, err := e.CompletePairingInvitation(t.Context(), code); err == nil {
		t.Fatal("expected uncertain confirm")
	}
	cfg, _ := e.Store.Config()
	if cfg.RelayToken != "test-relay-secret" {
		t.Fatal("unconfirmed result replaced old binding")
	}
	paired, err := e.CompletePairingInvitation(t.Context(), code)
	if err != nil || !paired {
		t.Fatalf("confirmation: %v %v", paired, err)
	}
	cfg, _ = e.Store.Config()
	st = stateOf(t, e.Store)
	if cfg.RelayToken != token || cfg.Cookie != "A2=qr-private-cookie" || !st.InitialPairingDone || st.InitialUnreadCount != 3 {
		t.Fatal("pairing not committed")
	}
	if paired, err := e.CompletePairingInvitation(t.Context(), code); err != nil || !paired || confirms != 2 {
		t.Fatal("local retry must not request the service again")
	}
}

func TestPairingInvitationRejectsExpiredOrChangedAccountWithoutRequests(t *testing.T) {
	e, _ := relayFixture(t)
	st, _ := e.Store.State()
	st.Username, st.Verified = "tester", true
	if err := e.Store.SaveState(st); err != nil {
		t.Fatal(err)
	}
	e.Relay = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("unexpected service request")
		return nil, errors.New("unexpected")
	})}
	invitation := pairingInvitation{Code: "V2N-" + strings.Repeat("a", 24), Token: "private-sender", Username: "tester", AccountID: 7, ExpiresAt: time.Now().Add(-time.Minute)}
	if err := e.Store.saveInvitation(invitation); err != nil {
		t.Fatal(err)
	}
	if _, err := e.CompletePairingInvitation(t.Context(), invitation.Code); err == nil {
		t.Fatal("expired invitation accepted")
	}
	invitation.ExpiresAt = time.Now().Add(time.Minute)
	invitation.Username = "other"
	if err := e.Store.saveInvitation(invitation); err != nil {
		t.Fatal(err)
	}
	if _, err := e.CompletePairingInvitation(t.Context(), invitation.Code); err == nil {
		t.Fatal("another account accepted")
	}
}
