package assistant

import (
	"crypto/hmac"
	"crypto/sha256"
	"time"
)

const maxManagementSessions = 32

// This fingerprint binds sessions to the current administrator secret without
// storing either that secret or the hash used to authenticate login requests.
func (s *Server) managementSessionBinding() []byte {
	mac := hmac.New(sha256.New, s.adminHash[:])
	mac.Write([]byte("v2echo-notifier/management-sessions/v1"))
	return mac.Sum(nil)
}

func (s *Server) cleanManagementSessions(now time.Time) error {
	_, err := s.Accounts.DB.Exec("DELETE FROM management_sessions WHERE expires_at<=? OR admin_binding<>?", now.UnixNano(), s.managementSessionBinding())
	return err
}

func (s *Server) createManagementSession(token string, now time.Time) error {
	tx, err := s.Accounts.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	binding := s.managementSessionBinding()
	if _, err = tx.Exec("DELETE FROM management_sessions WHERE expires_at<=? OR admin_binding<>?", now.UnixNano(), binding); err != nil {
		return err
	}
	// Retain the newest sessions when the limit is reached, rather than logging
	// out every browser. Eviction and insertion commit together.
	if _, err = tx.Exec(`DELETE FROM management_sessions WHERE token_hash IN (
		SELECT token_hash FROM management_sessions ORDER BY created_at DESC, rowid DESC LIMIT -1 OFFSET ?
	)`, maxManagementSessions-1); err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(token))
	if _, err = tx.Exec("INSERT INTO management_sessions(token_hash,admin_binding,created_at,expires_at) VALUES(?,?,?,?)", hash[:], binding, now.UnixNano(), now.Add(managementSessionLifetime).UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Server) validManagementSession(token string, now time.Time) (bool, error) {
	if err := s.cleanManagementSessions(now); err != nil {
		return false, err
	}
	hash := sha256.Sum256([]byte(token))
	var valid bool
	err := s.Accounts.DB.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM management_sessions WHERE token_hash=? AND admin_binding=? AND expires_at>?
	)`, hash[:], s.managementSessionBinding(), now.UnixNano()).Scan(&valid)
	return valid, err
}

func (s *Server) revokeManagementSession(token string) error {
	hash := sha256.Sum256([]byte(token))
	_, err := s.Accounts.DB.Exec("DELETE FROM management_sessions WHERE token_hash=?", hash[:])
	return err
}
