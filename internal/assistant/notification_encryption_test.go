package assistant

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func bodyFixture(t *testing.T) (*ecdh.PrivateKey, bodyRecipient, Event, encryptedNotificationBody) {
	t.Helper()
	raw, err := os.ReadFile("testdata/push-content-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Private  string `json:"private_key"`
		Body     string `json:"body"`
		Title    string `json:"title"`
		UserInfo struct {
			EventID   string                    `json:"event_id"`
			Encrypted encryptedNotificationBody `json:"encrypted_body"`
		} `json:"user_info"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	keyBytes, _ := base64.RawURLEncoding.DecodeString(fixture.Private)
	private, err := ecdh.X25519().NewPrivateKey(keyBytes)
	if err != nil {
		t.Fatal(err)
	}
	envelope := fixture.UserInfo.Encrypted
	recipient := bodyRecipient{envelope.DeviceID, envelope.BindingID, envelope.KeyID, base64.RawURLEncoding.EncodeToString(private.PublicKey().Bytes())}
	event := Event{EventID: fixture.UserInfo.EventID, Type: "notification", SourceAccountID: 7, NotificationID: 42, Title: fixture.Title, Body: fixture.Body,
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	return private, recipient, event, envelope
}

func openBody(private *ecdh.PrivateKey, event Event, recipient bodyRecipient, envelope encryptedNotificationBody) ([]byte, error) {
	ephemeralBytes, _ := base64.RawURLEncoding.DecodeString(envelope.EphemeralKey)
	ephemeral, err := ecdh.X25519().NewPublicKey(ephemeralBytes)
	if err != nil {
		return nil, err
	}
	shared, err := private.ECDH(ephemeral)
	if err != nil {
		return nil, err
	}
	salt := sha256.Sum256(private.PublicKey().Bytes())
	aad := bodyAAD(event, recipient)
	key, err := hkdf.Key(sha256.New, shared, salt[:], aad, 32)
	if err != nil {
		return nil, err
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	nonce, _ := base64.RawURLEncoding.DecodeString(envelope.Nonce)
	sealed, _ := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
	return gcm.Open(nil, nonce, sealed, []byte(aad))
}

func TestNotificationBodyCrossLanguageVectorAndAuthentication(t *testing.T) {
	private, recipient, event, fixture := bodyFixture(t)
	text, err := openBody(private, event, recipient, fixture)
	if err != nil || !bytes.Equal(text, notificationPlaintext(event)) {
		t.Fatal(string(text), err)
	}
	a, err := sealNotificationBody(event, recipient, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	b, err := sealNotificationBody(event, recipient, rand.Reader)
	if err != nil || a.Nonce == b.Nonce || a.EphemeralKey == b.EphemeralKey || a.Ciphertext == b.Ciphertext {
		t.Fatal("encryption randomness reused", err)
	}
	text, err = openBody(private, event, recipient, a)
	if err != nil || !bytes.Equal(text, notificationPlaintext(event)) {
		t.Fatal(string(text), err)
	}
	for _, change := range []func(*Event, *bodyRecipient, *encryptedNotificationBody){
		func(e *Event, _ *bodyRecipient, _ *encryptedNotificationBody) { e.EventID = strings.Repeat("b", 64) },
		func(e *Event, _ *bodyRecipient, _ *encryptedNotificationBody) { e.SourceAccountID++ },
		func(_ *Event, r *bodyRecipient, _ *encryptedNotificationBody) {
			r.DeviceID = "33333333-3333-4333-8333-333333333333"
		},
		func(_ *Event, r *bodyRecipient, _ *encryptedNotificationBody) {
			r.BindingID = "33333333-3333-4333-8333-333333333333"
		},
		func(_ *Event, r *bodyRecipient, _ *encryptedNotificationBody) { r.KeyID = strings.Repeat("b", 64) },
		func(_ *Event, _ *bodyRecipient, e *encryptedNotificationBody) {
			e.Ciphertext = strings.Repeat("A", len(e.Ciphertext))
		},
	} {
		e, r, envelope := event, recipient, a
		change(&e, &r, &envelope)
		if _, err := openBody(private, e, r, envelope); err == nil {
			t.Fatal("tampered context authenticated")
		}
	}
	event.Body = strings.Repeat("👋", 500)
	event.Title = strings.Repeat("👋", 220)
	long, err := sealNotificationBody(event, recipient, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	text, err = openBody(private, event, recipient, long)
	if err != nil || len(text) != 1128 || !bytes.Equal(text, notificationPlaintext(event)) {
		t.Fatal("unbounded ciphertext", len(text), err)
	}
	event.Body = ""
	empty, err := sealNotificationBody(event, recipient, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	text, err = openBody(private, event, recipient, empty)
	if err != nil || !bytes.HasSuffix(text, []byte("打开应用查看详情")) {
		t.Fatal("empty legacy body lost its fallback", err)
	}
}

func TestEncryptedWirePinnedKeysDurableRetriesAndLocalHistory(t *testing.T) {
	e, now := pushTestFixture(t)
	private, recipient, event, _ := bodyFixture(t)
	event.NotificationKind = "mention"
	event.CreatedAt, event.ExpiresAt = now, now.Add(time.Hour)
	local, _ := json.Marshal(event)
	if _, err := e.Store.DB.Exec("INSERT INTO outbox(event_id,payload) VALUES(?,?)", event.EventID, string(local)); err != nil {
		t.Fatal(err)
	}
	var first string
	directoryCalls, postCalls := 0, 0
	transport := transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/health") {
			return notificationCapabilitiesResponse(), nil
		}
		if strings.HasSuffix(r.URL.Path, "/recipient-keys") {
			directoryCalls++
			if r.Header.Get("Authorization") != "Bearer synthetic-sender" || r.Header.Get("Cookie") != "" {
				t.Fatal("recipient credentials")
			}
			raw, _ := json.Marshal(map[string]any{"source_account_id": 7, "recipients": []bodyRecipient{recipient}})
			return response(200, string(raw)), nil
		}
		postCalls++
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), event.Body) || strings.Contains(string(raw), event.Title) || strings.Contains(string(raw), `"title":`) || strings.Contains(string(raw), `"body":`) {
			t.Fatal("plaintext escaped onto the wire")
		}
		if postCalls == 1 {
			first = string(raw)
			return nil, errors.New("ambiguous timeout")
		}
		if string(raw) != first {
			t.Fatal("retry changed ciphertext after restart")
		}
		return response(200, `{"receipts":[{"event_id":"`+event.EventID+`","status":"apns_accepted"}]}`), nil
	})
	e.Relay.Transport = transport
	if err := e.Deliver(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	e = NewEngine(e.Store)
	e.Relay.Transport = transport
	if err := e.Deliver(t.Context(), now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if directoryCalls != 1 || postCalls != 2 {
		t.Fatal(directoryCalls, postCalls)
	}
	var batch struct {
		Events []struct {
			Envelopes []encryptedNotificationBody `json:"encrypted_bodies"`
			Kind      string                      `json:"notification_kind"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(first), &batch); err != nil {
		t.Fatal(err)
	}
	if batch.Events[0].Kind != "mention" {
		t.Fatal("routing category lost during retry")
	}
	plaintext, err := openBody(private, event, recipient, batch.Events[0].Envelopes[0])
	if err != nil || !bytes.Equal(plaintext, notificationPlaintext(event)) {
		t.Fatal("wire cannot decrypt", err)
	}
	if item := historyOne(t, e.Store); item.Body != event.Body {
		t.Fatal("encryption destroyed local history")
	}
	// A new event must not silently trust a replacement key for an existing device.
	other, _ := ecdh.X25519().GenerateKey(rand.Reader)
	recipient.PublicKey = base64.RawURLEncoding.EncodeToString(other.PublicKey().Bytes())
	digest := sha256.Sum256(other.PublicKey().Bytes())
	recipient.KeyID = hex.EncodeToString(digest[:])
	cfg, _ := e.Store.Config()
	if _, err := e.bodyRecipients(t.Context(), cfg, 7); err == nil {
		t.Fatal("changed pinned public key accepted")
	}
}

func TestBodyEncryptionNeverFallsBackToLegacyPlaintext(t *testing.T) {
	for _, kind := range []string{"notification", "reply", "mention", "thanks_topic", "thanks_reply", "favorite"} {
		t.Run(kind, func(t *testing.T) {
			e, now := pushTestFixture(t)
			_, _, event, _ := bodyFixture(t)
			event.CreatedAt, event.ExpiresAt = now, now.Add(time.Hour)
			if kind != "notification" {
				event.Type, event.TestKind = "test", kind
			}
			cfg, _ := e.Store.Config()
			e.Relay.Transport = detailedTransport(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected request"); return nil, nil })
			var recipients []bodyRecipient
			raw, err := e.encryptedWire(t.Context(), cfg, event, 0, &recipients)
			if err != nil || strings.Contains(string(raw), `"title":`) || strings.Contains(string(raw), `"body":`) || !strings.Contains(string(raw), `"encrypted_bodies":[]`) {
				t.Fatal(string(raw), err)
			}
		})
	}
	e, _ := pushTestFixture(t)
	_, _, event, _ := bodyFixture(t)
	cfg, _ := e.Store.Config()
	var recipients []bodyRecipient
	if _, err := e.encryptedWire(t.Context(), cfg, event, 1, &recipients); !errors.Is(err, errPreEncryptionAttempt) {
		t.Fatal("ambiguous old event rewritten", err)
	}
}

func TestTitleEncryptionUpgradeNeverResendsCachedClearTitles(t *testing.T) {
	for _, attempted := range []bool{false, true} {
		t.Run(fmt.Sprint(attempted), func(t *testing.T) {
			e, _ := pushTestFixture(t)
			_, _, event, _ := bodyFixture(t)
			old := `{"details_version":2,"title":"private sender","encrypted_bodies":[]}`
			if _, err := e.Store.DB.Exec("INSERT INTO encrypted_notification_wire(event_id,payload) VALUES(?,?)", event.EventID, old); err != nil {
				t.Fatal(err)
			}
			cfg, _ := e.Store.Config()
			e.Relay.Transport = detailedTransport(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected request"); return nil, nil })
			attempts := 0
			if attempted {
				attempts = 1
			}
			var recipients []bodyRecipient
			raw, err := e.encryptedWire(t.Context(), cfg, event, attempts, &recipients)
			if attempted {
				if !errors.Is(err, errPreEncryptionAttempt) || raw != nil {
					t.Fatal("old title resent", string(raw), err)
				}
			} else if err != nil || strings.Contains(string(raw), `"title"`) || !strings.Contains(string(raw), `"details_version":3`) {
				t.Fatal("unsubmitted old payload not upgraded", string(raw), err)
			}
		})
	}
}

func TestTitleEncryptionWaitsForBodyOnlyServiceUpgrade(t *testing.T) {
	e, _ := pushTestFixture(t)
	e.Relay.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `{"notification_body_encryption":1,"notification_details":1,"notification_test_samples":1,"max_events":20,"max_receipts":50,"max_sync_bytes":65536}`), nil
	})
	cfg, _ := e.Store.Config()
	if _, err := e.notificationCapabilities(t.Context(), cfg.RelayURL); err == nil {
		t.Fatal("title encryption capability was not required")
	}
}

func TestEncryptedBatchTrimsTenRecipientPayloadsWithoutConsumingOverflow(t *testing.T) {
	e, now := pushTestFixture(t)
	recipients := make([]bodyRecipient, 10)
	for i := range recipients {
		key, _ := ecdh.X25519().GenerateKey(rand.Reader)
		digest := sha256.Sum256(key.PublicKey().Bytes())
		recipients[i] = bodyRecipient{fmt.Sprintf("%08d-1111-4111-8111-111111111111", i+1), "22222222-2222-4222-8222-222222222222", hex.EncodeToString(digest[:]), base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())}
	}
	for i := range 20 {
		event := notificationEvent(stateOf(t, e.Store), notification(int64(i+1)), now, 20)
		event.Body = strings.Repeat("👋", 500)
		raw, _ := json.Marshal(event)
		if _, err := e.Store.DB.Exec("INSERT INTO outbox(event_id,payload) VALUES(?,?)", event.EventID, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	sent := 0
	e.Relay.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/health") {
			return notificationCapabilitiesResponse(), nil
		}
		if strings.HasSuffix(r.URL.Path, "/recipient-keys") {
			raw, _ := json.Marshal(map[string]any{"source_account_id": 7, "recipients": recipients})
			return response(200, string(raw)), nil
		}
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 64<<10 || strings.Contains(string(raw), "👋") {
			t.Fatal("oversized or plaintext batch")
		}
		var batch syncRequest
		if err := json.Unmarshal(raw, &batch); err != nil {
			t.Fatal(err)
		}
		sent = len(batch.Events)
		if sent < 1 || sent >= 20 {
			t.Fatal("encrypted batch did not trim", sent)
		}
		return nil, errors.New("synthetic network timeout")
	})
	if err := e.Deliver(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if untouched := queryInt(t, e.Store, "SELECT COUNT(*) FROM outbox WHERE attempts=0"); untouched != 20-sent {
		t.Fatal("overflow consumed", untouched, sent)
	}
}

func TestRecipientDirectoryFailuresDoNotSendOrCountEventAttempts(t *testing.T) {
	for _, status := range []int{401, 403, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			e, now := relayFixture(t)
			e.Relay.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/health") {
					return notificationCapabilitiesResponse(), nil
				}
				if !strings.HasSuffix(r.URL.Path, "/recipient-keys") {
					t.Fatal("directory failure submitted an event")
				}
				res := response(status, "")
				res.Header.Set("Retry-After", "86400")
				return res, nil
			})
			if err := e.Deliver(t.Context(), now); err != nil {
				t.Fatal(err)
			}
			if attempts := queryInt(t, e.Store, "SELECT SUM(attempts) FROM outbox"); attempts != 0 {
				t.Fatal(attempts)
			}
			blocked, err := e.Store.RelayBlocked()
			if err != nil || blocked != (status == 401 || status == 403) {
				t.Fatal(blocked, err)
			}
			if !blocked && int64(queryInt(t, e.Store, "SELECT next_sync FROM relay_schedule")) < now.Add(24*time.Hour).Unix() {
				t.Fatal("directory Retry-After ignored")
			}
		})
	}
}
