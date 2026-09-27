package assistant

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

type assistantSource struct {
	MemberID int64  `json:"member_id"`
	Token    string `json:"token"`
}
type assistantConnection struct {
	Enabled   bool                       `json:"enabled"`
	ID        string                     `json:"id"`
	Secret    string                     `json:"secret"`
	Sources   map[string]assistantSource `json:"sources"`
	NextSync  time.Time                  `json:"next_sync"`
	LastError string                     `json:"last_error"`
}
type AssistantConnectionView struct {
	Enabled   bool   `json:"enabled"`
	LastError string `json:"last_error"`
}

func (a *Accounts) connectionStorage() (*Store, error) {
	key, err := loadOrCreateSecret(filepath.Join(a.dir, "connection.key"), 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Store{DB: a.DB, aead: aead}, nil
}
func (a *Accounts) loadConnection() (assistantConnection, error) {
	value := assistantConnection{Sources: map[string]assistantSource{}}
	var raw string
	err := a.DB.QueryRow("SELECT body FROM assistant_connection WHERE id=1").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return value, nil
	}
	if err != nil {
		return value, err
	}
	store, err := a.connectionStorage()
	if err != nil {
		return value, err
	}
	raw, err = store.unseal(raw)
	if err != nil {
		return value, err
	}
	err = json.Unmarshal([]byte(raw), &value)
	return value, err
}
func (a *Accounts) saveConnection(value assistantConnection) error {
	store, err := a.connectionStorage()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = a.DB.Exec("INSERT INTO assistant_connection(id,body) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", store.seal(string(raw)))
	return err
}
func (a *Accounts) ConnectionView() (AssistantConnectionView, error) {
	a.reuseMu.Lock()
	defer a.reuseMu.Unlock()
	value, err := a.loadConnection()
	return AssistantConnectionView{value.Enabled, value.LastError}, err
}
func (a *Accounts) EnableConnection() error {
	a.reuseMu.Lock()
	defer a.reuseMu.Unlock()
	value, err := a.loadConnection()
	if err != nil {
		return err
	}
	if value.ID == "" {
		raw := make([]byte, 16)
		if _, err = rand.Read(raw); err != nil {
			return err
		}
		raw[6] = (raw[6] & 15) | 64
		raw[8] = (raw[8] & 63) | 128
		id := hex.EncodeToString(raw)
		value.ID = id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
		token := make([]byte, 32)
		if _, err = rand.Read(token); err != nil {
			return err
		}
		value.Secret = base64.RawURLEncoding.EncodeToString(token)
	}
	value.Enabled = true
	value.NextSync = time.Time{}
	return a.saveConnection(value)
}
func assistantRequest(ctx context.Context, client *http.Client, method, path, secret string, body, result any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, PushServiceURL+path, strings.NewReader(string(raw)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("多账号连接暂时不可用，将自动重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("多账号连接同步失败（HTTP %d），请检查推送服务版本或已有连接冲突", resp.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(result); err != nil {
		return errors.New("无法读取多账号连接状态")
	}
	return nil
}

// Independent of collection and delivery schedules; no Cookie/PAT is sent to the relay.
func (a *Accounts) SyncConnection(ctx context.Context, now time.Time) error {
	a.reuseMu.Lock()
	defer a.reuseMu.Unlock()
	value, err := a.loadConnection()
	if err != nil || !value.Enabled || now.Before(value.NextSync) {
		return err
	}
	value.NextSync = now.Add(3 * time.Minute)
	if err = a.saveConnection(value); err != nil {
		return err
	}
	a.mu.Lock()
	engines := make(map[string]*Engine, len(a.engines))
	for id, e := range a.engines {
		engines[id] = e
	}
	a.mu.Unlock()
	var failures []error
	// Removed accounts retain a durable cleanup task until the server confirms revocation.
	for id, source := range value.Sources {
		if _, ok := engines[id]; ok {
			continue
		}
		var result struct {
			Revoked bool `json:"revoked"`
		}
		err = assistantRequest(ctx, a.connectionClient, http.MethodDelete, fmt.Sprintf("/v1/assistants/%s/accounts/%d", value.ID, source.MemberID), value.Secret, nil, &result)
		if err != nil || !result.Revoked {
			failures = append(failures, errors.New("已移除账号的连接撤销等待重试"))
			continue
		}
		delete(value.Sources, id)
	}
	for id, e := range engines {
		if err = a.syncConnectionAccount(ctx, id, e, &value); err != nil {
			failures = append(failures, err)
		}
	}
	value.LastError = ""
	if len(failures) > 0 {
		value.LastError = failures[0].Error()
	}
	if err = a.saveConnection(value); err != nil {
		return err
	}
	return errors.Join(failures...)
}
func (a *Accounts) syncConnectionAccount(ctx context.Context, id string, e *Engine, value *assistantConnection) error {
	e.relayMu.Lock()
	defer e.relayMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed {
		return nil
	}
	cfg, err := e.Store.Config()
	if err != nil {
		return err
	}
	st, err := e.Store.State()
	if err != nil {
		return err
	}
	if !st.Verified || st.AuthBlocked || st.AccountID <= 0 || cfg.Cookie == "" || st.CookieIssue != "" {
		return nil
	}
	source := value.Sources[id]
	if cfg.RelayToken != "" {
		source.Token = cfg.RelayToken
	}
	if source.Token == "" {
		raw := make([]byte, 32)
		if _, err = rand.Read(raw); err != nil {
			return err
		}
		source.Token = base64.RawURLEncoding.EncodeToString(raw)
	}
	source.MemberID = st.AccountID
	value.Sources[id] = source
	// Persist before publication, including before the first unpaired account is admitted.
	if err = a.saveConnection(*value); err != nil {
		return err
	}
	var result struct {
		Published    bool   `json:"published"`
		Ready        bool   `json:"ready"`
		ConnectionID string `json:"connection_id"`
	}
	err = assistantRequest(ctx, e.Relay, http.MethodPost, "/v1/assistants/"+value.ID+"/accounts", value.Secret, map[string]any{"source_account_id": st.AccountID, "username": st.Username, "sender_token": source.Token}, &result)
	if err != nil {
		return err
	}
	if !result.Published || result.ConnectionID == "" {
		return errors.New("多账号连接回执不完整")
	}
	if !result.Ready || cfg.RelayToken == source.Token {
		return nil
	}
	var unread *WebUnreadSnapshot
	if !st.InitialPairingDone {
		snapshot, err := e.readWebUnread(ctx, cfg.Cookie, st.Username)
		if err != nil {
			return err
		}
		unread = &snapshot
	}
	return e.finishPairing(cfg, st, PushServiceURL, source.Token, unread)
}
