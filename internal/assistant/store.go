package assistant

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	DB   *sql.DB
	aead cipher.AEAD
}

func loadOrCreateSecret(path string, size int) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(decoded) != size {
			return nil, fmt.Errorf("invalid secret file: %s", path)
		}
		return decoded, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	raw = make([]byte, size)
	if _, err = rand.Read(raw); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	_, writeErr := f.WriteString(base64.RawURLEncoding.EncodeToString(raw) + "\n")
	closeErr := f.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	return raw, closeErr
}

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	key, err := loadOrCreateSecret(filepath.Join(dir, "encryption.key"), 32)
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
	dbPath := filepath.Join(dir, "notifier.db")
	f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || (version != 0 && version != 4) {
		db.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("账号数据库格式不受此版本支持，请使用新的数据目录")
	}
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
      CREATE TABLE IF NOT EXISTS settings (id INTEGER PRIMARY KEY CHECK(id=1), body TEXT NOT NULL);
      CREATE TABLE IF NOT EXISTS sync_state (id INTEGER PRIMARY KEY CHECK(id=1), body TEXT NOT NULL);
      CREATE TABLE IF NOT EXISTS notifications (
        account_id INTEGER NOT NULL, id INTEGER NOT NULL, created INTEGER NOT NULL,
        title TEXT NOT NULL, body TEXT NOT NULL, raw TEXT NOT NULL,
        PRIMARY KEY(account_id,id));
      CREATE TABLE IF NOT EXISTS outbox (
        event_id TEXT PRIMARY KEY, payload TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending',
        submitted INTEGER NOT NULL DEFAULT 0, attempts INTEGER NOT NULL DEFAULT 0,
        priority INTEGER NOT NULL DEFAULT 0, next_attempt INTEGER NOT NULL DEFAULT 0, detail TEXT NOT NULL DEFAULT '');
      CREATE INDEX IF NOT EXISTS outbox_due ON outbox(status,next_attempt);
      CREATE TABLE IF NOT EXISTS push_test_schedule (id INTEGER PRIMARY KEY CHECK(id=1),next_allowed INTEGER NOT NULL DEFAULT 0,event_id TEXT NOT NULL DEFAULT '');
      INSERT OR IGNORE INTO push_test_schedule(id) VALUES(1);
      CREATE TABLE IF NOT EXISTS delivery_history (
        event_id TEXT PRIMARY KEY, queued_at INTEGER NOT NULL DEFAULT 0,
        first_attempt_at INTEGER NOT NULL DEFAULT 0, last_attempt_at INTEGER NOT NULL DEFAULT 0,
        submitted_at INTEGER NOT NULL DEFAULT 0, finished_at INTEGER NOT NULL DEFAULT 0);
      CREATE TRIGGER IF NOT EXISTS outbox_history_insert AFTER INSERT ON outbox BEGIN
        INSERT OR IGNORE INTO delivery_history(event_id,queued_at)
          VALUES(NEW.event_id,CAST(strftime('%s','now') AS INTEGER));
      END;
      CREATE TRIGGER IF NOT EXISTS outbox_history_update AFTER UPDATE OF attempts,status,submitted ON outbox
      WHEN NEW.attempts<>OLD.attempts OR NEW.status<>OLD.status OR NEW.submitted<>OLD.submitted BEGIN
        INSERT OR IGNORE INTO delivery_history(event_id) VALUES(NEW.event_id);
        UPDATE delivery_history SET
          first_attempt_at=CASE WHEN OLD.attempts=0 AND NEW.attempts>0 AND first_attempt_at=0 THEN CAST(strftime('%s','now') AS INTEGER) ELSE first_attempt_at END,
          last_attempt_at=CASE WHEN NEW.attempts>OLD.attempts THEN CAST(strftime('%s','now') AS INTEGER) ELSE last_attempt_at END,
          submitted_at=CASE WHEN OLD.submitted=0 AND NEW.submitted=1 AND submitted_at=0 THEN CAST(strftime('%s','now') AS INTEGER) ELSE submitted_at END,
          finished_at=CASE WHEN NEW.status<>OLD.status AND NEW.status IN ('apns_accepted','rejected','expired','skipped') THEN CAST(strftime('%s','now') AS INTEGER) ELSE finished_at END
        WHERE event_id=NEW.event_id;
      END;
      CREATE TABLE IF NOT EXISTS pending_pairing (id INTEGER PRIMARY KEY CHECK(id=1), fingerprint TEXT NOT NULL, token TEXT NOT NULL);
      CREATE TABLE IF NOT EXISTS relay_schedule (id INTEGER PRIMARY KEY CHECK(id=1), next_sync INTEGER NOT NULL, failures INTEGER NOT NULL, blocked INTEGER NOT NULL DEFAULT 0);
      INSERT OR IGNORE INTO relay_schedule(id,next_sync,failures) VALUES(1,0,0);
      PRAGMA user_version=4;`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db, aead}, nil
}

func (s *Store) seal(text string) string {
	if text == "" {
		return ""
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(s.aead.Seal(nonce, nonce, []byte(text), []byte("v2echo-notifier-config-v1")))
}

func (s *Store) unseal(text string) (string, error) {
	if text == "" {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(text)
	if err != nil || len(raw) < s.aead.NonceSize() {
		return "", errors.New("invalid encrypted configuration")
	}
	decoded, err := s.aead.Open(nil, raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():], []byte("v2echo-notifier-config-v1"))
	return string(decoded), err
}

func (s *Store) Config() (Config, error) {
	c := Config{IntervalSeconds: 180, ProxyMode: "environment"}
	var raw string
	err := s.DB.QueryRow("SELECT body FROM settings WHERE id=1").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return c, err
	}
	if c.APIToken, err = s.unseal(c.APIToken); err != nil {
		return c, err
	}
	if c.Cookie, err = s.unseal(c.Cookie); err != nil {
		return c, err
	}
	if c.RelayToken, err = s.unseal(c.RelayToken); err != nil {
		return c, err
	}
	c.ProxyURL, err = s.unseal(c.ProxyURL)
	if c.ProxyMode == "" {
		c.ProxyMode = "environment"
	}
	return c, err
}

func (s *Store) State() (State, error) {
	v := State{Phase: "history", Page: 1}
	var raw string
	err := s.DB.QueryRow("SELECT body FROM sync_state WHERE id=1").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	err = json.Unmarshal([]byte(raw), &v)
	return v, err
}

type sqlExec interface {
	Exec(string, ...any) (sql.Result, error)
}

func saveState(ex sqlExec, v State) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = ex.Exec("INSERT INTO sync_state(id,body) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", string(raw))
	return err
}

func (s *Store) SaveState(v State) error { return saveState(s.DB, v) }

func (s *Store) SaveConfig(c Config, v State, retryBlocked bool, retireOld ...bool) error {
	return s.saveConfig(c, v, retryBlocked, len(retireOld) > 0 && retireOld[0], nil)
}

func (s *Store) saveConfig(c Config, v State, retryBlocked, retireOld bool, initial *Event) error {
	c.Cookie = s.seal(c.Cookie)
	c.APIToken = s.seal(c.APIToken)
	c.RelayToken = s.seal(c.RelayToken)
	c.ProxyURL = s.seal(c.ProxyURL)
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO settings(id,body) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", string(raw)); err != nil {
		return err
	}
	if err = saveState(tx, v); err != nil {
		return err
	}
	if retryBlocked || retireOld {
		if _, err = tx.Exec("UPDATE relay_schedule SET blocked=0 WHERE id=1"); err != nil {
			return err
		}
	}
	if retireOld {
		if _, err = tx.Exec("UPDATE outbox SET status='rejected',detail='接收设备或凭据已更换，旧连接的待处理事件不再投递' WHERE status IN ('pending','blocked')"); err != nil {
			return err
		}
	} else if retryBlocked {
		if _, err = tx.Exec("UPDATE outbox SET status='pending',next_attempt=0,detail='' WHERE status='blocked'"); err != nil {
			return err
		}
	}
	if c.Cookie != "" {
		if _, err = tx.Exec(`UPDATE outbox SET status='skipped',detail='已启用网页汇总，尚未尝试的旧版提醒已跳过'
		 WHERE status IN ('pending','blocked') AND submitted=0 AND attempts=0
		 AND json_extract(payload,'$.type') IN ('notification','initial_unread')`); err != nil {
			return err
		}
	}
	if initial != nil {
		payload, err := json.Marshal(initial)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO outbox(event_id,payload,priority,detail) VALUES(?,?,1,'首次配对未读汇总，等待上报')", initial.EventID, string(payload)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// Each page and its checkpoint commit together. A crash can replay a page, but
// cannot advance the cursor without persisting notifications and push events.
func (s *Store) ImportPage(v State, items []Notification, notify bool) error {
	return s.importPage(v, items, notify, nil, false)
}

func (s *Store) ImportPageAndUnread(v State, items []Notification, summary *Event) error {
	return s.importPage(v, items, false, summary, true)
}

func (s *Store) importPage(v State, items []Notification, notify bool, summary *Event, observed bool) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, n := range items {
		raw, err := json.Marshal(n)
		if err != nil {
			return err
		}
		title, body := plain(n.Text, 220), plain(n.PayloadRendered, 500)
		if body == "" && n.Payload != nil {
			body = plain(*n.Payload, 500)
		}
		_, err = tx.Exec(`INSERT INTO notifications(account_id,id,created,title,body,raw) VALUES(?,?,?,?,?,?)
          ON CONFLICT(account_id,id) DO UPDATE SET title=excluded.title,body=excluded.body,raw=excluded.raw`, v.AccountID, n.ID, n.Created, title, body, string(raw))
		if err != nil {
			return err
		}
		if notify && n.ID > v.AnchorID {
			sum := sha256.Sum256([]byte(fmt.Sprintf("v2ex:%d:%d", v.AccountID, n.ID)))
			ev := Event{EventID: hex.EncodeToString(sum[:]), Type: "notification", SourceAccountID: v.AccountID, NotificationID: n.ID, Title: title, Body: body, CreatedAt: time.Unix(n.Created, 0).UTC(), ExpiresAt: time.Unix(n.Created, 0).UTC().Add(24 * time.Hour)}
			payload, err := json.Marshal(ev)
			if err != nil {
				return err
			}
			_, err = tx.Exec("INSERT OR IGNORE INTO outbox(event_id,payload) VALUES(?,?)", ev.EventID, string(payload))
			if err != nil {
				return err
			}
		}
	}
	if observed {
		summaryID := ""
		if summary != nil {
			summaryID = summary.EventID
		}
		if _, err = tx.Exec(`UPDATE outbox SET status='skipped',detail='网页或 API 快照已更新，未提交的旧快照已跳过'
		 WHERE status='pending' AND submitted=0 AND attempts=0 AND json_extract(payload,'$.type')='unread_summary'
		 AND (?=0 OR (?<>'' AND event_id<>?))`, v.WebUnreadCount, summaryID, summaryID); err != nil {
			return err
		}
	}
	if summary != nil {
		raw, err := json.Marshal(summary)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT OR IGNORE INTO outbox(event_id,payload,priority,detail) VALUES(?,?,1,?)", summary.EventID, string(raw), "网页未读汇总已加入队列"); err != nil {
			return err
		}
	}
	if err = saveState(tx, v); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SaveDelivery(d Delivery) error {
	_, err := s.DB.Exec("UPDATE outbox SET status=?,submitted=?,attempts=?,next_attempt=?,detail=? WHERE event_id=?", d.Status, d.Submitted, d.Attempts, d.NextAttempt, d.Detail, d.EventID)
	return err
}

func (s *Store) RecentDeliveries() ([]Delivery, error) {
	rows, err := s.DB.Query("SELECT event_id,status,submitted,attempts,next_attempt,detail,COALESCE(json_extract(payload,'$.type'),'notification') FROM outbox ORDER BY rowid DESC LIMIT 50")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Delivery{}
	for rows.Next() {
		var d Delivery
		if err = rows.Scan(&d.EventID, &d.Status, &d.Submitted, &d.Attempts, &d.NextAttempt, &d.Detail, &d.Type); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

// Erase only this account. The registry, shared budget and other account
// databases are owned by Accounts and are never reached through this store.
func (s *Store) EraseAccount() error {
	if _, err := s.DB.Exec("PRAGMA secure_delete=ON"); err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"settings", "sync_state", "notifications", "outbox", "delivery_history", "push_test_schedule", "pending_pairing", "relay_schedule"} {
		if _, err = tx.Exec("DELETE FROM " + table); err != nil {
			return err
		}
	}
	return tx.Commit()
}
