package assistant

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type bodyRecipient struct {
	DeviceID  string `json:"device_id"`
	BindingID string `json:"binding_id"`
	KeyID     string `json:"key_id"`
	PublicKey string `json:"public_key"`
}

type encryptedNotificationBody struct {
	DeviceID     string `json:"device_id"`
	BindingID    string `json:"binding_id"`
	KeyID        string `json:"key_id"`
	EphemeralKey string `json:"ephemeral_key"`
	Nonce        string `json:"nonce"`
	Ciphertext   string `json:"ciphertext"`
}

var bodyUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type notificationHTTPError struct{ header http.Header }

func (*notificationHTTPError) Error() string {
	return "通知加密服务暂不可用，保留原事件等待重试"
}

func notificationRetryAt(err error, now time.Time) int64 {
	wait := 3 * time.Minute
	if response, ok := errors.AsType[*notificationHTTPError](err); ok {
		wait = max(wait, retryAfter(response.header, now))
	}
	return now.Add(wait).Unix()
}

func bodyAAD(event Event, recipient bodyRecipient) string {
	return fmt.Sprintf("V2EchoPushContent/v2\n%s\n%d\n%s\n%s\n%s", event.EventID, event.SourceAccountID, recipient.DeviceID, recipient.BindingID, recipient.KeyID)
}

// A two-byte big-endian UTF-8 title length precedes title and body. This keeps
// control characters from expanding the APNs JSON and bounds plaintext to 1128 bytes.
func notificationPlaintext(event Event) []byte {
	excerpt := func(value, fallback string, limit int) []byte {
		if strings.TrimSpace(value) == "" {
			value = fallback
		}
		text := []rune(value)
		if len(text) > limit {
			text = append(text[:limit], '…')
		}
		return []byte(string(text))
	}
	title := excerpt(event.Title, "V2Echo 通知", 100)
	body := excerpt(event.Body, "打开应用查看详情", 180)
	plaintext := binary.BigEndian.AppendUint16(nil, uint16(len(title)))
	plaintext = append(plaintext, title...)
	return append(plaintext, body...)
}

// Each event/recipient gets a fresh ephemeral X25519 key and 96-bit GCM nonce.
// HKDF salt is SHA-256(recipient public key); context is also authenticated by GCM.
func sealNotificationBody(event Event, recipient bodyRecipient, random io.Reader) (encryptedNotificationBody, error) {
	var envelope encryptedNotificationBody
	publicBytes, err := base64.RawURLEncoding.Strict().DecodeString(recipient.PublicKey)
	if err != nil {
		return envelope, err
	}
	public, err := ecdh.X25519().NewPublicKey(publicBytes)
	if err != nil {
		return envelope, err
	}
	ephemeral, err := ecdh.X25519().GenerateKey(random)
	if err != nil {
		return envelope, err
	}
	shared, err := ephemeral.ECDH(public)
	if err != nil {
		return envelope, err
	}
	salt := sha256.Sum256(publicBytes)
	if hex.EncodeToString(salt[:]) != recipient.KeyID {
		return envelope, errors.New("recipient key ID mismatch")
	}
	aad := bodyAAD(event, recipient)
	key, err := hkdf.Key(sha256.New, shared, salt[:], aad, 32)
	if err != nil {
		return envelope, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return envelope, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return envelope, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(random, nonce); err != nil {
		return envelope, err
	}
	ciphertext := aead.Seal(nil, nonce, notificationPlaintext(event), []byte(aad))
	return encryptedNotificationBody{recipient.DeviceID, recipient.BindingID, recipient.KeyID,
		base64.RawURLEncoding.EncodeToString(ephemeral.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(nonce), base64.RawURLEncoding.EncodeToString(ciphertext)}, nil
}

func (e *Engine) bodyRecipients(ctx context.Context, cfg Config, account int64) ([]bodyRecipient, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.RelayURL+"/v1/recipient-keys", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.RelayToken)
	req.Header.Set("Accept", "application/json")
	resp, err := e.Relay.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, ErrPairingRequired
	}
	if resp.StatusCode != 200 {
		return nil, &notificationHTTPError{header: resp.Header.Clone()}
	}
	var directory struct {
		Account    int64            `json:"source_account_id"`
		Recipients *[]bodyRecipient `json:"recipients"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&directory); err != nil || directory.Account != account || directory.Recipients == nil || len(*directory.Recipients) > 10 {
		return nil, errors.New("invalid recipient directory")
	}
	recipients := *directory.Recipients
	seen := make(map[string]bool)
	tx, err := e.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, recipient := range recipients {
		public, err := base64.RawURLEncoding.Strict().DecodeString(recipient.PublicKey)
		digest := sha256.Sum256(public)
		if err != nil || len(public) != 32 || !bodyUUID.MatchString(recipient.DeviceID) || !bodyUUID.MatchString(recipient.BindingID) || seen[recipient.DeviceID] || recipient.KeyID != hex.EncodeToString(digest[:]) {
			return nil, errors.New("invalid recipient key")
		}
		seen[recipient.DeviceID] = true
		var pinned string
		err = tx.QueryRowContext(ctx, "SELECT public_key FROM notification_key_pins WHERE device_id=?", recipient.DeviceID).Scan(&pinned)
		if err == nil && pinned != recipient.PublicKey {
			return nil, errors.New("recipient key changed")
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO notification_key_pins(device_id,public_key) VALUES(?,?)", recipient.DeviceID, recipient.PublicKey); err != nil {
			return nil, err
		}
	}
	return recipients, tx.Commit()
}

var errPreEncryptionAttempt = errors.New("pre-encryption event already attempted")

// The local history retains the original body. Only this separately persisted,
// immutable projection can cross the relay boundary, including ambiguous retries.
func (e *Engine) encryptedWire(ctx context.Context, cfg Config, event Event, attempts int, recipients *[]bodyRecipient) (json.RawMessage, error) {
	var cached string
	err := e.Store.DB.QueryRowContext(ctx, "SELECT payload FROM encrypted_notification_wire WHERE event_id=?", event.EventID).Scan(&cached)
	if err == nil {
		var version struct {
			Details    int `json:"details_version"`
			Encryption int `json:"body_encryption"`
		}
		if err = json.Unmarshal([]byte(cached), &version); err != nil {
			return nil, err
		}
		if event.Type == "notification" && version.Details == 3 || event.Type == "test" && version.Encryption == 2 {
			return json.RawMessage(cached), nil
		}
		// The previous body-only format exposed its title. Never resend it or
		// rewrite a submission whose original hash may already exist remotely.
		if attempts > 0 {
			return nil, errPreEncryptionAttempt
		}
		if _, err = e.Store.DB.ExecContext(ctx, "DELETE FROM encrypted_notification_wire WHERE event_id=?", event.EventID); err != nil {
			return nil, err
		}
		err = sql.ErrNoRows
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// Rewriting an ambiguous old plaintext submission would violate its server
	// idempotency hash. Do not resend that plaintext or invent a duplicate ID.
	if attempts > 0 {
		return nil, errPreEncryptionAttempt
	}
	if *recipients == nil {
		keys, err := e.bodyRecipients(ctx, cfg, event.SourceAccountID)
		if err != nil {
			return nil, err
		}
		*recipients = keys
	}
	envelopes := make([]encryptedNotificationBody, 0, len(*recipients))
	for _, recipient := range *recipients {
		envelope, err := sealNotificationBody(event, recipient, rand.Reader)
		if err != nil {
			return nil, err
		}
		envelopes = append(envelopes, envelope)
	}
	wire := map[string]any{"event_id": event.EventID, "type": event.Type, "source_account_id": event.SourceAccountID,
		"created_at": event.CreatedAt, "expires_at": event.ExpiresAt, "encrypted_bodies": envelopes}
	if event.Type == "test" {
		wire["test_kind"], wire["body_encryption"] = event.TestKind, 2
	} else {
		wire["details_version"], wire["notification_id"] = 3, event.NotificationID
		if event.NotificationKind != "" {
			wire["notification_kind"] = event.NotificationKind
		}
		if event.UnreadCount > 0 {
			wire["unread_count"] = event.UnreadCount
		}
		if event.AvatarURL != "" {
			wire["avatar_url"] = event.AvatarURL
		}
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	_, err = e.Store.DB.ExecContext(ctx, "INSERT INTO encrypted_notification_wire(event_id,payload) VALUES(?,?)", event.EventID, string(raw))
	return raw, err
}

func encryptedBodyEvent(event Event) bool {
	return event.Type == "notification" || event.Type == "test" && strings.TrimSpace(event.TestKind) != ""
}
