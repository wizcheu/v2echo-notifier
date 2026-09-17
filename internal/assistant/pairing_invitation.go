package assistant

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// The QR carries only Code. Token and Cookie stay encrypted on this server.
type pairingInvitation struct {
	Code      string
	Token     string
	Username  string
	AccountID int64
	Cookie    string
	Unread    *WebUnreadSnapshot
	ExpiresAt time.Time
}

type PairingInvitationView struct {
	Code      string    `json:"code"`
	Payload   string    `json:"payload"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Store) loadInvitation() (pairingInvitation, error) {
	var encrypted string
	var invitation pairingInvitation
	if err := s.DB.QueryRow("SELECT body FROM pairing_invitation WHERE id=1").Scan(&encrypted); err != nil {
		return invitation, err
	}
	raw, err := s.unseal(encrypted)
	if err != nil {
		return invitation, err
	}
	err = json.Unmarshal([]byte(raw), &invitation)
	return invitation, err
}

func (s *Store) saveInvitation(invitation pairingInvitation) error {
	raw, err := json.Marshal(invitation)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec("INSERT INTO pairing_invitation(id,body) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", s.seal(string(raw)))
	return err
}

func (e *Engine) CreatePairingInvitation(ctx context.Context, cookie string) (PairingInvitationView, error) {
	e.relayMu.Lock()
	defer e.relayMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	var view PairingInvitationView
	if e.removed {
		return view, ErrAccountRemoved
	}
	cfg, err := e.Store.Config()
	if err != nil {
		return view, err
	}
	st, err := e.Store.State()
	if err != nil {
		return view, err
	}
	if !st.Verified || st.AuthBlocked || st.AccountID <= 0 || st.Username == "" {
		return view, errors.New("请先验证 V2EX 账号后再配对")
	}
	invitation, err := e.Store.loadInvitation()
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return view, err
	}
	if invitation.ExpiresAt.Before(time.Now()) || invitation.Username != st.Username || invitation.AccountID != st.AccountID || cfg.RelayToken == invitation.Token {
		invitation = pairingInvitation{Username: st.Username, AccountID: st.AccountID, ExpiresAt: time.Now().Add(10 * time.Minute)}
		if !st.InitialPairingDone {
			if cookie == "" {
				cookie = cfg.Cookie
			}
			snapshot, err := e.readWebUnread(ctx, cookie, st.Username)
			if err != nil {
				return view, err
			}
			invitation.Cookie, err = normalizeWebCookie(cookie)
			if err != nil {
				return view, err
			}
			invitation.Unread = &snapshot
		}
		code, token := make([]byte, 18), make([]byte, 32)
		if _, err := rand.Read(code); err != nil {
			return view, err
		}
		if _, err := rand.Read(token); err != nil {
			return view, err
		}
		invitation.Code = "V2N-" + base64.RawURLEncoding.EncodeToString(code)
		invitation.Token = base64.RawURLEncoding.EncodeToString(token)
		// Persist first so a lost response or restart reuses the same credentials.
		if err = e.Store.saveInvitation(invitation); err != nil {
			return view, err
		}
	}
	var result struct {
		ExpiresAt time.Time `json:"expires_at"`
	}
	err = e.invitationRequest(ctx, "/v1/invitations", invitation.Token, map[string]any{
		"code": invitation.Code, "source_account_id": st.AccountID, "source_username": st.Username,
	}, &result)
	if err != nil {
		return view, err
	}
	if !result.ExpiresAt.After(time.Now()) || result.ExpiresAt.After(time.Now().Add(11*time.Minute)) {
		return view, errors.New("二维码有效期不正确，请稍后重试")
	}
	invitation.ExpiresAt = result.ExpiresAt
	if err = e.Store.saveInvitation(invitation); err != nil {
		return view, err
	}
	return PairingInvitationView{invitation.Code, "v2echo://push-pair?code=" + invitation.Code, invitation.ExpiresAt}, nil
}

func (e *Engine) CompletePairingInvitation(ctx context.Context, code string) (bool, error) {
	e.relayMu.Lock()
	defer e.relayMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed {
		return false, ErrAccountRemoved
	}
	invitation, err := e.Store.loadInvitation()
	if err != nil || invitation.Code != code {
		return false, errors.New("二维码已失效，请重新生成")
	}
	cfg, err := e.Store.Config()
	if err != nil {
		return false, err
	}
	st, err := e.Store.State()
	if err != nil {
		return false, err
	}
	if !st.Verified || st.AuthBlocked || invitation.Username != st.Username || invitation.AccountID != st.AccountID {
		return false, errors.New("账号状态已变更，请重新验证后配对")
	}
	if cfg.RelayToken == invitation.Token {
		return true, nil // A lost local response must not reset the binding or queue.
	}
	if !invitation.ExpiresAt.After(time.Now()) {
		return false, errors.New("二维码已过期，请重新生成")
	}
	var result struct {
		Paired    bool   `json:"paired"`
		BindingID string `json:"binding_id"`
		Username  string `json:"username"`
	}
	if err = e.invitationRequest(ctx, "/v1/invitations/exchange", invitation.Token, map[string]string{"code": code}, &result); err != nil {
		return false, err
	}
	if !result.Paired {
		return false, nil
	}
	if result.BindingID == "" || !strings.EqualFold(result.Username, st.Username) {
		return false, errors.New("配对回执账号不匹配，请重新生成二维码")
	}
	var unread *WebUnreadSnapshot
	if !st.InitialPairingDone {
		if invitation.Unread == nil || invitation.Cookie == "" {
			return false, errors.New("缺少首页验证结果，请重新生成二维码")
		}
		unread = invitation.Unread
		if cfg.Cookie == "" {
			cfg.Cookie = invitation.Cookie
		}
	}
	if err = e.finishPairing(cfg, st, PushServiceURL, invitation.Token, unread); err != nil {
		return false, err
	}
	return true, nil
}

func (e *Engine) invitationRequest(ctx context.Context, path, token string, body, result any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, PushServiceURL+path, strings.NewReader(string(raw)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := e.Relay.Do(req)
	if err != nil {
		return errors.New("暂时无法确认扫码配对结果，请点击重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		return errors.New("二维码或 App 配对请求已失效，请等待二维码到期后重新生成")
	}
	if resp.StatusCode != http.StatusOK {
		return errors.New("推送服务暂不支持扫码配对或暂时不可用，请稍后重试，也可使用手动配对")
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(result); err != nil {
		return errors.New("无法读取扫码配对结果，请重试")
	}
	return nil
}
