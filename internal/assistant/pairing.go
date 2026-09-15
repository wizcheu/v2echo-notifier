package assistant

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var pairingCode = regexp.MustCompile(`^V2E-[A-Za-z0-9_-]{24}$`)

// Persist before exchange so a restart or lost response can redeem the same
// code with the same credential. Only the encrypted credential is stored.
func (s *Store) pairingToken(fingerprint string) (string, error) {
	var saved, encrypted string
	err := s.DB.QueryRow("SELECT fingerprint,token FROM pending_pairing WHERE id=1").Scan(&saved, &encrypted)
	if err == nil && saved == fingerprint {
		return s.unseal(encrypted)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	_, err = s.DB.Exec("INSERT INTO pending_pairing(id,fingerprint,token) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET fingerprint=excluded.fingerprint,token=excluded.token", fingerprint, s.seal(token))
	return token, err
}

func (e *Engine) Pair(ctx context.Context, relayURL, code, cookie string) error {
	e.relayMu.Lock()
	defer e.relayMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed {
		return ErrAccountRemoved
	}
	relayURL = strings.TrimRight(strings.TrimSpace(relayURL), "/")
	code = strings.TrimSpace(code)
	u, err := url.Parse(relayURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("推送服务地址必须为不含用户名、查询参数或片段的 HTTPS 地址")
	}
	if !pairingCode.MatchString(code) {
		return errors.New("配对码格式不正确，请从接收设备重新复制")
	}
	cfg, err := e.Store.Config()
	if err != nil {
		return err
	}
	st, err := e.Store.State()
	if err != nil {
		return err
	}
	if !st.Verified || st.AuthBlocked || st.AccountID <= 0 || st.Username == "" {
		return errors.New("请先保存并启用 V2EX 连接，等待账号验证完成后再配对")
	}

	var unread *WebUnreadSnapshot
	if cookie == "" {
		cookie = cfg.Cookie
	}
	if !st.InitialPairingDone {
		snapshot, err := e.readWebUnread(ctx, cookie, st.Username)
		if err != nil {
			return err
		}
		unread = &snapshot
		cfg.Cookie, err = normalizeWebCookie(cookie)
		if err != nil {
			return err
		}
	}
	fingerprint := sha256.Sum256([]byte(relayURL + "\n" + code + "\n" + st.Username))
	token, err := e.Store.pairingToken(hex.EncodeToString(fingerprint[:]))
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"code": code, "sender_token": token, "source_account_id": st.AccountID, "source_username": st.Username})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, relayURL+"/v1/pairings/exchange", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := e.Relay.Do(req)
	if err != nil {
		return errors.New("无法确认配对结果，请在配对码有效期内使用同一码重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 403 {
		return errors.New("配对码已失效、已被使用，或接收设备登录的账号不一致")
	}
	if resp.StatusCode != 200 {
		return errors.New("推送服务暂时无法配对，请稍后使用同一码重试")
	}
	var result struct {
		Paired    bool   `json:"paired"`
		BindingID string `json:"binding_id"`
		Username  string `json:"username"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result); err != nil || !result.Paired || result.BindingID == "" || !strings.EqualFold(result.Username, st.Username) {
		return errors.New("配对回执不匹配，请使用同一码重试")
	}
	changed := cfg.RelayURL != relayURL || cfg.RelayToken != token
	retire := changed && cfg.RelayToken != ""
	cfg.RelayURL, cfg.RelayToken = relayURL, token
	st.CheckRequested = false

	if unread != nil {
		st.CookieIssue = ""
		st.CookieCheckedAt = unread.ObservedAt
		st.HasWebUnread = true
		st.WebUnreadCount, st.WebObservedAt = unread.Count, unread.ObservedAt
		st.InitialPairingDone = true
		st.InitialUnreadCount, st.InitialUnreadAt = unread.Count, unread.ObservedAt

	}
	// Keep pending_pairing for idempotent retries after the local commit too.
	return e.Store.saveConfig(cfg, st, changed, retire, nil)
}
